package htmlpdf

import (
	"math"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func parseDoc(t *testing.T, src string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func byID(n *html.Node, id string) *html.Node {
	if n.Type == html.ElementNode && attr(n, "id") == id {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := byID(c, id); f != nil {
			return f
		}
	}
	return nil
}

func TestSelectors(t *testing.T) {
	doc := parseDoc(t, `<div id="main" class="a b"><p id="one" class="x">1</p><p id="two">2</p><span id="s"><em id="e">e</em></span></div>`)
	tests := []struct {
		sel, id string
		want    bool
	}{
		{"p", "one", true},
		{"P", "one", true},
		{"*", "one", true},
		{".a.b p", "one", true},
		{".a.c p", "one", false},
		{"#main > p", "two", true},
		{"div em", "e", true},
		{"div > em", "e", false},
		{"div>span>em", "e", true},
		{"p:first-child", "one", true},
		{"p:first-child", "two", false},
		{"span:last-child", "s", true},
		{"p.x", "one", true},
		{"p.x", "two", false},
		{"html body #main #e", "e", true},
		{":root", "main", false},
	}
	for _, tt := range tests {
		sel, ok := parseSelector(tt.sel)
		if !ok {
			t.Errorf("%q: not parsed", tt.sel)
			continue
		}
		if got := sel.match(byID(doc, tt.id)); got != tt.want {
			t.Errorf("%q on #%s = %v, want %v", tt.sel, tt.id, got, tt.want)
		}
	}
	for _, bad := range []string{"p + p", "p ~ p", "a[href]", "p::before", "a:hover", "> p", "p >", ""} {
		if _, ok := parseSelector(bad); ok {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestSpecificity(t *testing.T) {
	tests := map[string]int{
		"p":              1,
		".a":             1 << 8,
		"#a":             1 << 16,
		"div p.x.y":      2<<8 | 2,
		"#a .b > p":      1<<16 | 1<<8 | 1,
		"li:first-child": 1<<8 | 1,
	}
	for s, want := range tests {
		sel, _ := parseSelector(s)
		if got := sel.specificity(); got != want {
			t.Errorf("%q: %#x, want %#x", s, got, want)
		}
	}
}

func TestParseStylesheet(t *testing.T) {
	rules := parseStylesheet(`
		@charset "utf-8";
		@import url(x.css);
		/* comment { } */
		h1, .t { color: red; margin: 0 !important }
		@media screen { p { color: blue } }
		@media print { p { color: green } }
		@font-face { font-family: X; src: url(x.ttf) }
		a:hover, b { font-weight: bold }
	`)
	var got []string
	for _, r := range rules {
		for _, d := range r.decls {
			got = append(got, d.prop+"="+d.value)
		}
	}
	want := "color=red margin=0 color=red margin=0 color=green font-weight=bold"
	if strings.Join(got, " ") != want {
		t.Errorf("got %v", got)
	}
	if !rules[0].decls[1].important || rules[0].decls[0].important {
		t.Error("!important not parsed")
	}
}

// styleOf computes the style of the element with the given id.
func styleOf(t *testing.T, src, id string) *style {
	t.Helper()
	r, err := newRenderer(nil)
	if err != nil {
		t.Fatal(err)
	}
	doc := parseDoc(t, src)
	if err := r.collect(doc); err != nil {
		t.Fatal(err)
	}
	target := byID(doc, id)
	var path []*html.Node
	for n := target; n != nil && n.Type == html.ElementNode; n = n.Parent {
		path = append([]*html.Node{n}, path...)
	}
	st := &style{color: color{ok: true}, families: []string{"Helvetica"}, size: 12, lineNum: 1.15}
	for _, n := range path {
		st = r.computeStyle(n, st)
	}
	return st
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestCascade(t *testing.T) {
	src := `<style>
		p { color: red }
		#x { color: blue }
		.c { color: green !important }
		#y { color: blue }
		div { font-size: 20px; font-family: "Times New Roman", serif }
		div p { font-size: 1.5em; margin: 1em 2px 3px }
		html { font-size: 10px }
		.r { font-size: 2rem; border: 2px dashed; border-left: 1px solid #00f; color: #123 }
	</style>
	<div><p id="x" class="c" style="color: yellow">a</p><p id="y" style="color: #fff">b</p><h1 id="h">h</h1><p id="z" class="r">r</p></div>`

	if c := styleOf(t, src, "x").color; c != (color{0, 128, 0, true}) {
		t.Errorf("important: color = %v", c)
	}
	if c := styleOf(t, src, "y").color; c != (color{255, 255, 255, true}) {
		t.Errorf("inline: color = %v", c)
	}

	y := styleOf(t, src, "y")
	if !near(y.size, 22.5) {
		t.Errorf("em font-size = %v, want 22.5", y.size)
	}
	if got := [4]float64{y.margin[0].v, y.margin[1].v, y.margin[2].v, y.margin[3].v}; got != [4]float64{22.5, 1.5, 2.25, 1.5} {
		t.Errorf("margins = %v", got)
	}
	if y.families[0] != "Times New Roman" || y.families[1] != "serif" {
		t.Errorf("families = %q", y.families)
	}

	h := styleOf(t, src, "h")
	if !h.bold || !near(h.size, 30) {
		t.Errorf("h1: bold %v size %v", h.bold, h.size)
	}

	z := styleOf(t, src, "z")
	if !near(z.size, 15) {
		t.Errorf("rem font-size = %v, want 15", z.size)
	}
	if b := z.border[top]; b.style != "dashed" || !near(b.width, 1.5) || b.color != (color{0x11, 0x22, 0x33, true}) {
		t.Errorf("top border = %+v", b)
	}
	if b := z.border[left]; b.style != "solid" || !near(b.width, 0.75) || b.color != (color{0, 0, 255, true}) {
		t.Errorf("left border = %+v", b)
	}
}

func TestParseColor(t *testing.T) {
	tests := []struct {
		in   string
		want color
		ok   bool
	}{
		{"#fff", color{255, 255, 255, true}, true},
		{"#1E40AF", color{0x1e, 0x40, 0xaf, true}, true},
		{"#1e40af80", color{0x1e, 0x40, 0xaf, true}, true},
		{"rgb(10, 20, 30)", color{10, 20, 30, true}, true},
		{"rgba(10 20 30 / 0)", color{}, true},
		{"rgb(100%, 0%, 50%)", color{255, 0, 128, true}, true},
		{"Navy", color{0, 0, 128, true}, true},
		{"transparent", color{}, true},
		{"#ggg", color{}, false},
		{"hsl(0, 0%, 0%)", color{}, false},
		{"bogus", color{}, false},
	}
	for _, tt := range tests {
		got, ok := parseColor(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("%q = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestLength(t *testing.T) {
	r := &renderer{rootSize: 10}
	tests := []struct {
		in   string
		want length
	}{
		{"16px", length{12, pt}},
		{"12pt", length{12, pt}},
		{"2em", length{20, pt}},
		{"1.5rem", length{15, pt}},
		{"50%", length{50, pct}},
		{"25.4mm", length{72, pt}},
		{"1in", length{72, pt}},
		{"-4px", length{-3, pt}},
		{"0", length{0, pt}},
		{"auto", autoLength},
	}
	for _, tt := range tests {
		got, ok := r.length(tt.in, 10)
		if !ok || !near(got.v, tt.want.v) || got.unit != tt.want.unit {
			t.Errorf("%q = %v, %v; want %v", tt.in, got, ok, tt.want)
		}
	}
	for _, bad := range []string{"px", "12qq", "NaNpx", "abc"} {
		if _, ok := r.length(bad, 10); ok {
			t.Errorf("%q should not parse", bad)
		}
	}
}
