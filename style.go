package htmlpdf

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

const pxToPt = 0.75

type color struct {
	r, g, b uint8
	ok      bool // false means transparent
}

type unit uint8

const (
	pt unit = iota
	pct
	auto
)

type length struct {
	v    float64
	unit unit
}

var autoLength = length{unit: auto}

func (l length) resolve(base float64) float64 {
	switch l.unit {
	case pt:
		return l.v
	case pct:
		return l.v * base / 100
	}
	return 0
}

type border struct {
	width float64
	style string
	color color // !ok means currentColor
}

func (b border) visible() bool {
	return b.width > 0 && b.style != "" && b.style != "none" && b.style != "hidden"
}

// Sides are ordered top, right, bottom, left.
const (
	top = iota
	right
	bottom
	left
)

type style struct {
	// Inherited.
	color      color
	families   []string
	size       float64
	bold       bool
	italic     bool
	align      string
	underline  bool
	lineNum    float64 // line-height as a multiple of size; 0 if lineAbs is used
	lineAbs    float64
	whiteSpace string
	listStyle  string
	collapse   bool
	spacing    float64
	transform  string

	// Not inherited.
	display     string
	bg          color
	margin      [4]length
	padding     [4]length
	border      [4]border
	radius      float64
	width       length
	height      length
	minWidth    length
	maxWidth    length
	borderBox   bool
	breakBefore string
	breakAfter  string
	breakInside string
	valign      string
}

func (s *style) inherit() *style {
	return &style{
		color:      s.color,
		families:   s.families,
		size:       s.size,
		bold:       s.bold,
		italic:     s.italic,
		align:      s.align,
		underline:  s.underline,
		lineNum:    s.lineNum,
		lineAbs:    s.lineAbs,
		whiteSpace: s.whiteSpace,
		listStyle:  s.listStyle,
		collapse:   s.collapse,
		spacing:    s.spacing,
		transform:  s.transform,
		display:    "inline",
		valign:     "baseline",
		width:      autoLength,
		height:     autoLength,
		minWidth:   autoLength,
		maxWidth:   autoLength,
	}
}

func (s *style) lineHeight() float64 {
	if s.lineAbs > 0 {
		return s.lineAbs
	}
	return s.lineNum * s.size
}

func (s *style) borderWidth(side int) float64 {
	if !s.border[side].visible() {
		return 0
	}
	return s.border[side].width
}

const userAgentCSS = `
html, body, div, p, h1, h2, h3, h4, h5, h6, ul, ol, dl, dt, dd, li, header, footer,
section, article, nav, main, aside, address, blockquote, pre, hr, form, figure,
figcaption, fieldset, center { display: block }
head, script, style, title, meta, link, template, noscript { display: none }
li { display: list-item }
table { display: table; border-spacing: 2px; border-collapse: separate }
thead { display: table-header-group }
tbody { display: table-row-group }
tfoot { display: table-footer-group }
tr { display: table-row }
td, th { display: table-cell; padding: 1px; vertical-align: middle }
th { font-weight: bold; text-align: center }
caption { display: table-caption; text-align: center }
body { margin: 8px }
p, dl, blockquote, figure, pre { margin: 1em 0 }
blockquote, figure { margin-left: 40px; margin-right: 40px }
dd { margin-left: 40px }
h1 { font-size: 2em; margin: .67em 0 }
h2 { font-size: 1.5em; margin: .83em 0 }
h3 { font-size: 1.17em; margin: 1em 0 }
h4 { margin: 1.33em 0 }
h5 { font-size: .83em; margin: 1.67em 0 }
h6 { font-size: .67em; margin: 2.33em 0 }
h1, h2, h3, h4, h5, h6, b, strong, dt { font-weight: bold }
i, em, cite, var, dfn, address { font-style: italic }
u, ins { text-decoration: underline }
a { color: #0000ee; text-decoration: underline }
ul, ol { margin: 1em 0; padding-left: 40px }
ul ul, ul ol, ol ul, ol ol { margin-top: 0; margin-bottom: 0 }
ul { list-style-type: disc }
ol { list-style-type: decimal }
ul ul { list-style-type: circle }
ul ul ul { list-style-type: square }
pre, code, kbd, samp, tt { font-family: monospace }
pre { white-space: pre }
small { font-size: smaller }
big { font-size: larger }
hr { border-top: 1px solid #a0a0a0; margin: .5em 0 }
center { text-align: center }
`

