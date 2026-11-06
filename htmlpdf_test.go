package htmlpdf

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"image"
	ic "image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/image/font/gofont/goregular"
)

var update = flag.Bool("update", false, "rewrite testdata/invoice.pdf")

func render(t *testing.T, src string, opts *Options) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Render(&buf, src, opts); err != nil {
		t.Fatal(err)
	}
	checkStructure(t, buf.Bytes())
	return buf.Bytes()
}

// checkStructure verifies the header, trailer and that every xref entry
// points at its object.
func checkStructure(t *testing.T, pdf []byte) {
	t.Helper()
	if !bytes.HasPrefix(pdf, []byte("%PDF-1.7\n")) || !bytes.HasSuffix(pdf, []byte("%%EOF\n")) {
		t.Fatal("missing PDF header or EOF marker")
	}
	m := regexp.MustCompile(`startxref\n(\d+)\n`).FindSubmatch(pdf)
	if m == nil {
		t.Fatal("no startxref")
	}
	off, _ := strconv.Atoi(string(m[1]))
	if !bytes.HasPrefix(pdf[off:], []byte("xref\n0 ")) {
		t.Fatalf("startxref %d does not point at xref", off)
	}
	entries := regexp.MustCompile(`(\d{10}) 00000 n `).FindAllSubmatch(pdf[off:], -1)
	if len(entries) == 0 {
		t.Fatal("empty xref")
	}
	for i, e := range entries {
		o, _ := strconv.Atoi(string(e[1]))
		if want := fmt.Sprintf("%d 0 obj\n", i+1); !bytes.HasPrefix(pdf[o:], []byte(want)) {
			t.Fatalf("xref entry %d points at %q", i+1, pdf[o:o+10])
		}
	}
}

// contents returns every Flate stream in pdf, inflated and concatenated.
func contents(t *testing.T, pdf []byte) string {
	t.Helper()
	var out strings.Builder
	re := regexp.MustCompile(`/FlateDecode /Length (\d+) >>\nstream\n`)
	for _, loc := range re.FindAllSubmatchIndex(pdf, -1) {
		n, _ := strconv.Atoi(string(pdf[loc[2]:loc[3]]))
		zr, err := zlib.NewReader(bytes.NewReader(pdf[loc[1] : loc[1]+n]))
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(zr)
		if err != nil {
			t.Fatal(err)
		}
		out.Write(b)
	}
	return out.String()
}

func pages(pdf []byte) int {
	return bytes.Count(pdf, []byte("/Type /Page /Parent"))
}

// pdftotext returns the text of each page, skipping the test when poppler is
// not installed.
func pdftotext(t *testing.T, pdf []byte) []string {
	t.Helper()
	bin, err := exec.LookPath("pdftotext")
	if err != nil {
		t.Skip("pdftotext not installed")
	}
	cmd := exec.Command(bin, "-layout", "-", "-")
	cmd.Stdin = bytes.NewReader(pdf)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(out), "\f"), "\f")
}

