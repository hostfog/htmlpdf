package htmlpdf

import (
	"math"
	"testing"
)

func layoutOf(t *testing.T, src string) *layout {
	t.Helper()
	r, err := newRenderer(&Options{PageSize: PageSize{400, 400}, Margins: Margins{10, 10, 10, 10}})
	if err != nil {
		t.Fatal(err)
	}
	l, err := r.layout(src)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func texts(l *layout) []item {
	var out []item
	for _, it := range l.items {
		if it.kind == textItem {
			out = append(out, it)
		}
	}
	return out
}

func TestStandardWidths(t *testing.T) {
	fs, _ := newFontSet(nil)
	tests := []struct {
		family string
		bold   bool
		text   string
		want   float64
	}{
		{"Helvetica", false, "Hello", 22.78},
		{"Arial", true, "Hello", 24.45},
		{"Times", false, "Hello", 22.22},
		{"monospace", false, "Hello", 30},
		{"Helvetica", false, "£€", 11.12},
		{"Helvetica", false, "ਸ", 5.56},
	}
	for _, tt := range tests {
		f := fs.face([]string{tt.family}, tt.bold, false)
		if got := f.width(tt.text, 10); math.Abs(got-tt.want) > 1e-9 {
			t.Errorf("%s %q = %v, want %v", tt.family, tt.text, got, tt.want)
		}
	}
	if f := fs.face([]string{"Nope", "Courier New"}, true, true); f.base != "Courier-BoldOblique" {
		t.Errorf("fallback family = %s", f.base)
	}
}

func TestWrapping(t *testing.T) {
	l := layoutOf(t, `<body style="margin: 0"><p style="width: 60pt; margin: 0">aaa bbb ccc ddd eee</p></body>`)
	lines := texts(l)
	if len(lines) != 3 {
		t.Fatalf("%d lines: %+v", len(lines), lines)
	}
	for _, ln := range lines {
		if ln.w > 60 {
			t.Errorf("line %q is %v wide", ln.text, ln.w)
		}
	}
	if lines[0].text != "aaa bbb " && lines[0].text != "aaa bbb" {
		t.Errorf("first line = %q", lines[0].text)
	}
}

func TestWhiteSpace(t *testing.T) {
	l := layoutOf(t, `<p>  a   <b> b </b>  c  </p><pre>x  y
z</pre><p style="white-space: nowrap; width: 10pt">no wrap here</p>`)
	got := texts(l)
	want := []string{"a ", "b ", "c", "x  y", "z", "no wrap here"}
	if len(got) != len(want) {
		t.Fatalf("got %d runs: %+v", len(got), got)
	}
	for i := range want {
		if got[i].text != want[i] {
			t.Errorf("run %d = %q, want %q", i, got[i].text, want[i])
		}
	}
}

func TestMarginCollapse(t *testing.T) {
	l := layoutOf(t, `<body style="margin: 0; line-height: 20pt; font-size: 10pt">
		<p style="margin: 0 0 20pt">a</p><p style="margin: 30pt 0 0">b</p><p style="margin: -10pt 0 0">c</p></body>`)
	ts := texts(l)
	if len(ts) != 3 {
		t.Fatalf("%d runs", len(ts))
	}
	if d := ts[1].y - ts[0].y; !near(d, 50) {
		t.Errorf("gap a-b = %v, want 50", d)
	}
	if d := ts[2].y - ts[1].y; !near(d, 10) {
		t.Errorf("gap b-c = %v, want 10", d)
	}
}

func TestAlignment(t *testing.T) {
	l := layoutOf(t, `<body style="margin: 0">
		<p style="text-align: right; width: 100pt">x</p>
		<p style="text-align: center; width: 100pt">x</p>
		<div style="width: 100pt; margin: 0 auto">x</div>
		<p style="text-align: justify; width: 100pt">aa bb cc dd ee ff gg hh ii jj kk ll</p></body>`)
	ts := texts(l)
	xw := ts[0].w
	if !near(ts[0].x, 100-xw) || !near(ts[1].x, (100-xw)/2) || !near(ts[2].x, 140) {
		t.Errorf("x = %v %v %v", ts[0].x, ts[1].x, ts[2].x)
	}
	var first float64
	for _, it := range ts[3:] {
		if it.y == ts[3].y {
			first = it.x + it.w
		}
	}
	if !near(first, 100) {
		t.Errorf("justified line ends at %v, want 100", first)
	}
}

func TestBorderBox(t *testing.T) {
	l := layoutOf(t, `<body style="margin: 0">
		<div id="a" style="width: 100pt; padding: 10pt; border: 5pt solid">x</div>
		<div style="width: 100pt; padding: 10pt; border: 5pt solid; box-sizing: border-box">x</div>
		<div style="width: 50%; max-width: 120pt">x</div></body>`)
	var widths []float64
	for _, it := range l.items {
		if it.kind == boxItem && it.w < 380 {
			widths = append(widths, it.w)
		}
	}
	if len(widths) != 3 || widths[0] != 130 || widths[1] != 100 || widths[2] != 120 {
		t.Errorf("widths = %v", widths)
	}
}

func TestTableLayout(t *testing.T) {
	l := layoutOf(t, `<body style="margin: 0"><table style="border-collapse: collapse; width: 300pt">
		<tr><td style="width: 50%">a</td><td>b</td><td>c</td></tr>
		<tr><td colspan="2">wide</td><td style="vertical-align: bottom">x<br>y</td></tr>
		</table></body>`)
	var cells []item
	for _, it := range l.items {
		if it.kind == boxItem && it.centered {
			cells = append(cells, it)
		}
	}
	if len(cells) != 6 {
		t.Fatalf("%d boxes", len(cells))
	}
	table, a, b, c, wide := cells[0], cells[1], cells[2], cells[3], cells[4]
	if table.w != 300 || a.w != 150 || !near(b.w+c.w, 150) || !near(wide.w, a.w+b.w) {
		t.Errorf("widths: table %v a %v b %v c %v wide %v", table.w, a.w, b.w, c.w, wide.w)
	}
	if wide.h != cells[5].h || wide.h <= a.h {
		t.Errorf("row heights: %v %v", wide.h, cells[5].h)
	}
}

func TestDistribute(t *testing.T) {
	tests := []struct {
		mins, maxs, pcts []float64
		avail            float64
		fixed            bool
		want             []float64
	}{
		{[]float64{10, 10}, []float64{30, 50}, []float64{0, 0}, 200, false, []float64{30, 50}},
		{[]float64{10, 10}, []float64{30, 50}, []float64{0, 0}, 160, true, []float64{60, 100}},
		{[]float64{10, 10}, []float64{30, 50}, []float64{0, 0}, 50, false, []float64{20, 30}},
		{[]float64{10, 10}, []float64{30, 50}, []float64{0, 0}, 5, false, []float64{10, 10}},
		{[]float64{10, 10}, []float64{30, 50}, []float64{25, 0}, 200, false, []float64{50, 150}},
	}
	for i, tt := range tests {
		got := distribute(tt.mins, tt.maxs, tt.pcts, tt.avail, tt.fixed)
		for j := range got {
			if !near(got[j], tt.want[j]) {
				t.Errorf("%d: got %v, want %v", i, got, tt.want)
				break
			}
		}
	}
}

func TestListMarker(t *testing.T) {
	tests := []struct {
		style string
		n     int
		want  string
	}{
		{"decimal", 7, "7"},
		{"lower-alpha", 1, "a"},
		{"upper-alpha", 28, "AB"},
		{"lower-roman", 4, "iv"},
		{"upper-roman", 1994, "MCMXCIV"},
		{"upper-roman", 0, "0"},
	}
	for _, tt := range tests {
		if got := listMarker(tt.style, tt.n); got != tt.want {
			t.Errorf("%s %d = %q, want %q", tt.style, tt.n, got, tt.want)
		}
	}
}

func TestKeepTogether(t *testing.T) {
	l := layoutOf(t, `<body style="margin: 0; font-size: 10pt; line-height: 20pt">
		<div style="height: 330pt"></div>
		<div style="page-break-inside: avoid"><p style="margin: 0">a</p><p style="margin: 0">b</p><p style="margin: 0">c</p></div></body>`)
	ts := texts(l)
	if ts[0].y != 380 {
		t.Errorf("block starts at %v, want 380 (top of page 2)", ts[0].y)
	}
}
