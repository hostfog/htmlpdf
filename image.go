package htmlpdf

import (
	"bytes"
	"fmt"
	"image"
	ic "image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
)

type pdfImage struct {
	w, h  int
	res   string
	dict  string
	data  []byte
	raw   bool // data is already encoded
	smask []byte
}

func (r *renderer) image(src string) (*pdfImage, error) {
	if img, ok := r.images[src]; ok {
		return img, nil
	}
	data, err := r.load(src)
	if err != nil {
		return nil, err
	}
	img, err := decodeImage(data)
	if err != nil {
		return nil, fmt.Errorf("htmlpdf: image %.64q: %w", src, err)
	}
	r.images[src] = img
	return img, nil
}

func decodeImage(data []byte) (*pdfImage, error) {
	if bytes.HasPrefix(data, []byte{0xFF, 0xD8}) {
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		space := "/DeviceRGB"
		switch cfg.ColorModel {
		case ic.GrayModel:
			space = "/DeviceGray"
		case ic.CMYKModel:
			space = "/DeviceCMYK /Decode [1 0 1 0 1 0 1 0]"
		}
		return &pdfImage{
			w: cfg.Width, h: cfg.Height, data: data, raw: true,
			dict: " /ColorSpace " + space + " /BitsPerComponent 8 /Filter /DCTDecode",
		}, nil
	}

	m, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := m.Bounds()
	img := &pdfImage{w: b.Dx(), h: b.Dy(), dict: " /ColorSpace /DeviceRGB /BitsPerComponent 8"}
	img.data = make([]byte, 0, 3*img.w*img.h)
	alpha := make([]byte, 0, img.w*img.h)
	opaque := true
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := ic.NRGBAModel.Convert(m.At(x, y)).(ic.NRGBA)
			img.data = append(img.data, c.R, c.G, c.B)
			alpha = append(alpha, c.A)
			opaque = opaque && c.A == 0xFF
		}
	}
	if !opaque {
		img.smask = alpha
	}
	return img, nil
}

func (img *pdfImage) write(p *pdfFile) int {
	dict := fmt.Sprintf(" /Type /XObject /Subtype /Image /Width %d /Height %d", img.w, img.h) + img.dict
	if img.smask != nil {
		mask := p.addStream(fmt.Sprintf(" /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceGray /BitsPerComponent 8", img.w, img.h), img.smask, true)
		dict += " /SMask " + ref(mask)
	}
	return p.addStream(dict, img.data, !img.raw)
}
