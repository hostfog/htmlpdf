package htmlpdf

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf16"
)

// pdfFile collects indirect objects in memory and writes them with a
// cross-reference table.
type pdfFile struct {
	objs [][]byte
}

func (p *pdfFile) alloc() int {
	p.objs = append(p.objs, nil)
	return len(p.objs)
}

func (p *pdfFile) set(id int, body string) {
	p.objs[id-1] = []byte(body)
}

func (p *pdfFile) add(body string) int {
	id := p.alloc()
	p.set(id, body)
	return id
}

// addStream adds a stream object. dict holds the entries other than /Length
// and /Filter.
func (p *pdfFile) addStream(dict string, data []byte, compress bool) int {
	if compress {
		var buf bytes.Buffer
		zw, _ := zlib.NewWriterLevel(&buf, zlib.DefaultCompression)
		zw.Write(data)
		zw.Close()
		data = buf.Bytes()
		dict += " /Filter /FlateDecode"
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "<<%s /Length %d >>\nstream\n", dict, len(data))
	b.Write(data)
	b.WriteString("\nendstream")
	p.objs = append(p.objs, b.Bytes())
	return len(p.objs)
}

func (p *pdfFile) writeTo(w io.Writer, root, info int) error {
	bw := bufio.NewWriter(w)
	cw := &countWriter{w: bw}
	io.WriteString(cw, "%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")

	offsets := make([]int64, len(p.objs))
	for i, body := range p.objs {
		offsets[i] = cw.n
		fmt.Fprintf(cw, "%d 0 obj\n", i+1)
		cw.Write(body)
		io.WriteString(cw, "\nendobj\n")
	}

	xref := cw.n
	fmt.Fprintf(cw, "xref\n0 %d\n0000000000 65535 f \n", len(p.objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(cw, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(cw, "trailer\n<< /Size %d /Root %d 0 R /Info %d 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(p.objs)+1, root, info, xref)
	if cw.err != nil {
		return cw.err
	}
	return bw.Flush()
}

type countWriter struct {
	w   io.Writer
	n   int64
	err error
}

func (c *countWriter) Write(b []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	n, err := c.w.Write(b)
	c.n += int64(n)
	c.err = err
	return n, err
}

func ref(id int) string {
	return strconv.Itoa(id) + " 0 R"
}

// num formats a coordinate with at most three decimals.
func num(v float64) string {
	s := strconv.FormatFloat(v, 'f', 3, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	if s == "-0" {
		return "0"
	}
	return s
}

// literal returns b as a PDF literal string.
func literal(b []byte) string {
	var s strings.Builder
	s.WriteByte('(')
	for _, c := range b {
		switch c {
		case '(', ')', '\\':
			s.WriteByte('\\')
			s.WriteByte(c)
		case '\r':
			s.WriteString(`\r`)
		case '\n':
			s.WriteString(`\n`)
		default:
			s.WriteByte(c)
		}
	}
	s.WriteByte(')')
	return s.String()
}

// textString encodes s as a PDF text string: literal for ASCII, UTF-16BE
// otherwise.
func textString(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return literal([]byte(s))
	}
	var b strings.Builder
	b.WriteString("<FEFF")
	for _, u := range utf16.Encode([]rune(s)) {
		fmt.Fprintf(&b, "%04X", u)
	}
	b.WriteByte('>')
	return b.String()
}
