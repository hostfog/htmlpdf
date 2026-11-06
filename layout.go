package htmlpdf

import "math"

const eps = 0.01

type itemKind uint8

const (
	boxItem itemKind = iota // background and borders
	textItem
	imageItem
	linkItem
)

// item is one thing to paint. Coordinates are in points from the top left of
// the content area of the first page; pages follow each other vertically.
type item struct {
	kind       itemKind
	x, y, w, h float64

	bg       color
	border   [4]border
	radius   float64
	centered bool // borders straddle the edges, as in collapsed tables

	base  float64 // text baseline, relative to y
	face  *face
	size  float64
	text  string
	color color

	img  *pdfImage
	href string
}

type layout struct {
	fonts   *fontSet
	pageH   float64
	items   []item
	marker  *box // list item whose marker goes on the next line
	markerX float64
}

func (l *layout) push(it item) int {
	l.items = append(l.items, it)
	return len(l.items) - 1
}

func (l *layout) nextPage(y float64) float64 {
	return math.Floor((y+eps)/l.pageH+1) * l.pageH
}

// page returns the index of the page that y falls on.
func (l *layout) page(y float64) int {
	p := math.Floor((y + eps) / l.pageH)
	if !(p < maxPages) {
		return maxPages
	}
	return int(math.Max(p, 0))
}

func (l *layout) atPageTop(y float64) bool {
	off := math.Mod(y, l.pageH)
	return off < eps || l.pageH-off < eps
}

// crosses reports whether [y, y+h) spans a page break and would fit on a
// fresh page.
func (l *layout) crosses(y, h float64) bool {
	return h > eps && h <= l.pageH && l.page(y) != l.page(y+h-2*eps)
}

// fit returns where something of height h starting at y must go to avoid
// a page break.
func (l *layout) fit(y, h float64) float64 {
	if l.crosses(y, h) {
		return l.nextPage(y)
	}
	return y
}

// keep runs fn at y and, if the result spans a page break, runs it again at
// the top of the next page.
func (l *layout) keep(y float64, fn func(y float64) float64) (float64, float64) {
	n, m := len(l.items), l.marker
	h := fn(y)
	if !l.crosses(y, h) {
		return y, h
	}
	l.items, l.marker = l.items[:n], m
	y = l.nextPage(y)
	return y, fn(y)
}

func (l *layout) translate(from, to int, dx, dy float64) {
	for i := from; i < to; i++ {
		l.items[i].x += dx
		l.items[i].y += dy
	}
}

func padding(st *style, cb float64) [4]float64 {
	var p [4]float64
	for i := range p {
		p[i] = st.padding[i].resolve(cb)
	}
	return p
}

func borders(st *style) [4]float64 {
	return [4]float64{st.borderWidth(top), st.borderWidth(right), st.borderWidth(bottom), st.borderWidth(left)}
}

// blocks lays out block-level boxes from top to bottom, collapsing adjacent
// vertical margins, and returns the height used.
func (l *layout) blocks(kids []*box, x, y, w float64) float64 {
	cur, pending := y, 0.0
	breakNext := false
	for _, k := range kids {
		st := k.st
		if (breakNext || st.breakBefore == "page") && cur > 0 && !l.atPageTop(cur) {
			cur, pending = l.nextPage(cur), 0
		}
		breakNext = st.breakAfter == "page"

		mt := st.margin[top].resolve(w)
		at := cur + collapse(pending, mt)
		if cur > 0 && l.atPageTop(cur) && pending == 0 {
			at = cur
		}
		at, h := l.place(k, x, at, w)
		cur, pending = at+h, st.margin[bottom].resolve(w)
	}
	return cur + pending - y
}

func collapse(a, b float64) float64 {
	switch {
	case a >= 0 && b >= 0:
		return math.Max(a, b)
	case a < 0 && b < 0:
		return math.Min(a, b)
	}
	return a + b
}

// place lays out one block-level box with its border edge at y and returns
// the final top, which moves when the box is kept on one page.
func (l *layout) place(b *box, x, y, cb float64) (float64, float64) {
	switch b.kind {
	case imageBox:
		w, h := l.imageSize(b, cb)
		ml := b.st.margin[left].resolve(cb)
		if b.st.margin[left].unit == auto && b.st.margin[right].unit == auto {
			ml = (cb - w) / 2
		}
		y = l.fit(y, h)
		l.image(b, x+ml, y, w, h)
		return y, h
	}
	fn := func(y float64) float64 { return l.table(b, x, y, cb) }
	if b.kind != tableBox {
		w, ml := l.blockWidth(b, cb)
		fn = func(y float64) float64 {
			h, _ := l.boxAt(b, x+ml, y, w, false)
			return h
		}
	}
	if b.st.breakInside == "avoid" {
		return l.keep(y, fn)
	}
	return y, fn(y)
}

