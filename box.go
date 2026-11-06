package htmlpdf

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

type boxKind uint8

const (
	blockBox boxKind = iota
	inlineBox
	textBox
	imageBox
	breakBox
	inlineBlockBox
	tableBox
	rowGroupBox
	rowBox
	cellBox
)

type box struct {
	kind     boxKind
	st       *style
	children []*box
	inline   bool // block container whose children are inline-level

	text  string
	img   *pdfImage
	href  string
	bg    color // text: background of the enclosing inline element
	index int   // list item number
	span  int   // cell colspan

	header  bool // row group repeated on every page
	footer  bool
	caption []*box
}

func (b *box) isInline() bool {
	switch b.kind {
	case inlineBox, textBox, breakBox, inlineBlockBox:
		return true
	case imageBox:
		return b.st.display != "block"
	}
	return false
}

type buildContext struct {
	href string
	bg   color
}

func (r *renderer) build(n *html.Node, parent *style) ([]*box, error) {
	return r.buildNode(n, parent, buildContext{})
}

func (r *renderer) buildNode(n *html.Node, parent *style, ctx buildContext) ([]*box, error) {
	if n.Type == html.TextNode {
		return []*box{{kind: textBox, st: parent, text: n.Data, href: ctx.href, bg: ctx.bg}}, nil
	}
	if n.Type != html.ElementNode {
		return nil, nil
	}
	st := r.computeStyle(n, parent)
	if st.display == "none" {
		return nil, nil
	}

	switch n.Data {
	case "img":
		src := attr(n, "src")
		if src == "" {
			return nil, nil
		}
		img, err := r.image(src)
		if err != nil {
			return nil, err
		}
		return []*box{{kind: imageBox, st: st, img: img, href: ctx.href}}, nil
	case "br":
		return []*box{{kind: breakBox, st: st}}, nil
	case "a":
		if h := attr(n, "href"); h != "" && !strings.HasPrefix(h, "#") {
			ctx.href = h
		}
	}

	if st.display == "inline" {
		if st.bg.ok {
			ctx.bg = st.bg
		}
	} else {
		ctx.bg = color{}
	}
	var kids []*box
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b, err := r.buildNode(c, st, ctx)
		if err != nil {
			return nil, err
		}
		kids = append(kids, b...)
	}

	b := &box{st: st}
	switch st.display {
	case "inline":
		b.kind = inlineBox
		for _, k := range kids {
			if !k.isInline() {
				k = anonymousInlineBlock(k)
			}
			b.children = append(b.children, k)
		}
	case "inline-block":
		b.kind = inlineBlockBox
		b.setChildren(kids)
	case "table", "inline-table":
		b.kind = tableBox
		b.setTableChildren(kids)
	case "table-row-group", "table-header-group", "table-footer-group":
		b.kind = rowGroupBox
		b.header = st.display == "table-header-group"
		b.footer = st.display == "table-footer-group"
		for _, k := range kids {
			if k.kind == rowBox {
				b.children = append(b.children, k)
			}
		}
	case "table-row":
		b.kind = rowBox
		for _, k := range kids {
			if k.kind == cellBox {
				b.children = append(b.children, k)
			}
		}
	case "table-cell":
		b.kind = cellBox
		b.span = 1
		if s, err := strconv.Atoi(attr(n, "colspan")); err == nil && s > 1 {
			b.span = min(s, 1000)
		}
		b.setChildren(kids)
	default:
		b.kind = blockBox
		b.setChildren(kids)
	}

	if n.Data == "ol" || n.Data == "ul" {
		i := 1
		if s, err := strconv.Atoi(attr(n, "start")); err == nil {
			i = s
		}
		for _, k := range b.children {
			if k.st.display == "list-item" {
				k.index = i
				i++
			}
		}
	}
	return []*box{b}, nil
}

func anonymousInlineBlock(k *box) *box {
	b := &box{kind: inlineBlockBox, st: k.st.inherit()}
	b.st.display = "inline-block"
	b.setChildren([]*box{k})
	return b
}

// setChildren makes b a block container, wrapping runs of inline-level
// children in anonymous blocks when they are mixed with block-level ones.
func (b *box) setChildren(kids []*box) {
	mixed := false
	for _, k := range kids {
		if !k.isInline() {
			mixed = true
			break
		}
	}
	if !mixed {
		if !blank(kids) {
			b.inline = true
			b.children = kids
		}
		return
	}
	var run []*box
	flush := func() {
		if !blank(run) {
			anon := &box{kind: blockBox, st: b.st.inherit(), inline: true, children: run}
			anon.st.display = "block"
			b.children = append(b.children, anon)
		}
		run = nil
	}
	for _, k := range kids {
		if k.isInline() {
			run = append(run, k)
			continue
		}
		flush()
		b.children = append(b.children, k)
	}
	flush()
}

func (b *box) setTableChildren(kids []*box) {
	var rows []*box
	flush := func() {
		if len(rows) > 0 {
			g := &box{kind: rowGroupBox, st: b.st.inherit(), children: rows}
			b.children = append(b.children, g)
			rows = nil
		}
	}
	for _, k := range kids {
		switch {
		case k.kind == rowBox:
			rows = append(rows, k)
		case k.kind == rowGroupBox:
			flush()
			b.children = append(b.children, k)
		case k.st.display == "table-caption":
			b.caption = append(b.caption, k)
		}
	}
	flush()
}

// blank reports whether boxes hold nothing but collapsible white space.
func blank(boxes []*box) bool {
	for _, k := range boxes {
		if k.kind != textBox || strings.HasPrefix(k.st.whiteSpace, "pre") || strings.TrimSpace(k.text) != "" {
			return false
		}
	}
	return true
}
