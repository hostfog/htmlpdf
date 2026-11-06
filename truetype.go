package htmlpdf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"unicode/utf16"

	"golang.org/x/image/font/sfnt"
)

type trueType struct {
	font    *sfnt.Font
	buf     sfnt.Buffer
	tables  map[string][]byte
	name    string
	variant int

	upem        float64
	numGlyphs   int
	numHMetrics int
	ascent      float64 // 1/1000 em
	descent     float64 // 1/1000 em, negative
	bbox        [4]float64

	gids map[rune]uint16
	used map[uint16]rune
}

func parseTrueType(data []byte) (*trueType, error) {
	f, err := sfnt.Parse(data)
	if err != nil {
		return nil, err
	}
	tables, err := readTables(data)
	if err != nil {
		return nil, err
	}
	for _, tag := range []string{"head", "hhea", "hmtx", "maxp", "loca", "glyf"} {
		if tables[tag] == nil {
			return nil, fmt.Errorf("missing %s table (only TrueType outlines are supported)", tag)
		}
	}
	head, hhea, maxp := tables["head"], tables["hhea"], tables["maxp"]
	if len(head) < 54 || len(hhea) < 36 || len(maxp) < 6 {
		return nil, errors.New("truncated font tables")
	}

	t := &trueType{
		font:        f,
		tables:      tables,
		upem:        float64(binary.BigEndian.Uint16(head[18:])),
		numGlyphs:   int(binary.BigEndian.Uint16(maxp[4:])),
		numHMetrics: int(binary.BigEndian.Uint16(hhea[34:])),
		gids:        map[rune]uint16{},
		used:        map[uint16]rune{},
	}
	if t.upem == 0 || t.numHMetrics == 0 || len(tables["hmtx"]) < 4*t.numHMetrics {
		return nil, errors.New("invalid font metrics")
	}
	t.ascent = t.scale(int16(binary.BigEndian.Uint16(hhea[4:])))
	t.descent = t.scale(int16(binary.BigEndian.Uint16(hhea[6:])))
	for i := range t.bbox {
		t.bbox[i] = t.scale(int16(binary.BigEndian.Uint16(head[36+2*i:])))
	}

	ps, err := f.Name(&t.buf, sfnt.NameIDPostScript)
	if err != nil || ps == "" {
		ps = "Font"
	}
	t.name = strings.Map(func(r rune) rune {
		if r < '!' || r > '~' || strings.ContainsRune("[](){}<>/%#", r) {
			return -1
		}
		return r
	}, ps)
	return t, nil
}

func readTables(data []byte) (map[string][]byte, error) {
	if len(data) < 12 {
		return nil, errors.New("not a TrueType font")
	}
	n := int(binary.BigEndian.Uint16(data[4:]))
	if len(data) < 12+16*n {
		return nil, errors.New("truncated table directory")
	}
	tables := map[string][]byte{}
	for i := 0; i < n; i++ {
		rec := data[12+16*i:]
		off := int(binary.BigEndian.Uint32(rec[8:]))
		size := int(binary.BigEndian.Uint32(rec[12:]))
		if off < 0 || size < 0 || off+size > len(data) {
			return nil, fmt.Errorf("table %q out of range", rec[:4])
		}
		tables[string(rec[:4])] = data[off : off+size]
	}
	return tables, nil
}

func (t *trueType) scale(v int16) float64 {
	return float64(v) * 1000 / t.upem
}

func (t *trueType) glyph(r rune) uint16 {
	if g, ok := t.gids[r]; ok {
		return g
	}
	g, err := t.font.GlyphIndex(&t.buf, r)
	if err != nil {
		g = 0
	}
	t.gids[r] = uint16(g)
	return uint16(g)
}

func (t *trueType) advance(g uint16) float64 {
	i := int(g)
	if i >= t.numHMetrics {
		i = t.numHMetrics - 1
	}
	return t.scale(int16(binary.BigEndian.Uint16(t.tables["hmtx"][4*i:])))
}

func (t *trueType) use(r rune) uint16 {
	g := t.glyph(r)
	if _, ok := t.used[g]; !ok {
		t.used[g] = r
	}
	return g
}

