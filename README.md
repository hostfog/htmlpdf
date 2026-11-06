# htmlpdf

HTML and CSS to PDF in pure Go. No headless Chrome, no wkhtmltopdf, no cgo. Built for invoices, receipts and reports.

```sh
go get github.com/hostfog/htmlpdf
```

## Usage

```go
f, _ := os.Create("invoice.pdf")
defer f.Close()

err := htmlpdf.Render(f, html, &htmlpdf.Options{
	PageSize: htmlpdf.A4,                          // or htmlpdf.Letter, or PageSize{w, h} in points
	Margins:  htmlpdf.Margins{Top: 20 * htmlpdf.Mm, Right: 15 * htmlpdf.Mm, Bottom: 20 * htmlpdf.Mm, Left: 15 * htmlpdf.Mm},
	BaseDir:  "templates",                         // for <img src> and <link rel=stylesheet>
	Fonts:    []htmlpdf.Font{{Family: "Inter", Data: interTTF}, {Family: "Inter", Bold: true, Data: interBoldTTF}},
	DefaultFont: "Inter",
})
```

[testdata/invoice.html](testdata/invoice.html) renders to [testdata/invoice.pdf](testdata/invoice.pdf):

![Invoice](testdata/invoice.png)

## Supported HTML/CSS

| Area | Support |
|------|---------|
| Stylesheets | `<style>`, `style=""`, `<link rel="stylesheet">` from `BaseDir`, `@media print` |
| Selectors | type, `*`, `.class`, `#id`, descendant, child `>`, compound (`p.a.b`), `:first-child`, `:last-child`, `:root`; specificity, `!important`, inheritance |
| Text | `color`, `font-family`, `font-size` (px, pt, em, rem, %, keywords), `font-weight`, `font-style`, `font`, `line-height`, `text-align` (left, right, center, justify), `text-decoration: underline`, `text-transform`, `white-space` |
| Box | `margin`, `padding`, `border` (shorthands and sides; solid, dashed, dotted), `border-radius`, `background-color`, `width`, `height`, `min-width`, `max-width`, `box-sizing` |
| Display | `block`, `inline`, `inline-block`, `none`, `list-item`, `table` and table parts |
| Lists | `ul`/`ol`, `start`, `list-style-type` disc, circle, square, decimal, alpha, roman |
| Tables | auto column widths, percentage widths, `colspan`, `border-collapse`, `border-spacing`, `vertical-align`, `<thead>` repeated on every page, `<tfoot>` |
| Images | PNG (with alpha), JPEG (embedded as is), GIF; intrinsic or CSS size; `data:` URLs |
| Pages | A4, Letter or custom; `page-break-before/after/inside` and `break-*`; long blocks and tables split across pages |
| Fonts | Helvetica, Times and Courier built in (WinAnsi); TrueType fonts embedded and subset, so any Unicode text the font covers works |
| PDF | links (`<a href>`), document title from `<title>`, compressed streams |

## Limitations

- No floats, positioning, flexbox or grid. Use tables or `inline-block` for columns.
- No complex text shaping: scripts that need ligatures or reordering (Gurmukhi conjuncts, Arabic) render glyph by glyph. No right-to-left text, no hyphenation, no kerning.
- No `rowspan`, `background-image`, `letter-spacing`, `opacity`, `::before`/`::after`, attribute or sibling selectors.
- Padding, borders and margins on inline elements are ignored; their backgrounds are drawn.
- A table row taller than a page is split mid-row; other rows are kept whole.
- Only TrueType (`glyf`) fonts can be embedded, not CFF/OpenType outlines. No synthetic bold or italic.
- Remote URLs are never fetched.

## License

MIT
