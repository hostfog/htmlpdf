package htmlpdf

import (
	"math"
	"strconv"
	"strings"
	"unicode"
)

type pieceKind uint8

const (
	wordPiece pieceKind = iota
	spacePiece
	atomicPiece
	breakPiece
)

// piece is the smallest unit of inline layout: a word or part of one, a
// space, an image or inline-block, or a forced line break.
type piece struct {
	kind pieceKind
	brk  bool // a line may break before this piece
	st   *style
	text string
	face *face
	w    float64
	min  float64 // atomic min-content width, when measuring
	href string
	bg   color

	box    *box
	h      float64 // atomic margin-box height
	dx, dy float64 // atomic margin offsets
	iw, ih float64 // image size
	items  []item  // inline-block content laid out at the origin
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f'
}

func collapsible(st *style) bool {
	return st.whiteSpace != "pre" && st.whiteSpace != "pre-wrap"
}

func wraps(st *style) bool {
	return st.whiteSpace != "nowrap" && st.whiteSpace != "pre"
}

func (l *layout) pieces(boxes []*box, avail float64, measure bool, out []piece) []piece {
	for _, b := range boxes {
		switch b.kind {
		case textBox:
			out = l.textPieces(b, out)
		case inlineBox:
			out = l.pieces(b.children, avail, measure, out)
		case breakBox:
			out = append(out, piece{kind: breakPiece, st: b.st})
		case imageBox, inlineBlockBox:
			out = append(out, l.atomic(b, avail, measure))
		}
	}
	return out
}

func setBreaks(ps []piece) {
	for i := 1; i < len(ps); i++ {
		prev, p := &ps[i-1], &ps[i]
		switch {
		case p.kind == spacePiece || p.kind == breakPiece:
		case prev.kind == spacePiece:
			p.brk = wraps(prev.st)
		case prev.kind == atomicPiece || p.kind == atomicPiece:
			p.brk = wraps(p.st)
		}
	}
}

func (l *layout) textPieces(b *box, out []piece) []piece {
	st := b.st
	text := transform(b.text, st.transform)
	primary := l.fonts.face(st.families, st.bold, st.italic)

	space := func(s string) {
		if collapsible(st) {
			if len(out) == 0 || out[len(out)-1].kind == spacePiece || out[len(out)-1].kind == breakPiece {
				return
			}
			s = " "
		}
		out = append(out, piece{kind: spacePiece, st: st, text: s, face: primary, w: primary.width(s, st.size), href: b.href, bg: b.bg})
	}
	word := func(s string) {
		start := 0
		var cur *face
		emit := func(end int) {
			out = append(out, piece{kind: wordPiece, st: st, text: s[start:end], face: cur, w: cur.width(s[start:end], st.size), href: b.href, bg: b.bg})
			start = end
		}
		for i, r := range s {
			f := primary
			if !primary.has(r) {
				if fb := l.fonts.fallback(r, st.bold, st.italic); fb != nil {
					f = fb
				}
			}
			if f != cur && i > start {
				emit(i)
			}
			cur = f
		}
		if start < len(s) {
			emit(len(s))
		}
	}
	tokens := func(line string, sep func(rune) bool) {
		for line != "" {
			i := strings.IndexFunc(line, func(r rune) bool { return !sep(r) })
			if i != 0 {
				if i < 0 {
					i = len(line)
				}
				space(line[:i])
				line = line[i:]
				continue
			}
			j := strings.IndexFunc(line, sep)
			if j < 0 {
				j = len(line)
			}
			word(line[:j])
			line = line[j:]
		}
	}

	if collapsible(st) {
		tokens(text, isSpace)
		return out
	}
	for i, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if i > 0 {
			out = append(out, piece{kind: breakPiece, st: st})
		}
		line = expandTabs(line)
		if st.whiteSpace == "pre" {
			if line != "" {
				word(line)
			}
			continue
		}
		tokens(line, func(r rune) bool { return r == ' ' })
	}
	return out
}

func expandTabs(s string) string {
	if !strings.Contains(s, "\t") {
		return s
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			n := 8 - col%8
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		col++
	}
	return b.String()
}

func transform(s, t string) string {
	switch t {
	case "uppercase":
		return strings.ToUpper(s)
	case "lowercase":
		return strings.ToLower(s)
	case "capitalize":
		prev := ' '
		return strings.Map(func(r rune) rune {
			if isSpace(prev) {
				r = unicode.ToTitle(r)
			}
			prev = r
			return r
		}, s)
	}
	return s
}

