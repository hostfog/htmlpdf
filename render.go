package htmlpdf

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"strings"
)

const maxPages = 10000

type page struct {
	content bytes.Buffer
	annots  []string
}

// canvas turns layout items into page content streams.
type canvas struct {
	r      *renderer
	l      *layout
	pages  []*page
	images []*pdfImage
}

func (r *renderer) write(w io.Writer, l *layout) error {
	c := &canvas{r: r, l: l}
	n := 1
	for _, it := range l.items {
		n = max(n, l.page(it.y+math.Max(it.h, 0)-2*eps)+1)
	}
	if n >= maxPages {
		return fmt.Errorf("htmlpdf: document needs %d pages or more", maxPages)
	}
	for range n {
		c.pages = append(c.pages, &page{})
	}
	for i := range l.items {
		c.paint(&l.items[i])
	}
	return c.finish(w)
}

func (c *canvas) paint(it *item) {
	l := c.l
	switch it.kind {
	case boxItem:
		for p := l.page(it.y); p <= l.page(it.y+it.h-2*eps); p++ {
			c.box(it, p)
		}
	case textItem:
		pg := l.page(it.y)
		c.r.fonts.markUsed(it.face)
		fmt.Fprintf(&c.page(pg).content, "BT /%s %s Tf %s rg %s %s Td %s Tj ET\n",
			it.face.res, num(it.size), rgb(it.color), num(c.x(it.x)), num(c.y(it.y+it.base, pg)), it.face.encode(it.text))
	case imageItem:
		pg := l.page(it.y)
		if it.img.res == "" {
			c.images = append(c.images, it.img)
			it.img.res = fmt.Sprintf("Im%d", len(c.images))
		}
		fmt.Fprintf(&c.page(pg).content, "q %s 0 0 %s %s %s cm /%s Do Q\n",
			num(it.w), num(it.h), num(c.x(it.x)), num(c.y(it.y+it.h, pg)), it.img.res)
	case linkItem:
		pg := l.page(it.y)
		p := c.page(pg)
		p.annots = append(p.annots, fmt.Sprintf("<< /Type /Annot /Subtype /Link /Rect [%s %s %s %s] /Border [0 0 0] /A << /S /URI /URI %s >> >>",
			num(c.x(it.x)), num(c.y(it.y+it.h, pg)), num(c.x(it.x+it.w)), num(c.y(it.y, pg)), literal([]byte(it.href))))
	}
}

func (c *canvas) page(i int) *page {
	return c.pages[min(max(i, 0), len(c.pages)-1)]
}

func (c *canvas) x(v float64) float64 {
	return c.r.opts.Margins.Left + v
}

// y converts a layout y on page p to PDF user space.
func (c *canvas) y(v float64, p int) float64 {
	return c.r.opts.PageSize.Height - c.r.opts.Margins.Top - (v - float64(p)*c.l.pageH)
}

func rgb(c color) string {
	return fmt.Sprintf("%s %s %s", num(float64(c.r)/255), num(float64(c.g)/255), num(float64(c.b)/255))
}

// box paints the part of a background and border box that falls on page p.
// Edges cut by a page break get no border.
func (c *canvas) box(it *item, p int) {
	pageTop := float64(p) * c.l.pageH
	y0, y1 := math.Max(it.y, pageTop), math.Min(it.y+it.h, pageTop+c.l.pageH)
	cutTop, cutBottom := it.y < pageTop-eps, it.y+it.h > pageTop+c.l.pageH+eps
	x0, x1 := it.x, it.x+it.w
	out := &c.page(p).content
	rounded := it.radius > 0 && !cutTop && !cutBottom

	if it.bg.ok && x1 > x0 && y1 > y0 {
		fmt.Fprintf(out, "%s rg ", rgb(it.bg))
		if rounded {
			c.roundRect(out, x0, y0, x1, y1, it.radius, p)
			out.WriteString("f\n")
		} else {
			fmt.Fprintf(out, "%s %s %s %s re f\n", num(c.x(x0)), num(c.y(y1, p)), num(x1-x0), num(y1-y0))
		}
	}

	var w [4]float64
	for i, b := range it.border {
		if b.visible() {
			w[i] = b.width
		}
	}
	if cutTop {
		w[top] = 0
	}
	if cutBottom {
		w[bottom] = 0
	}
	if w == [4]float64{} {
		return
	}
	if it.centered {
		x0, x1 = x0-w[left]/2, x1+w[right]/2
		y0, y1 = y0-w[top]/2, y1+w[bottom]/2
	}

	b := it.border
	if rounded && w[0] == w[1] && w[1] == w[2] && w[2] == w[3] && b[0] == b[1] && b[1] == b[2] && b[2] == b[3] {
		h := w[0] / 2
		fmt.Fprintf(out, "q %s RG %s w %s", rgb(b[0].color), num(w[0]), dash(b[0]))
		c.roundRect(out, x0+h, y0+h, x1-h, y1-h, math.Max(it.radius-h, 0), p)
		out.WriteString("S Q\n")
		return
	}

	ix0, iy0, ix1, iy1 := x0+w[left], y0+w[top], x1-w[right], y1-w[bottom]
	sides := [4][4][2]float64{
		top:    {{x0, y0}, {x1, y0}, {ix1, iy0}, {ix0, iy0}},
		right:  {{x1, y0}, {x1, y1}, {ix1, iy1}, {ix1, iy0}},
		bottom: {{x1, y1}, {x0, y1}, {ix0, iy1}, {ix1, iy1}},
		left:   {{x0, y1}, {x0, y0}, {ix0, iy0}, {ix0, iy1}},
	}
	for i, quad := range sides {
		if w[i] == 0 {
			continue
		}
		if b[i].style == "dashed" || b[i].style == "dotted" {
			h := w[i] / 2
			var ax, ay, bx, by float64
			switch i {
			case top:
				ax, ay, bx, by = x0, y0+h, x1, y0+h
			case right:
				ax, ay, bx, by = x1-h, y0, x1-h, y1
			case bottom:
				ax, ay, bx, by = x0, y1-h, x1, y1-h
			case left:
				ax, ay, bx, by = x0+h, y0, x0+h, y1
			}
			fmt.Fprintf(out, "q %s RG %s w %s%s %s m %s %s l S Q\n", rgb(b[i].color), num(w[i]), dash(b[i]),
				num(c.x(ax)), num(c.y(ay, p)), num(c.x(bx)), num(c.y(by, p)))
			continue
		}
		fmt.Fprintf(out, "%s rg", rgb(b[i].color))
		for j, pnt := range quad {
			op := "l"
			if j == 0 {
				op = "m"
			}
			fmt.Fprintf(out, " %s %s %s", num(c.x(pnt[0])), num(c.y(pnt[1], p)), op)
		}
		out.WriteString(" h f\n")
	}
}