var uaRules = parseStylesheet(userAgentCSS)

type matched struct {
	decl      declaration
	important bool
	author    bool
	spec      int
	order     int
}

func (r *renderer) computeStyle(n *html.Node, parent *style) *style {
	var ms []matched
	order := 0
	collect := func(rules []rule, author bool) {
		for _, ru := range rules {
			if !ru.sel.match(n) {
				continue
			}
			spec := ru.sel.specificity()
			for _, d := range ru.decls {
				ms = append(ms, matched{d, d.important, author, spec, order})
				order++
			}
		}
	}
	collect(uaRules, false)
	collect(r.rules, true)
	if s := attr(n, "style"); s != "" {
		for _, d := range parseDeclarations(s) {
			ms = append(ms, matched{d, d.important, true, 1 << 30, order})
			order++
		}
	}
	sort.SliceStable(ms, func(i, j int) bool {
		a, b := ms[i], ms[j]
		if a.important != b.important {
			return !a.important
		}
		if a.author != b.author {
			return a.author == a.important
		}
		return a.spec < b.spec
	})

	st := parent.inherit()
	if n.Data == "html" {
		st.display = "block"
	}
	r.presentational(n, st)

	// font-size first: em units in other properties depend on it.
	for i := len(ms) - 1; i >= 0; i-- {
		if p := ms[i].decl.prop; p == "font-size" || p == "font" {
			r.apply(st, parent, ms[i].decl.prop, ms[i].decl.value)
			break
		}
	}
	for _, m := range ms {
		if m.decl.prop != "font-size" {
			r.apply(st, parent, m.decl.prop, m.decl.value)
		}
	}
	for i := range st.border {
		if !st.border[i].color.ok {
			st.border[i].color = st.color
		}
	}
	if n.Data == "html" {
		r.rootSize = st.size
	}
	return st
}

// presentational maps legacy HTML attributes that invoices commonly use.
func (r *renderer) presentational(n *html.Node, st *style) {
	if a := attr(n, "align"); a != "" && n.Data != "img" && n.Data != "table" {
		st.align = strings.ToLower(a)
	}
	if a := attr(n, "bgcolor"); a != "" {
		if c, ok := parseColor(a); ok {
			st.bg = c
		}
	}
	if a := attr(n, "width"); a != "" {
		r.apply(st, st, "width", htmlLength(a))
	}
	if a := attr(n, "height"); a != "" {
		r.apply(st, st, "height", htmlLength(a))
	}
	if a := attr(n, "valign"); a != "" {
		st.valign = strings.ToLower(a)
	}
	switch n.Data {
	case "table":
		if a := attr(n, "cellspacing"); a != "" {
			r.apply(st, st, "border-spacing", htmlLength(a))
		}
		if a := attr(n, "border"); a != "" && a != "0" {
			r.apply(st, st, "border", htmlLength(a)+" solid gray")
		}
		if strings.EqualFold(attr(n, "align"), "center") {
			st.margin[left], st.margin[right] = autoLength, autoLength
		}
	case "td", "th":
		t := parentElement(n)
		for t != nil && t.Data != "table" {
			t = parentElement(t)
		}
		if t == nil {
			break
		}
		if a := attr(t, "border"); a != "" && a != "0" {
			r.apply(st, st, "border", "1px solid gray")
		}
		if a := attr(t, "cellpadding"); a != "" {
			r.apply(st, st, "padding", htmlLength(a))
		}
	}
}

func htmlLength(s string) string {
	s = strings.TrimSpace(s)
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return s + "px"
	}
	return s
}