func (l *layout) atomic(b *box, avail float64, measure bool) piece {
	st := b.st
	p := piece{kind: atomicPiece, st: st, box: b, href: b.href}
	ml, mr := st.margin[left].resolve(avail), st.margin[right].resolve(avail)
	mt, mb := st.margin[top].resolve(avail), st.margin[bottom].resolve(avail)
	p.dx, p.dy = ml, mt

	if measure {
		mn, mx := l.intrinsic(b)
		p.min, p.w = mn, mx
		return p
	}
	if b.kind == imageBox {
		p.iw, p.ih = l.imageSize(b, avail)
		p.w, p.h = p.iw+ml+mr, p.ih+mt+mb
		return p
	}

	var w float64
	if st.width.unit != auto {
		w, _ = l.blockWidth(b, avail)
	} else {
		mn, mx := l.intrinsic(b)
		w = math.Min(math.Max(mn, avail), mx) - ml - mr
	}
	n, m := len(l.items), l.marker
	l.marker = nil
	h, _ := l.boxAt(b, 0, 0, w, false)
	p.items = append([]item(nil), l.items[n:]...)
	l.items, l.marker = l.items[:n], m
	p.w, p.h = w+ml+mr, h+mt+mb
	return p
}

func breakLines(ps []piece, w float64) [][]piece {
	var lines [][]piece
	for start := 0; start < len(ps); {
		for start < len(ps) && ps[start].kind == spacePiece && collapsible(ps[start].st) {
			start++
		}
		if start == len(ps) {
			break
		}
		end, brk, width := start, -1, 0.0
		for end < len(ps) {
			p := ps[end]
			if p.kind == breakPiece {
				end++
				break
			}
			if p.brk && end > start {
				brk = end
			}
			if p.kind != spacePiece && width+p.w > w+eps && brk > start {
				end = brk
				break
			}
			width += p.w
			end++
		}
		lines = append(lines, ps[start:end])
		start = end
	}
	return lines
}

func (l *layout) inlineIntrinsic(boxes []*box) (float64, float64) {
	ps := l.pieces(boxes, 0, true, nil)
	setBreaks(ps)
	var mn, mx, unit, line float64
	for _, p := range ps {
		switch {
		case p.kind == breakPiece:
			mx, line, unit = math.Max(mx, line), 0, 0
			continue
		case p.brk, p.kind == spacePiece:
			unit = 0
		}
		if p.kind == atomicPiece {
			unit += p.min
		} else if p.kind != spacePiece {
			unit += p.w
		}
		mn = math.Max(mn, unit)
		line += p.w
	}
	return mn, math.Max(mx, line)
}

// inline lays out inline-level content in lines and returns the height used.
func (l *layout) inline(b *box, x, y, w float64) float64 {
	ps := l.pieces(b.children, w, false, nil)
	setBreaks(ps)
	lines := breakLines(ps, w)
	y0 := y
	for i, ln := range lines {
		last := i == len(lines)-1 || ln[len(ln)-1].kind == breakPiece
		y = l.line(ln, b.st, x, y, w, last)
	}
	return y - y0
}

// metrics returns the space a run of st in f needs above and below the
// baseline, with half the leading on each side.
func metrics(f *face, st *style) (float64, float64) {
	a, d := f.ascent()*st.size/1000, f.descent()*st.size/1000
	half := (st.lineHeight() - a - d) / 2
	return a + half, d + half
}

func (l *layout) line(ps []piece, st *style, x, y, w float64, last bool) float64 {
	for len(ps) > 0 {
		p := ps[len(ps)-1]
		if p.kind != breakPiece && (p.kind != spacePiece || !collapsible(p.st)) {
			break
		}
		ps = ps[:len(ps)-1]
	}

	above, below := metrics(l.fonts.face(st.families, st.bold, st.italic), st)
	var width, tall float64
	spaces := 0
	for _, p := range ps {
		width += p.w
		switch p.kind {
		case atomicPiece:
			switch p.box.st.valign {
			case "top", "middle", "bottom", "text-top", "text-bottom":
				tall = math.Max(tall, p.h)
			default:
				above = math.Max(above, p.h)
			}
		case spacePiece:
			spaces++
			fallthrough
		default:
			a, d := metrics(p.face, p.st)
			above, below = math.Max(above, a), math.Max(below, d)
		}
	}
	h := above + below
	if tall > h {
		below += tall - h
		h = tall
	}
	y = l.fit(y, h)
	base := y + above

	free := w - width
	var off, gap float64
	switch st.align {
	case "right", "end":
		off = free
	case "center":
		off = free / 2
	case "justify":
		if !last && spaces > 0 && free > 0 {
			gap = free / float64(spaces)
		}
	}
	cx := x + math.Max(off, 0)

	if l.marker != nil {
		l.drawMarker(y, base, h)
		l.marker = nil
	}

	for i := 0; i < len(ps); {
		p := ps[i]
		if p.kind == atomicPiece {
			ty := base - p.h
			switch p.box.st.valign {
			case "top", "text-top":
				ty = y
			case "bottom", "text-bottom":
				ty = y + h - p.h
			case "middle":
				ty = y + (h-p.h)/2
			}
			if p.box.kind == imageBox {
				l.image(p.box, cx+p.dx, ty+p.dy, p.iw, p.ih)
			} else {
				n := len(l.items)
				l.items = append(l.items, p.items...)
				l.translate(n, len(l.items), cx+p.dx, ty+p.dy)
			}
			cx += p.w
			i++
			continue
		}

		j, runW := i, 0.0
		var text strings.Builder
		for j < len(ps) && sameRun(&p, &ps[j]) && (gap == 0 || j == i) {
			text.WriteString(ps[j].text)
			runW += ps[j].w
			j++
		}
		if p.kind == spacePiece {
			runW += gap
		}
		l.run(&p, text.String(), cx, y, base, runW, h)
		cx += runW
		i = j
	}
	return y + h
}