func (t *trueType) glyphRange(g int) (int, int) {
	loca := t.tables["loca"]
	if binary.BigEndian.Uint16(t.tables["head"][50:]) == 0 {
		if 2*g+4 > len(loca) {
			return 0, 0
		}
		return 2 * int(binary.BigEndian.Uint16(loca[2*g:])), 2 * int(binary.BigEndian.Uint16(loca[2*g+2:]))
	}
	if 4*g+8 > len(loca) {
		return 0, 0
	}
	return int(binary.BigEndian.Uint32(loca[4*g:])), int(binary.BigEndian.Uint32(loca[4*g+4:]))
}

// subset returns a font file in which every glyph not drawn is empty. Glyph
// ids are unchanged so the CID to GID mapping stays the identity.
func (t *trueType) subset() ([]byte, error) {
	glyf := t.tables["glyf"]
	keep := map[int]bool{}
	queue := []int{0}
	for g := range t.used {
		queue = append(queue, int(g))
	}
	for len(queue) > 0 {
		g := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if keep[g] || g >= t.numGlyphs {
			continue
		}
		keep[g] = true
		start, end := t.glyphRange(g)
		if start > end || end > len(glyf) {
			return nil, fmt.Errorf("glyph %d out of range", g)
		}
		queue = append(queue, components(glyf[start:end])...)
	}

	var newGlyf []byte
	newLoca := make([]byte, 4*(t.numGlyphs+1))
	for g := 0; g < t.numGlyphs; g++ {
		binary.BigEndian.PutUint32(newLoca[4*g:], uint32(len(newGlyf)))
		if !keep[g] {
			continue
		}
		start, end := t.glyphRange(g)
		newGlyf = append(newGlyf, glyf[start:end]...)
		for len(newGlyf)%4 != 0 {
			newGlyf = append(newGlyf, 0)
		}
	}
	binary.BigEndian.PutUint32(newLoca[4*t.numGlyphs:], uint32(len(newGlyf)))

	head := append([]byte(nil), t.tables["head"]...)
	binary.BigEndian.PutUint32(head[8:], 0)
	binary.BigEndian.PutUint16(head[50:], 1)

	out := map[string][]byte{
		"head": head, "hhea": t.tables["hhea"], "maxp": t.tables["maxp"],
		"hmtx": t.tables["hmtx"], "loca": newLoca, "glyf": newGlyf,
	}
	for _, tag := range []string{"cvt ", "fpgm", "prep"} {
		if b := t.tables[tag]; b != nil {
			out[tag] = b
		}
	}
	file := buildSFNT(out)
	adj := 0xB1B0AFBA - checksum(file)
	binary.BigEndian.PutUint32(file[headOffset(file)+8:], adj)
	return file, nil
}

// components returns the glyph ids referenced by a composite glyph.
func components(g []byte) []int {
	if len(g) < 10 || int16(binary.BigEndian.Uint16(g)) >= 0 {
		return nil
	}
	var ids []int
	p := 10
	for p+4 <= len(g) {
		flags := binary.BigEndian.Uint16(g[p:])
		ids = append(ids, int(binary.BigEndian.Uint16(g[p+2:])))
		p += 4
		if flags&0x0001 != 0 {
			p += 4
		} else {
			p += 2
		}
		switch {
		case flags&0x0008 != 0:
			p += 2
		case flags&0x0040 != 0:
			p += 4
		case flags&0x0080 != 0:
			p += 8
		}
		if flags&0x0020 == 0 {
			break
		}
	}
	return ids
}

func buildSFNT(tables map[string][]byte) []byte {
	tags := make([]string, 0, len(tables))
	for tag := range tables {
		tags = append(tags, tag)
	}
	sort.Strings(tags)

	n := len(tags)
	sr, es := 1, 0
	for sr*2 <= n {
		sr *= 2
		es++
	}
	head := make([]byte, 12+16*n)
	binary.BigEndian.PutUint32(head, 0x00010000)
	binary.BigEndian.PutUint16(head[4:], uint16(n))
	binary.BigEndian.PutUint16(head[6:], uint16(sr*16))
	binary.BigEndian.PutUint16(head[8:], uint16(es))
	binary.BigEndian.PutUint16(head[10:], uint16(n*16-sr*16))

	file := head
	for i, tag := range tags {
		data := tables[tag]
		rec := file[12+16*i:]
		copy(rec, tag)
		binary.BigEndian.PutUint32(rec[4:], checksum(data))
		binary.BigEndian.PutUint32(rec[8:], uint32(len(file)))
		binary.BigEndian.PutUint32(rec[12:], uint32(len(data)))
		file = append(file, data...)
		for len(file)%4 != 0 {
			file = append(file, 0)
		}
	}
	return file
}