func (r *renderer) apply(st, parent *style, prop, v string) {
	lv := strings.ToLower(v)
	if lv == "inherit" {
		r.inheritProp(st, parent, prop)
		return
	}
	switch prop {
	case "color":
		if c, ok := parseColor(lv); ok {
			st.color = c
		}
	case "background-color":
		if c, ok := parseColor(lv); ok {
			st.bg = c
		}
	case "background":
		for _, f := range fields(lv) {
			if c, ok := parseColor(f); ok {
				st.bg = c
			}
		}
	case "font-family":
		st.families = parseFamilies(v)
	case "font-size":
		if s, ok := r.fontSize(lv, parent.size); ok {
			st.size = s
		}
	case "font-weight":
		switch lv {
		case "bold", "bolder":
			st.bold = true
		case "normal", "lighter":
			st.bold = false
		default:
			if w, err := strconv.Atoi(lv); err == nil {
				st.bold = w >= 600
			}
		}
	case "font-style":
		st.italic = lv == "italic" || lv == "oblique"
	case "font":
		r.applyFont(st, parent, v)
	case "text-align":
		st.align = lv
	case "text-decoration", "text-decoration-line":
		st.underline = strings.Contains(lv, "underline")
	case "text-transform":
		st.transform = lv
	case "line-height":
		r.applyLineHeight(st, lv)
	case "white-space":
		st.whiteSpace = lv
	case "list-style-type", "list-style":
		for _, f := range fields(lv) {
			if listStyles[f] {
				st.listStyle = f
			}
		}
	case "display":
		st.display = lv
	case "margin", "padding":
		vals := fields(lv)
		if len(vals) == 0 || len(vals) > 4 {
			return
		}
		var out [4]length
		for i := range out {
			l, ok := r.length(vals[sideIndex(len(vals), i)], st.size)
			if !ok {
				return
			}
			out[i] = l
		}
		if prop == "margin" {
			st.margin = out
		} else {
			st.padding = out
		}
	case "margin-top", "margin-right", "margin-bottom", "margin-left":
		if l, ok := r.length(lv, st.size); ok {
			st.margin[sideNames[prop[7:]]] = l
		}
	case "padding-top", "padding-right", "padding-bottom", "padding-left":
		if l, ok := r.length(lv, st.size); ok {
			st.padding[sideNames[prop[8:]]] = l
		}
	case "border":
		b := r.parseBorder(lv, st.size)
		st.border = [4]border{b, b, b, b}
	case "border-top", "border-right", "border-bottom", "border-left":
		st.border[sideNames[prop[7:]]] = r.parseBorder(lv, st.size)
	case "border-width", "border-style", "border-color":
		vals := fields(lv)
		if len(vals) == 0 || len(vals) > 4 {
			return
		}
		for i := range st.border {
			r.setBorderPart(&st.border[i], prop[7:], vals[sideIndex(len(vals), i)], st.size)
		}
	case "border-radius":
		if f := fields(lv); len(f) > 0 {
			if l, ok := r.length(f[0], st.size); ok {
				st.radius = l.resolve(0)
			}
		}
	case "border-collapse":
		st.collapse = lv == "collapse"
	case "border-spacing":
		if f := fields(lv); len(f) > 0 {
			if l, ok := r.length(f[0], st.size); ok {
				st.spacing = l.resolve(0)
			}
		}
	case "width", "height", "min-width", "max-width":
		l, ok := r.length(lv, st.size)
		if !ok {
			if lv != "none" {
				return
			}
			l = autoLength
		}
		switch prop {
		case "width":
			st.width = l
		case "height":
			st.height = l
		case "min-width":
			st.minWidth = l
		case "max-width":
			st.maxWidth = l
		}
	case "box-sizing":
		st.borderBox = lv == "border-box"
	case "page-break-before", "break-before":
		st.breakBefore = breakValue(lv)
	case "page-break-after", "break-after":
		st.breakAfter = breakValue(lv)
	case "page-break-inside", "break-inside":
		st.breakInside = breakValue(lv)
	case "vertical-align":
		st.valign = lv
	default:
		if strings.HasPrefix(prop, "border-") {
			parts := strings.SplitN(prop[7:], "-", 2)
			if side, ok := sideNames[parts[0]]; ok && len(parts) == 2 {
				r.setBorderPart(&st.border[side], parts[1], lv, st.size)
			}
		}
	}
}

func (r *renderer) inheritProp(st, parent *style, prop string) {
	switch prop {
	case "color":
		st.color = parent.color
	case "background-color", "background":
		st.bg = parent.bg
	case "font-family":
		st.families = parent.families
	case "font-size":
		st.size = parent.size
	case "font-weight":
		st.bold = parent.bold
	case "font-style":
		st.italic = parent.italic
	case "text-align":
		st.align = parent.align
	case "width":
		st.width = parent.width
	case "height":
		st.height = parent.height
	}
}

func breakValue(v string) string {
	switch v {
	case "always", "page", "left", "right":
		return "page"
	case "avoid", "avoid-page":
		return "avoid"
	}
	return ""
}