func TestInvoice(t *testing.T) {
	src, err := os.ReadFile("testdata/invoice.html")
	if err != nil {
		t.Fatal(err)
	}
	pdf := render(t, string(src), &Options{BaseDir: "testdata"})
	if *update {
		if err := os.WriteFile("testdata/invoice.pdf", pdf, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if n := pages(pdf); n != 1 {
		t.Errorf("pages = %d, want 1", n)
	}
	for _, want := range []string{"/Title (Invoice INV-2026-0142)", "/URI (mailto:billing@hostfog.example)", "/SMask"} {
		if !bytes.Contains(pdf, []byte(want)) {
			t.Errorf("missing %s", want)
		}
	}

	text := pdftotext(t, pdf)[0]
	for _, want := range []string{"INVOICE", "INV-2026-0142", "Acme Widgets Ltd", "£186.36", "€14.99", "Managed VPS — Standard"} {
		if !strings.Contains(text, want) {
			t.Errorf("text is missing %q", want)
		}
	}
}

func TestDeterministic(t *testing.T) {
	src, _ := os.ReadFile("testdata/invoice.html")
	opts := &Options{BaseDir: "testdata", Fonts: []Font{{Family: "Go", Data: goregular.TTF}}}
	a := render(t, string(src), opts)
	b := render(t, string(src), opts)
	if !bytes.Equal(a, b) {
		t.Fatal("output differs between runs")
	}
}

func TestTableHeaderRepeats(t *testing.T) {
	var rows strings.Builder
	for i := 1; i <= 120; i++ {
		fmt.Fprintf(&rows, "<tr><td>Row %d</td><td>%d.00</td></tr>", i, i)
	}
	src := `<table border="1"><thead><tr><th>Item</th><th>Price</th></tr></thead><tbody>` + rows.String() + `</tbody></table>`
	pdf := render(t, src, nil)

	n := pages(pdf)
	if n < 3 {
		t.Fatalf("pages = %d, want at least 3", n)
	}
	if got := strings.Count(contents(t, pdf), "(Item)"); got != n {
		t.Errorf("header drawn %d times on %d pages", got, n)
	}

	text := pdftotext(t, pdf)
	seen := 0
	for i, p := range text {
		if !strings.Contains(p, "Item") {
			t.Errorf("page %d has no header", i+1)
		}
		seen += strings.Count(p, "Row ")
	}
	if seen != 120 {
		t.Errorf("found %d rows, want 120", seen)
	}
}

func TestPageBreaks(t *testing.T) {
	src := `<p>one</p><p style="page-break-before: always">two</p><div style="break-after: page">three</div><p>four</p>`
	pdf := render(t, src, nil)
	if n := pages(pdf); n != 3 {
		t.Fatalf("pages = %d, want 3", n)
	}
	text := pdftotext(t, pdf)
	for i, want := range []string{"one", "two\n\nthree", "four"} {
		if strings.TrimSpace(text[i]) != want {
			t.Errorf("page %d = %q, want %q", i+1, strings.TrimSpace(text[i]), want)
		}
	}
}

func TestLongParagraphSplits(t *testing.T) {
	src := `<p>` + strings.Repeat("lorem ipsum dolor sit amet ", 2000) + `</p>`
	pdf := render(t, src, &Options{PageSize: Letter})
	if n := pages(pdf); n < 4 {
		t.Fatalf("pages = %d, want at least 4", n)
	}
	if !bytes.Contains(pdf, []byte("/MediaBox [0 0 612 792]")) {
		t.Error("Letter page size not used")
	}
}

func TestTrueTypeFont(t *testing.T) {
	src := `<p style="font-family: 'Go Sans'">Price £5 · €7 · Ωmega · Привет</p>`
	pdf := render(t, src, &Options{Fonts: []Font{{Family: "Go Sans", Data: goregular.TTF}}})
	for _, want := range []string{"/CIDFontType2", "/Identity-H", "/FontFile2", "/ToUnicode", "+GoRegular"} {
		if !bytes.Contains(pdf, []byte(want)) {
			t.Errorf("missing %s", want)
		}
	}
	if len(pdf) > len(goregular.TTF)/2 {
		t.Errorf("PDF is %d bytes, font was not subset", len(pdf))
	}
	if text := pdftotext(t, pdf)[0]; !strings.Contains(text, "Price £5 · €7 · Ωmega · Привет") {
		t.Errorf("text = %q", text)
	}
}

func TestFallbackFont(t *testing.T) {
	pdf := render(t, `<p>Helvetica with Ωmega</p>`, &Options{Fonts: []Font{{Family: "Go", Data: goregular.TTF}}})
	c := contents(t, pdf)
	if !strings.Contains(c, "/F1 ") || !strings.Contains(c, "/F2 ") {
		t.Errorf("expected two fonts in content: %s", c)
	}
	if text := pdftotext(t, pdf)[0]; !strings.Contains(text, "Helvetica with Ωmega") {
		t.Errorf("text = %q", text)
	}
}

func TestGurmukhi(t *testing.T) {
	font, err := os.ReadFile("/usr/share/fonts/truetype/freefont/FreeSerif.ttf")
	if err != nil {
		t.Skip("FreeSerif not installed")
	}
	pdf := render(t, `<p style="font-family: Free">ਸਤ ਸ੍ਰੀ ਅਕਾਲ</p>`, &Options{Fonts: []Font{{Family: "Free", Data: font}}})
	if text := pdftotext(t, pdf)[0]; !strings.Contains(text, "ਸਤ") {
		t.Errorf("text = %q", text)
	}
}

func TestSubsetIsValidFont(t *testing.T) {
	tt, err := parseTrueType(goregular.TTF)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range "Hello £€" {
		tt.use(r)
	}
	data, err := tt.subset()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) >= len(goregular.TTF) {
		t.Errorf("subset is %d bytes, original %d", len(data), len(goregular.TTF))
	}
	tables, err := readTables(data)
	if err != nil {
		t.Fatal(err)
	}
	sub := &trueType{tables: tables}
	kept := 0
	for g := 0; g < tt.numGlyphs; g++ {
		start, end := sub.glyphRange(g)
		if end > start {
			kept++
		} else if _, used := tt.used[uint16(g)]; used && g != int(tt.glyph(' ')) {
			t.Errorf("used glyph %d is empty", g)
		}
	}
	if kept > 12 {
		t.Errorf("kept %d glyphs", kept)
	}
	if checksum(data) != 0xB1B0AFBA {
		t.Errorf("file checksum = %#x", checksum(data))
	}
}

func dataURL(mime string, b []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)
}

