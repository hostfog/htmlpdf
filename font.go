package htmlpdf

import (
	"fmt"
	"strings"
)

// Font is a TrueType font made available to stylesheets under Family.
type Font struct {
	Family string
	Bold   bool
	Italic bool
	Data   []byte
}

type stdMetrics struct {
	ascent, descent int
	widths          [224]uint16
}

// face is one font as used on the page: a standard 14 font or an embedded
// TrueType font.
type face struct {
	res  string // resource name, e.g. F1
	std  *stdMetrics
	base string
	ttf  *trueType
}

func (f *face) has(r rune) bool {
	if f.ttf != nil {
		return f.ttf.glyph(r) != 0
	}
	_, ok := winAnsi(r)
	return ok
}

// advance returns the width of r in 1/1000 em.
func (f *face) advance(r rune) float64 {
	if f.ttf != nil {
		return f.ttf.advance(f.ttf.glyph(r))
	}
	c, ok := winAnsi(r)
	if !ok {
		c = '?'
	}
	return float64(f.std.widths[c-32])
}

func (f *face) width(s string, size float64) float64 {
	var w float64
	for _, r := range s {
		w += f.advance(r)
	}
	return w * size / 1000
}

func (f *face) ascent() float64 {
	if f.ttf != nil {
		return f.ttf.ascent
	}
	return float64(f.std.ascent)
}

func (f *face) descent() float64 {
	if f.ttf != nil {
		return -f.ttf.descent
	}
	return float64(-f.std.descent)
}

// encode returns s as a string operand for the Tj operator.
func (f *face) encode(s string) string {
	if f.ttf != nil {
		var b strings.Builder
		b.WriteByte('<')
		for _, r := range s {
			fmt.Fprintf(&b, "%04X", f.ttf.use(r))
		}
		b.WriteByte('>')
		return b.String()
	}
	out := make([]byte, 0, len(s))
	for _, r := range s {
		c, ok := winAnsi(r)
		if !ok {
			c = '?'
		}
		out = append(out, c)
	}
	return literal(out)
}

var winAnsiHigh = map[rune]byte{
	'€': 0x80, '‚': 0x82, 'ƒ': 0x83, '„': 0x84, '…': 0x85, '†': 0x86, '‡': 0x87,
	'ˆ': 0x88, '‰': 0x89, 'Š': 0x8A, '‹': 0x8B, 'Œ': 0x8C, 'Ž': 0x8E, '‘': 0x91,
	'’': 0x92, '“': 0x93, '”': 0x94, '•': 0x95, '–': 0x96, '—': 0x97, '˜': 0x98,
	'™': 0x99, 'š': 0x9A, '›': 0x9B, 'œ': 0x9C, 'ž': 0x9E, 'Ÿ': 0x9F,
}

func winAnsi(r rune) (byte, bool) {
	switch {
	case r >= 0x20 && r <= 0x7E, r >= 0xA0 && r <= 0xFF:
		return byte(r), true
	}
	c, ok := winAnsiHigh[r]
	return c, ok
}

// fontSet resolves CSS font-family lists to faces and tracks the faces used.
type fontSet struct {
	families map[string]*[4]*trueType
	order    []*trueType
	faces    map[string]*face
	used     []*face
}

func newFontSet(fonts []Font) (*fontSet, error) {
	fs := &fontSet{families: map[string]*[4]*trueType{}, faces: map[string]*face{}}
	for _, f := range fonts {
		t, err := parseTrueType(f.Data)
		if err != nil {
			return nil, fmt.Errorf("htmlpdf: font %q: %w", f.Family, err)
		}
		key := strings.ToLower(f.Family)
		fam := fs.families[key]
		if fam == nil {
			fam = new([4]*trueType)
			fs.families[key] = fam
		}
		t.variant = variant(f.Bold, f.Italic)
		fam[t.variant] = t
		fs.order = append(fs.order, t)
	}
	return fs, nil
}

func variant(bold, italic bool) int {
	v := 0
	if bold {
		v |= 1
	}
	if italic {
		v |= 2
	}
	return v
}

var stdFamilies = map[string]string{
	"helvetica": "Helvetica", "arial": "Helvetica", "sans-serif": "Helvetica",
	"system-ui": "Helvetica", "verdana": "Helvetica", "tahoma": "Helvetica",
	"times": "Times", "times new roman": "Times", "serif": "Times", "georgia": "Times",
	"courier": "Courier", "courier new": "Courier", "monospace": "Courier",
}

// face returns the first family in families that is available.
func (fs *fontSet) face(families []string, bold, italic bool) *face {
	for _, fam := range families {
		key := strings.ToLower(fam)
		if t := fs.families[key]; t != nil {
			return fs.ttfFace(t, bold, italic)
		}
		if base, ok := stdFamilies[key]; ok {
			return fs.stdFace(base, bold, italic)
		}
	}
	return fs.stdFace("Helvetica", bold, italic)
}

func (fs *fontSet) ttfFace(fam *[4]*trueType, bold, italic bool) *face {
	want := variant(bold, italic)
	t := fam[want]
	for _, v := range []int{want &^ 2, want &^ 1, 0, 1, 2, 3} {
		if t == nil {
			t = fam[v]
		}
	}
	return fs.faceFor(t)
}

func (fs *fontSet) faceFor(t *trueType) *face {
	key := fmt.Sprintf("ttf:%p", t)
	if f := fs.faces[key]; f != nil {
		return f
	}
	f := &face{ttf: t}
	fs.faces[key] = f
	return f
}

func (fs *fontSet) stdFace(base string, bold, italic bool) *face {
	switch {
	case base == "Times" && bold && italic:
		base = "Times-BoldItalic"
	case base == "Times" && bold:
		base = "Times-Bold"
	case base == "Times" && italic:
		base = "Times-Italic"
	case base == "Times":
		base = "Times-Roman"
	case bold && italic:
		base += "-BoldOblique"
	case bold:
		base += "-Bold"
	case italic:
		base += "-Oblique"
	}
	if f := fs.faces[base]; f != nil {
		return f
	}
	f := &face{std: standardFonts[base], base: base}
	fs.faces[base] = f
	return f
}

// fallback returns a registered font that can draw r, preferring the
// requested variant, or nil.
func (fs *fontSet) fallback(r rune, bold, italic bool) *face {
	var found *trueType
	for _, t := range fs.order {
		if t.glyph(r) == 0 {
			continue
		}
		if t.variant == variant(bold, italic) {
			return fs.faceFor(t)
		}
		if found == nil {
			found = t
		}
	}
	if found == nil {
		return nil
	}
	return fs.faceFor(found)
}

// markUsed assigns f a resource name the first time it is drawn.
func (fs *fontSet) markUsed(f *face) {
	if f.res == "" {
		fs.used = append(fs.used, f)
		f.res = fmt.Sprintf("F%d", len(fs.used))
	}
}

func (fs *fontSet) write(p *pdfFile) (map[string]int, error) {
	refs := map[string]int{}
	for _, f := range fs.used {
		if f.ttf == nil {
			refs[f.res] = p.add("<< /Type /Font /Subtype /Type1 /BaseFont /" + f.base + " /Encoding /WinAnsiEncoding >>")
			continue
		}
		id, err := f.ttf.write(p)
		if err != nil {
			return nil, err
		}
		refs[f.res] = id
	}
	return refs, nil
}