var listStyles = map[string]bool{
	"disc": true, "circle": true, "square": true, "decimal": true, "none": true,
	"lower-alpha": true, "upper-alpha": true, "lower-latin": true, "upper-latin": true,
	"lower-roman": true, "upper-roman": true,
}

var sideNames = map[string]int{"top": top, "right": right, "bottom": bottom, "left": left}

// sideIndex maps side i to an index into a 1-4 value shorthand.
func sideIndex(n, i int) int {
	switch n {
	case 1:
		return 0
	case 2:
		return i % 2
	case 3:
		if i == left {
			return 1
		}
		return i
	}
	return i
}

func (r *renderer) parseBorder(v string, size float64) border {
	b := border{width: 3 * pxToPt}
	for _, f := range fields(v) {
		switch {
		case borderStyles[f]:
			b.style = f
		case f == "thin" || f == "medium" || f == "thick":
			r.setBorderPart(&b, "width", f, size)
		default:
			if c, ok := parseColor(f); ok {
				b.color = c
			} else if l, ok := r.length(f, size); ok {
				b.width = l.resolve(0)
			}
		}
	}
	if v == "none" || v == "0" {
		b.style = "none"
	}
	return b
}

var borderStyles = map[string]bool{
	"none": true, "hidden": true, "solid": true, "dashed": true, "dotted": true,
	"double": true, "groove": true, "ridge": true, "inset": true, "outset": true,
}

func (r *renderer) setBorderPart(b *border, part, v string, size float64) {
	switch part {
	case "width":
		switch v {
		case "thin":
			b.width = 1 * pxToPt
		case "medium":
			b.width = 3 * pxToPt
		case "thick":
			b.width = 5 * pxToPt
		default:
			if l, ok := r.length(v, size); ok {
				b.width = l.resolve(0)
			}
		}
	case "style":
		if borderStyles[v] {
			b.style = v
		}
	case "color":
		if c, ok := parseColor(v); ok {
			b.color = c
		}
	}
}

func (r *renderer) applyFont(st, parent *style, v string) {
	parts := fields(v)
	for i, f := range parts {
		lf := strings.ToLower(f)
		switch {
		case lf == "bold" || lf == "bolder" || lf == "600" || lf == "700" || lf == "800" || lf == "900":
			st.bold = true
		case lf == "italic" || lf == "oblique":
			st.italic = true
		case lf == "normal":
		default:
			size, lh, _ := strings.Cut(lf, "/")
			if s, ok := r.fontSize(size, parent.size); ok {
				st.size = s
				if lh != "" {
					r.applyLineHeight(st, lh)
				}
				if rest := strings.Join(parts[i+1:], " "); rest != "" {
					st.families = parseFamilies(rest)
				}
				return
			}
		}
	}
}

func (r *renderer) applyLineHeight(st *style, v string) {
	if v == "normal" {
		st.lineNum, st.lineAbs = 1.15, 0
		return
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		st.lineNum, st.lineAbs = f, 0
		return
	}
	if l, ok := r.length(v, st.size); ok {
		st.lineNum, st.lineAbs = 0, l.resolve(st.size)
	}
}

var fontKeywords = map[string]float64{
	"xx-small": 9, "x-small": 10, "small": 13, "medium": 16,
	"large": 18, "x-large": 24, "xx-large": 32,
}

func (r *renderer) fontSize(v string, parent float64) (float64, bool) {
	if px, ok := fontKeywords[v]; ok {
		return px * pxToPt, true
	}
	switch v {
	case "smaller":
		return parent / 1.2, true
	case "larger":
		return parent * 1.2, true
	}
	l, ok := r.length(v, parent)
	if !ok || l.unit == auto {
		return 0, false
	}
	return l.resolve(parent), true
}

// length parses a CSS length into points. em is relative to size.
func (r *renderer) length(v string, size float64) (length, bool) {
	if v == "auto" {
		return autoLength, true
	}
	if v == "0" {
		return length{0, pt}, true
	}
	i := len(v)
	for i > 0 && (v[i-1] < '0' || v[i-1] > '9') && v[i-1] != '.' {
		i--
	}
	f, err := strconv.ParseFloat(v[:i], 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return length{}, false
	}
	switch strings.ToLower(v[i:]) {
	case "px":
		return length{f * pxToPt, pt}, true
	case "pt":
		return length{f, pt}, true
	case "em":
		return length{f * size, pt}, true
	case "rem":
		return length{f * r.rootSize, pt}, true
	case "%":
		return length{f, pct}, true
	case "mm":
		return length{f * 72 / 25.4, pt}, true
	case "cm":
		return length{f * 72 / 2.54, pt}, true
	case "in":
		return length{f * 72, pt}, true
	case "pc":
		return length{f * 12, pt}, true
	case "ex":
		return length{f * size / 2, pt}, true
	}
	return length{}, false
}