func headOffset(file []byte) int {
	n := int(binary.BigEndian.Uint16(file[4:]))
	for i := 0; i < n; i++ {
		rec := file[12+16*i:]
		if string(rec[:4]) == "head" {
			return int(binary.BigEndian.Uint32(rec[8:]))
		}
	}
	return 0
}

func checksum(b []byte) uint32 {
	var sum uint32
	for i := 0; i < len(b); i += 4 {
		var w [4]byte
		copy(w[:], b[i:])
		sum += binary.BigEndian.Uint32(w[:])
	}
	return sum
}

func (t *trueType) write(p *pdfFile) (int, error) {
	data, err := t.subset()
	if err != nil {
		return 0, fmt.Errorf("htmlpdf: subsetting %s: %w", t.name, err)
	}
	gids := make([]int, 0, len(t.used))
	for g := range t.used {
		gids = append(gids, int(g))
	}
	sort.Ints(gids)

	h := fnv.New32a()
	fmt.Fprint(h, t.name, gids)
	sum := h.Sum32()
	tag := make([]byte, 6)
	for i := range tag {
		tag[i] = 'A' + byte(sum%26)
		sum /= 26
	}
	base := string(tag) + "+" + t.name

	file := p.addStream(fmt.Sprintf(" /Length1 %d", len(data)), data, true)
	flags := 4
	if t.variant&2 != 0 {
		flags |= 64
	}
	desc := p.add(fmt.Sprintf("<< /Type /FontDescriptor /FontName /%s /Flags %d /FontBBox [%s %s %s %s] /ItalicAngle 0 /Ascent %s /Descent %s /CapHeight %s /StemV 80 /FontFile2 %s >>",
		base, flags, num(t.bbox[0]), num(t.bbox[1]), num(t.bbox[2]), num(t.bbox[3]),
		num(t.ascent), num(t.descent), num(t.ascent), ref(file)))

	var w strings.Builder
	for _, g := range gids {
		fmt.Fprintf(&w, "%d [%s] ", g, num(t.advance(uint16(g))))
	}
	cid := p.add(fmt.Sprintf("<< /Type /Font /Subtype /CIDFontType2 /BaseFont /%s /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> /FontDescriptor %s /W [%s] /CIDToGIDMap /Identity >>",
		base, ref(desc), strings.TrimSpace(w.String())))

	toUnicode := p.addStream("", t.toUnicode(gids), true)
	return p.add(fmt.Sprintf("<< /Type /Font /Subtype /Type0 /BaseFont /%s /Encoding /Identity-H /DescendantFonts [%s] /ToUnicode %s >>",
		base, ref(cid), ref(toUnicode))), nil
}

func (t *trueType) toUnicode(gids []int) []byte {
	var b strings.Builder
	b.WriteString("/CIDInit /ProcSet findresource begin\n12 dict begin\nbegincmap\n" +
		"/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def\n" +
		"/CMapName /Adobe-Identity-UCS def\n/CMapType 2 def\n" +
		"1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n")
	for len(gids) > 0 {
		chunk := gids[:min(len(gids), 100)]
		gids = gids[len(chunk):]
		fmt.Fprintf(&b, "%d beginbfchar\n", len(chunk))
		for _, g := range chunk {
			fmt.Fprintf(&b, "<%04X> <", g)
			for _, u := range utf16.Encode([]rune{t.used[uint16(g)]}) {
				fmt.Fprintf(&b, "%04X", u)
			}
			b.WriteString(">\n")
		}
		b.WriteString("endbfchar\n")
	}
	b.WriteString("endcmap\nCMapName currentdict /CMap defineresource pop\nend\nend\n")
	return []byte(b.String())
}