func TestImages(t *testing.T) {
	m := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	for i := range m.Pix {
		m.Pix[i] = 0x80
	}
	var pngBuf, jpgBuf bytes.Buffer
	png.Encode(&pngBuf, m)
	g := image.NewGray(image.Rect(0, 0, 8, 8))
	g.Set(1, 1, ic.White)
	jpeg.Encode(&jpgBuf, g, nil)

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.jpg"), jpgBuf.Bytes(), 0o644)

	src := fmt.Sprintf(`<img src="%s" width="40"><img src="a.jpg" style="width: 20px; height: 10px"><img src="a.jpg">`, dataURL("image/png", pngBuf.Bytes()))
	pdf := render(t, src, &Options{BaseDir: dir})
	for _, want := range []string{"/Filter /DCTDecode", "/ColorSpace /DeviceGray", "/SMask", "/Width 4 /Height 2"} {
		if !bytes.Contains(pdf, []byte(want)) {
			t.Errorf("missing %s", want)
		}
	}
	if n := bytes.Count(pdf, []byte("/Subtype /Image")); n != 3 {
		t.Errorf("%d image objects, want 3 (PNG, its mask and one shared JPEG)", n)
	}
	c := contents(t, pdf)
	if !strings.Contains(c, "q 30 0 0 15 ") || !strings.Contains(c, "q 15 0 0 7.5 ") {
		t.Errorf("unexpected image sizes in %s", c)
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		opts *Options
		want string
	}{
		{"missing image", `<img src="nope.png">`, &Options{BaseDir: t.TempDir()}, "nope.png"},
		{"no base dir", `<img src="logo.png">`, nil, "BaseDir"},
		{"remote", `<link rel="stylesheet" href="https://example.com/a.css">`, nil, "remote"},
		{"escape base dir", `<img src="../../../etc/passwd">`, &Options{BaseDir: "testdata"}, "passwd"},
		{"bad image", `<img src="data:image/png;base64,AAAA">`, nil, "image"},
		{"bad font", `x`, &Options{Fonts: []Font{{Family: "X", Data: []byte("nope")}}}, `font "X"`},
		{"margins", `x`, &Options{Margins: Margins{Left: 400, Right: 400}}, "margins"},
		{"too long", `<div style="height: 100000000px"></div>`, nil, "pages"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Render(io.Discard, tt.src, tt.opts)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestWriteError(t *testing.T) {
	if err := Render(failWriter{}, "<p>hi</p>", nil); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v", err)
	}
}

func TestLinksAndLists(t *testing.T) {
	src := `<p><a href="https://hostfog.com/a?b=(1)">site</a></p>
		<ol start="9"><li>nine</li><li>ten</li></ol>
		<ol style="list-style-type: lower-alpha"><li>x</li></ol>`
	pdf := render(t, src, nil)
	if !bytes.Contains(pdf, []byte(`/URI (https://hostfog.com/a?b=\(1\))`)) {
		t.Error("link annotation missing")
	}
	text := pdftotext(t, pdf)[0]
	for _, want := range []string{"9. nine", "10. ten", "a. x"} {
		if !strings.Contains(text, want) {
			t.Errorf("text is missing %q:\n%s", want, text)
		}
	}
}

func TestPdfReaders(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil || exec.Command(py, "-c", "import pypdf").Run() != nil {
		t.Skip("pypdf not installed")
	}
	src, _ := os.ReadFile("testdata/invoice.html")
	pdf := render(t, string(src), &Options{BaseDir: "testdata"})
	path := filepath.Join(t.TempDir(), "x.pdf")
	os.WriteFile(path, pdf, 0o644)
	script := `import sys, pypdf
r = pypdf.PdfReader(sys.argv[1], strict=True)
print(len(r.pages), r.metadata.title)
print(r.pages[0].extract_text())`
	out, err := exec.Command(py, "-c", script, path).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if !strings.HasPrefix(string(out), "1 Invoice INV-2026-0142\n") || !strings.Contains(string(out), "£186.36") {
		t.Errorf("pypdf output:\n%s", out)
	}
}

func FuzzRender(f *testing.F) {
	f.Add(`<table><tr><td colspan=3>a</td></tr><tr><td>b<td>c</table>`)
	f.Add(`<ul><li><ol><li>x</ol></ul><p style="margin:-5px;width:-10px;border:9px dashed">y</p>`)
	f.Add(`<div style="display:inline-block;width:200%"><span style="background:red">z</span><br><br></div>`)
	f.Add(`<style>* { box-sizing: border-box; padding: 3em; font: bold 30px/2 serif }</style><pre>	a
b</pre>`)
	f.Fuzz(func(t *testing.T, src string) {
		Render(io.Discard, src, nil)
	})
}