func sameRun(a, b *piece) bool {
	return b.kind != atomicPiece && b.kind != breakPiece && a.face == b.face && a.st.size == b.st.size &&
		a.st.color == b.st.color && a.st.underline == b.st.underline && a.href == b.href && a.bg == b.bg
}

func (l *layout) run(p *piece, text string, x, y, base, w, lineH float64) {
	size := p.st.size
	if p.bg.ok {
		a, d := p.face.ascent()*size/1000, p.face.descent()*size/1000
		l.push(item{kind: boxItem, x: x, y: base - a, w: w, h: a + d, bg: p.bg})
	}
	if strings.TrimSpace(text) != "" {
		l.push(item{kind: textItem, x: x, y: y, w: w, h: lineH, base: base - y, face: p.face, size: size, text: text, color: p.st.color})
	}
	if p.st.underline {
		l.push(item{kind: boxItem, x: x, y: base + size*0.08, w: w, h: math.Max(size*0.05, 0.5), bg: p.st.color})
	}
	if p.href != "" {
		l.push(item{kind: linkItem, x: x, y: y, w: w, h: lineH, href: p.href})
	}
}

func (l *layout) drawMarker(y, base, h float64) {
	st := l.marker.st
	size, x := st.size, l.markerX
	switch st.listStyle {
	case "disc", "circle", "square":
		d := size * 0.32
		it := item{kind: boxItem, x: x - size*0.55 - d, y: base - size*0.27 - d/2, w: d, h: d}
		switch st.listStyle {
		case "disc":
			it.bg, it.radius = st.color, d/2
		case "circle":
			b := border{width: math.Max(size*0.06, 0.5), style: "solid", color: st.color}
			it.border, it.radius = [4]border{b, b, b, b}, d/2
		default:
			it.bg = st.color
		}
		l.push(it)
	default:
		f := l.fonts.face(st.families, st.bold, st.italic)
		text := listMarker(st.listStyle, l.marker.index) + "."
		tw := f.width(text, size)
		l.push(item{kind: textItem, x: x - tw - size*0.35, y: y, w: tw, h: h, base: base - y, face: f, size: size, text: text, color: st.color})
	}
}

func listMarker(style string, n int) string {
	switch style {
	case "lower-alpha", "lower-latin", "upper-alpha", "upper-latin":
		if n < 1 {
			break
		}
		var s []byte
		for ; n > 0; n = (n - 1) / 26 {
			s = append([]byte{byte('a' + (n-1)%26)}, s...)
		}
		if strings.HasPrefix(style, "upper") {
			return strings.ToUpper(string(s))
		}
		return string(s)
	case "lower-roman", "upper-roman":
		if n < 1 || n > 3999 {
			break
		}
		var b strings.Builder
		for _, r := range []struct {
			v int
			s string
		}{{1000, "m"}, {900, "cm"}, {500, "d"}, {400, "cd"}, {100, "c"}, {90, "xc"}, {50, "l"}, {40, "xl"}, {10, "x"}, {9, "ix"}, {5, "v"}, {4, "iv"}, {1, "i"}} {
			for ; n >= r.v; n -= r.v {
				b.WriteString(r.s)
			}
		}
		if style == "upper-roman" {
			return strings.ToUpper(b.String())
		}
		return b.String()
	}
	return strconv.Itoa(n)
}