// blockWidth returns the border-box width and left margin of a block in a
// containing block of width cb.
func (l *layout) blockWidth(b *box, cb float64) (w, ml float64) {
	st := b.st
	pad, bw := padding(st, cb), borders(st)
	edges := pad[left] + pad[right] + bw[left] + bw[right]
	ml, mr := st.margin[left].resolve(cb), st.margin[right].resolve(cb)

	size := func(l length) float64 {
		v := l.resolve(cb)
		if !st.borderBox {
			v += edges
		}
		return v
	}
	fixed := st.width.unit != auto
	if fixed {
		w = size(st.width)
	} else {
		w = cb - ml - mr
	}
	if st.maxWidth.unit != auto && w > size(st.maxWidth) {
		w, fixed = size(st.maxWidth), true
	}
	if st.minWidth.unit != auto && w < size(st.minWidth) {
		w = size(st.minWidth)
	}
	w = math.Max(w, edges)
	if fixed {
		switch {
		case st.margin[left].unit == auto && st.margin[right].unit == auto:
			ml = (cb - w) / 2
		case st.margin[left].unit == auto:
			ml = cb - w - mr
		}
	}
	return w, ml
}

// boxAt lays out a block container with border-box width w at (x, y). It
// returns the border-box height and the index of its decoration item.
func (l *layout) boxAt(b *box, x, y, w float64, collapsed bool) (float64, int) {
	st := b.st
	pad, bw := padding(st, w), borders(st)
	if collapsed {
		for i := range bw {
			bw[i] /= 2
		}
	}
	idx := l.push(item{kind: boxItem, x: x, y: y, w: w, bg: st.bg, border: st.border, radius: st.radius, centered: collapsed})

	cx, cy := x+bw[left]+pad[left], y+bw[top]+pad[top]
	cw := math.Max(0, w-bw[left]-pad[left]-bw[right]-pad[right])

	prev := l.marker
	if st.display == "list-item" && st.listStyle != "none" {
		l.marker, l.markerX = b, cx
	}
	var ch float64
	if b.inline {
		ch = l.inline(b, cx, cy, cw)
	} else {
		ch = l.blocks(b.children, cx, cy, cw)
	}
	if l.marker == b {
		l.marker = prev
	}

	vert := bw[top] + pad[top] + bw[bottom] + pad[bottom]
	if st.height.unit == pt {
		h := st.height.v
		if st.borderBox {
			h -= vert
		}
		ch = math.Max(ch, h)
	}
	h := ch + vert
	l.items[idx].h = h
	return h, idx
}

func (l *layout) imageSize(b *box, cb float64) (float64, float64) {
	st := b.st
	iw, ih := float64(b.img.w)*pxToPt, float64(b.img.h)*pxToPt
	w, h := iw, ih
	switch {
	case st.width.unit != auto && st.height.unit == pt:
		w, h = st.width.resolve(cb), st.height.v
	case st.width.unit != auto:
		w = st.width.resolve(cb)
		h = w * ih / math.Max(iw, eps)
	case st.height.unit == pt:
		h = st.height.v
		w = h * iw / math.Max(ih, eps)
	}
	if st.maxWidth.unit == pt || st.maxWidth.unit == pct && cb > 0 {
		if mw := st.maxWidth.resolve(cb); w > mw {
			if st.height.unit != pt {
				h *= mw / w
			}
			w = mw
		}
	}
	return w, h
}

func (l *layout) image(b *box, x, y, w, h float64) {
	l.push(item{kind: imageItem, x: x, y: y, w: w, h: h, img: b.img})
	if b.href != "" {
		l.push(item{kind: linkItem, x: x, y: y, w: w, h: h, href: b.href})
	}
}

// intrinsic returns the min-content and max-content widths of b including
// its padding, borders and fixed margins.
func (l *layout) intrinsic(b *box) (float64, float64) {
	st := b.st
	pad, bw := padding(st, 0), borders(st)
	edges := pad[left] + pad[right] + bw[left] + bw[right]
	margins := st.margin[left].resolve(0) + st.margin[right].resolve(0)

	if st.width.unit == pt && b.kind != tableBox {
		w := st.width.v
		if !st.borderBox {
			w += edges
		}
		return w + margins, w + margins
	}
	var mn, mx float64
	switch {
	case b.kind == imageBox:
		w, _ := l.imageSize(b, 0)
		if st.width.unit == pct || st.maxWidth.unit == pct {
			return margins, w + margins
		}
		return w + margins, w + margins
	case b.kind == tableBox:
		mn, mx = l.tableIntrinsic(b)
		return mn + margins, mx + margins
	case b.inline:
		mn, mx = l.inlineIntrinsic(b.children)
	default:
		for _, k := range b.children {
			kmn, kmx := l.intrinsic(k)
			mn, mx = math.Max(mn, kmn), math.Max(mx, kmx)
		}
	}
	if st.maxWidth.unit == pt {
		limit := st.maxWidth.v
		if st.borderBox {
			limit -= edges
		}
		mx = math.Min(mx, limit)
		mn = math.Min(mn, mx)
	}
	return mn + edges + margins, mx + edges + margins
}
