package htmlpdf_test

import (
	"bytes"
	"fmt"
	"log"
	"os"

	"github.com/hostfog/htmlpdf"
	"golang.org/x/image/font/gofont/goregular"
)

func Example() {
	src := `<h1>Receipt</h1>
<table style="width: 100%; border-collapse: collapse">
  <tr><td>Coffee</td><td style="text-align: right">£3.20</td></tr>
  <tr style="font-weight: bold; border-top: 1px solid"><td>Total</td><td style="text-align: right">£3.20</td></tr>
</table>`

	var buf bytes.Buffer
	if err := htmlpdf.Render(&buf, src, nil); err != nil {
		log.Fatal(err)
	}
	fmt.Println(bytes.HasPrefix(buf.Bytes(), []byte("%PDF-")))
	// Output: true
}

func ExampleRender_options() {
	src, err := os.ReadFile("testdata/invoice.html")
	if err != nil {
		log.Fatal(err)
	}
	f, err := os.Create("invoice.pdf")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	err = htmlpdf.Render(f, string(src), &htmlpdf.Options{
		PageSize:    htmlpdf.Letter,
		Margins:     htmlpdf.Margins{Top: 20 * htmlpdf.Mm, Right: 15 * htmlpdf.Mm, Bottom: 20 * htmlpdf.Mm, Left: 15 * htmlpdf.Mm},
		BaseDir:     "testdata",
		Fonts:       []htmlpdf.Font{{Family: "Go", Data: goregular.TTF}},
		DefaultFont: "Go",
	})
	if err != nil {
		log.Fatal(err)
	}
}