func dash(b border) string {
	switch b.style {
	case "dashed":
		d := math.Max(b.width*3, 2)
		return fmt.Sprintf("[%s %s] 0 d ", num(d), num(d))
	case "dotted":
		return fmt.Sprintf("1 J [0 %s] 0 d ", num(b.width*2))
	}
	return ""
}

// roundRect appends a rounded rectangle path in layout coordinates.
func (c *canvas) roundRect(out *bytes.Buffer, x0, y0, x1, y1, r float64, p int) {
	r = math.Min(r, math.Min(x1-x0, y1-y0)/2)
	k := r * (1 - 0.5523)
	pnt := func(x, y float64) string { return num(c.x(x)) + " " + num(c.y(y, p)) }
	fmt.Fprintf(out, "%s m %s l %s %s %s c %s l %s %s %s c %s l %s %s %s c %s l %s %s %s c h ",
		pnt(x0+r, y0), pnt(x1-r, y0),
		pnt(x1-k, y0), pnt(x1, y0+k), pnt(x1, y0+r), pnt(x1, y1-r),
		pnt(x1, y1-k), pnt(x1-k, y1), pnt(x1-r, y1), pnt(x0+r, y1),
		pnt(x0+k, y1), pnt(x0, y1-k), pnt(x0, y1-r), pnt(x0, y0+r),
		pnt(x0, y0+k), pnt(x0+k, y0), pnt(x0+r, y0))
}

func (c *canvas) finish(w io.Writer) error {
	f := &pdfFile{}
	catalog, pagesID := f.alloc(), f.alloc()

	fonts, err := c.r.fonts.write(f)
	if err != nil {
		return err
	}
	var res strings.Builder
	res.WriteString("<< /ProcSet [/PDF /Text /ImageB /ImageC]")
	if len(c.r.fonts.used) > 0 {
		res.WriteString(" /Font <<")
		for _, fc := range c.r.fonts.used {
			fmt.Fprintf(&res, " /%s %s", fc.res, ref(fonts[fc.res]))
		}
		res.WriteString(" >>")
	}
	if len(c.images) > 0 {
		res.WriteString(" /XObject <<")
		for _, img := range c.images {
			fmt.Fprintf(&res, " /%s %s", img.res, ref(img.write(f)))
		}
		res.WriteString(" >>")
	}
	res.WriteString(" >>")
	resources := f.add(res.String())

	size := c.r.opts.PageSize
	kids := make([]string, len(c.pages))
	for i, pg := range c.pages {
		content := f.addStream("", pg.content.Bytes(), true)
		annots := ""
		if len(pg.annots) > 0 {
			refs := make([]string, len(pg.annots))
			for j, a := range pg.annots {
				refs[j] = ref(f.add(a))
			}
			annots = " /Annots [" + strings.Join(refs, " ") + "]"
		}
		kids[i] = ref(f.add(fmt.Sprintf("<< /Type /Page /Parent %s /MediaBox [0 0 %s %s] /Resources %s /Contents %s%s >>",
			ref(pagesID), num(size.Width), num(size.Height), ref(resources), ref(content), annots)))
	}
	f.set(pagesID, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(kids)))
	f.set(catalog, fmt.Sprintf("<< /Type /Catalog /Pages %s >>", ref(pagesID)))

	info := "<< /Producer (htmlpdf)"
	if c.r.title != "" {
		info += " /Title " + textString(c.r.title)
	}
	return f.writeTo(w, catalog, f.add(info+" >>"))
}
