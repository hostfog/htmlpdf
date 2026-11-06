// Package htmlpdf renders HTML and CSS to PDF in pure Go, without a browser
// or external tools. It targets documents such as invoices, receipts and
// reports, and supports a practical subset of CSS 2.1.
package htmlpdf

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"strings"

	"golang.org/x/net/html"
)

// Mm is one millimetre in points.
const Mm = 72 / 25.4

// PageSize is a page size in points.
type PageSize struct {
	Width, Height float64
}

var (
	A4     = PageSize{595.28, 841.89}
	Letter = PageSize{612, 792}
)

// Margins are page margins in points.
type Margins struct {
	Top, Right, Bottom, Left float64
}

// Options configure rendering. The zero value renders A4 pages with 15mm
// margins.
type Options struct {
	PageSize PageSize
	Margins  Margins

	// BaseDir is the directory that relative image and stylesheet URLs are
	// resolved against. Paths cannot escape it. Remote URLs are not
	// fetched; data: URLs always work.
	BaseDir string

	// Fonts are TrueType fonts available to font-family. They are also used
	// for characters that the requested font cannot draw.
	Fonts []Font

	// DefaultFont is the font family of the root element. It defaults to
	// Helvetica.
	DefaultFont string
}

type renderer struct {
	opts     Options
	fsys     fs.FS
	rules    []rule
	rootSize float64
	fonts    *fontSet
	images   map[string]*pdfImage
	title    string
}

// Render writes src as a PDF document to w.
func Render(w io.Writer, src string, opts *Options) error {
	r, err := newRenderer(opts)
	if err != nil {
		return err
	}
	l, err := r.layout(src)
	if err != nil {
		return err
	}
	return r.write(w, l)
}

func newRenderer(opts *Options) (*renderer, error) {
	var o Options
	if opts != nil {
		o = *opts
	}
	if o.PageSize == (PageSize{}) {
		o.PageSize = A4
	}
	if o.Margins == (Margins{}) {
		o.Margins = Margins{15 * Mm, 15 * Mm, 15 * Mm, 15 * Mm}
	}
	if o.DefaultFont == "" {
		o.DefaultFont = "Helvetica"
	}
	if o.PageSize.Width-o.Margins.Left-o.Margins.Right <= 0 || o.PageSize.Height-o.Margins.Top-o.Margins.Bottom <= 0 {
		return nil, errors.New("htmlpdf: margins leave no room on the page")
	}
	fonts, err := newFontSet(o.Fonts)
	if err != nil {
		return nil, err
	}
	r := &renderer{opts: o, fonts: fonts, images: map[string]*pdfImage{}, rootSize: 12}
	if o.BaseDir != "" {
		r.fsys = os.DirFS(o.BaseDir)
	}
	return r, nil
}

func (r *renderer) layout(src string) (*layout, error) {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return nil, fmt.Errorf("htmlpdf: %w", err)
	}
	if err := r.collect(doc); err != nil {
		return nil, err
	}
	root := findElement(doc, "html")
	if root == nil {
		return nil, errors.New("htmlpdf: no html element")
	}

	initial := &style{
		color:      color{ok: true},
		families:   []string{r.opts.DefaultFont},
		size:       12,
		lineNum:    1.15,
		align:      "left",
		whiteSpace: "normal",
		listStyle:  "disc",
	}
	tree, err := r.build(root, initial)
	if err != nil {
		return nil, err
	}

	o := r.opts
	l := &layout{fonts: r.fonts, pageH: o.PageSize.Height - o.Margins.Top - o.Margins.Bottom}
	l.blocks(tree, 0, 0, o.PageSize.Width-o.Margins.Left-o.Margins.Right)
	return l, nil
}

// collect gathers stylesheets and the title in document order.
func (r *renderer) collect(n *html.Node) error {
	if n.Type == html.ElementNode {
		switch n.Data {
		case "style":
			if m := attr(n, "media"); m == "" || strings.Contains(m, "print") || strings.Contains(m, "all") {
				r.rules = append(r.rules, parseStylesheet(textContent(n))...)
			}
		case "link":
			if strings.EqualFold(attr(n, "rel"), "stylesheet") && attr(n, "href") != "" {
				css, err := r.load(attr(n, "href"))
				if err != nil {
					return err
				}
				r.rules = append(r.rules, parseStylesheet(string(css))...)
			}
		case "title":
			if r.title == "" {
				r.title = strings.Join(strings.Fields(textContent(n)), " ")
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if err := r.collect(c); err != nil {
			return err
		}
	}
	return nil
}

// load reads a resource referenced from the document.
func (r *renderer) load(ref string) ([]byte, error) {
	if strings.HasPrefix(ref, "data:") {
		meta, data, ok := strings.Cut(ref[5:], ",")
		if !ok {
			return nil, errors.New("htmlpdf: malformed data URL")
		}
		if strings.HasSuffix(meta, ";base64") {
			b, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(data), ""))
			if err != nil {
				return nil, fmt.Errorf("htmlpdf: data URL: %w", err)
			}
			return b, nil
		}
		s, err := url.PathUnescape(data)
		return []byte(s), err
	}
	if strings.Contains(ref, "://") || strings.HasPrefix(ref, "//") {
		return nil, fmt.Errorf("htmlpdf: remote resource %q: only local files and data: URLs are supported", ref)
	}
	if r.fsys == nil {
		return nil, fmt.Errorf("htmlpdf: cannot load %q without Options.BaseDir", ref)
	}
	if i := strings.IndexAny(ref, "?#"); i >= 0 {
		ref = ref[:i]
	}
	p, err := url.PathUnescape(ref)
	if err != nil {
		return nil, fmt.Errorf("htmlpdf: %q: %w", ref, err)
	}
	p = strings.TrimPrefix(path.Clean("/"+p), "/")
	b, err := fs.ReadFile(r.fsys, p)
	if err != nil {
		return nil, fmt.Errorf("htmlpdf: %w", err)
	}
	return b, nil
}

func findElement(n *html.Node, tag string) *html.Node {
	if n.Type == html.ElementNode && n.Data == tag {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := findElement(c, tag); f != nil {
			return f
		}
	}
	return nil
}

func textContent(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}