// fields splits a value on whitespace outside parentheses.
func fields(v string) []string {
	var out []string
	depth, start := 0, -1
	for i := 0; i <= len(v); i++ {
		if i == len(v) || depth == 0 && (v[i] == ' ' || v[i] == '\t' || v[i] == '\n') {
			if start >= 0 {
				out = append(out, v[start:i])
				start = -1
			}
			continue
		}
		switch v[i] {
		case '(':
			depth++
		case ')':
			depth--
		}
		if start < 0 {
			start = i
		}
	}
	return out
}

func parseFamilies(v string) []string {
	var out []string
	for _, f := range splitTop(v, ',') {
		f = strings.Trim(strings.TrimSpace(f), `"'`)
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

var namedColors = map[string]uint32{
	"black": 0x000000, "white": 0xffffff, "red": 0xff0000, "green": 0x008000,
	"blue": 0x0000ff, "yellow": 0xffff00, "orange": 0xffa500, "purple": 0x800080,
	"gray": 0x808080, "grey": 0x808080, "silver": 0xc0c0c0, "maroon": 0x800000,
	"navy": 0x000080, "teal": 0x008080, "olive": 0x808000, "lime": 0x00ff00,
	"aqua": 0x00ffff, "cyan": 0x00ffff, "fuchsia": 0xff00ff, "magenta": 0xff00ff,
	"darkgray": 0xa9a9a9, "darkgrey": 0xa9a9a9, "lightgray": 0xd3d3d3,
	"lightgrey": 0xd3d3d3, "dimgray": 0x696969, "gainsboro": 0xdcdcdc,
	"whitesmoke": 0xf5f5f5, "darkblue": 0x00008b, "darkgreen": 0x006400,
	"darkred": 0x8b0000, "steelblue": 0x4682b4, "royalblue": 0x4169e1,
	"slategray": 0x708090, "crimson": 0xdc143c, "gold": 0xffd700,
	"lightblue": 0xadd8e6, "lightyellow": 0xffffe0, "ivory": 0xfffff0,
	"beige": 0xf5f5dc, "brown": 0xa52a2a, "pink": 0xffc0cb, "tomato": 0xff6347,
}

func parseColor(v string) (color, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "transparent" {
		return color{}, true
	}
	if rgb, ok := namedColors[v]; ok {
		return color{uint8(rgb >> 16), uint8(rgb >> 8), uint8(rgb), true}, true
	}
	if strings.HasPrefix(v, "#") {
		h := v[1:]
		if len(h) == 3 || len(h) == 4 {
			h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
		}
		if len(h) == 8 {
			if h[6:] == "00" {
				return color{}, true
			}
			h = h[:6]
		}
		x, err := strconv.ParseUint(h, 16, 32)
		if err != nil || len(h) != 6 {
			return color{}, false
		}
		return color{uint8(x >> 16), uint8(x >> 8), uint8(x), true}, true
	}
	if open := strings.IndexByte(v, '('); open > 0 && strings.HasSuffix(v, ")") {
		fn := v[:open]
		if fn != "rgb" && fn != "rgba" {
			return color{}, false
		}
		args := strings.FieldsFunc(v[open+1:len(v)-1], func(r rune) bool { return r == ',' || r == ' ' || r == '/' })
		if len(args) < 3 {
			return color{}, false
		}
		var c [3]uint8
		for i := range c {
			a := args[i]
			f, err := strconv.ParseFloat(strings.TrimSuffix(a, "%"), 64)
			if err != nil {
				return color{}, false
			}
			if strings.HasSuffix(a, "%") {
				f = f * 255 / 100
			}
			c[i] = uint8(math.Round(math.Max(0, math.Min(255, f))))
		}
		if len(args) > 3 && (args[3] == "0" || args[3] == "0%") {
			return color{}, true
		}
		return color{c[0], c[1], c[2], true}, true
	}
	return color{}, false
}
