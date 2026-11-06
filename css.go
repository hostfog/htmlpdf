package htmlpdf

import (
	"strings"

	"golang.org/x/net/html"
)

type declaration struct {
	prop      string
	value     string
	important bool
}

type rule struct {
	sel   selector
	decls []declaration
}

// selector is a chain of compound selectors, rightmost last.
type selector []compound

type compound struct {
	comb       byte // relation to the previous compound: ' ' or '>'
	tag        string
	id         string
	classes    []string
	firstChild bool
	lastChild  bool
	root       bool
}

func parseStylesheet(src string) []rule {
	src = stripComments(src)
	var rules []rule
	for {
		src = strings.TrimSpace(src)
		if src == "" {
			return rules
		}
		if src[0] == '@' {
			brace := strings.IndexByte(src, '{')
			semi := strings.IndexByte(src, ';')
			if brace < 0 || semi >= 0 && semi < brace {
				if semi < 0 {
					return rules
				}
				src = src[semi+1:]
				continue
			}
			prelude := strings.ToLower(src[:brace])
			end := matchBrace(src, brace)
			if strings.HasPrefix(prelude, "@media") && (strings.Contains(prelude, "print") || strings.Contains(prelude, "all")) {
				rules = append(rules, parseStylesheet(src[brace+1:end])...)
			}
			src = src[min(end+1, len(src)):]
			continue
		}
		brace := strings.IndexByte(src, '{')
		if brace < 0 {
			return rules
		}
		end := matchBrace(src, brace)
		decls := parseDeclarations(src[brace+1 : end])
		for _, s := range splitTop(src[:brace], ',') {
			if sel, ok := parseSelector(s); ok {
				rules = append(rules, rule{sel: sel, decls: decls})
			}
		}
		src = src[min(end+1, len(src)):]
	}
}

func stripComments(s string) string {
	for {
		i := strings.Index(s, "/*")
		if i < 0 {
			return s
		}
		j := strings.Index(s[i+2:], "*/")
		if j < 0 {
			return s[:i]
		}
		s = s[:i] + " " + s[i+2+j+2:]
	}
}

// matchBrace returns the index of the brace closing the one at open, or
// len(s) if it is unbalanced.
func matchBrace(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(s)
}

// splitTop splits s at sep, ignoring separators inside quotes and brackets.
func splitTop(s string, sep byte) []string {
	var parts []string
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			depth--
		case c == sep && depth == 0:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

func parseDeclarations(s string) []declaration {
	var decls []declaration
	for _, part := range splitTop(stripComments(s), ';') {
		prop, value, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		prop = strings.ToLower(strings.TrimSpace(prop))
		value = strings.TrimSpace(value)
		d := declaration{prop: prop, value: value}
		if i := strings.LastIndexByte(value, '!'); i >= 0 && strings.EqualFold(strings.TrimSpace(value[i+1:]), "important") {
			d.value = strings.TrimSpace(value[:i])
			d.important = true
		}
		if prop != "" && d.value != "" {
			decls = append(decls, d)
		}
	}
	return decls
}

// parseSelector parses a complex selector. Selectors using unsupported
// features are rejected so they never match.
func parseSelector(s string) (selector, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	var sel selector
	comb := byte(0)
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f':
			if comb == 0 && len(sel) > 0 {
				comb = ' '
			}
			i++
			continue
		case c == '>':
			if len(sel) == 0 {
				return nil, false
			}
			comb = '>'
			i++
			continue
		case c == '+' || c == '~':
			return nil, false
		}
		if len(sel) > 0 && comb == 0 {
			return nil, false
		}
		var cp compound
		n, ok := cp.parse(s[i:])
		if !ok {
			return nil, false
		}
		cp.comb = comb
		sel = append(sel, cp)
		comb = 0
		i += n
	}
	if comb == '>' {
		return nil, false
	}
	return sel, len(sel) > 0
}

func (cp *compound) parse(s string) (int, bool) {
	i := 0
	if i < len(s) && s[i] == '*' {
		i++
	} else if n := identLen(s); n > 0 {
		cp.tag = strings.ToLower(s[:n])
		i = n
	}
	for i < len(s) {
		switch s[i] {
		case '#', '.':
			n := identLen(s[i+1:])
			if n == 0 {
				return 0, false
			}
			if s[i] == '#' {
				cp.id = s[i+1 : i+1+n]
			} else {
				cp.classes = append(cp.classes, s[i+1:i+1+n])
			}
			i += 1 + n
		case ':':
			n := identLen(s[i+1:])
			switch strings.ToLower(s[i+1 : i+1+n]) {
			case "first-child":
				cp.firstChild = true
			case "last-child":
				cp.lastChild = true
			case "root":
				cp.root = true
			default:
				return 0, false
			}
			i += 1 + n
		case ' ', '\t', '\n', '\r', '\f', '>', '+', '~':
			return i, i > 0
		default:
			return 0, false
		}
	}
	return i, i > 0
}

func identLen(s string) int {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '-' || c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80 {
			continue
		}
		return i
	}
	return len(s)
}

func (s selector) specificity() int {
	var a, b, c int
	for _, cp := range s {
		if cp.id != "" {
			a++
		}
		b += len(cp.classes)
		if cp.firstChild || cp.lastChild || cp.root {
			b++
		}
		if cp.tag != "" {
			c++
		}
	}
	return a<<16 | b<<8 | c
}

func (s selector) match(n *html.Node) bool {
	return s.matchAt(len(s)-1, n)
}

func (s selector) matchAt(i int, n *html.Node) bool {
	if !s[i].match(n) {
		return false
	}
	if i == 0 {
		return true
	}
	if s[i].comb == '>' {
		p := parentElement(n)
		return p != nil && s.matchAt(i-1, p)
	}
	for p := parentElement(n); p != nil; p = parentElement(p) {
		if s.matchAt(i-1, p) {
			return true
		}
	}
	return false
}

func (cp *compound) match(n *html.Node) bool {
	if cp.tag != "" && cp.tag != n.Data {
		return false
	}
	if cp.id != "" && attr(n, "id") != cp.id {
		return false
	}
	if len(cp.classes) > 0 {
		have := strings.Fields(attr(n, "class"))
		for _, c := range cp.classes {
			if !contains(have, c) {
				return false
			}
		}
	}
	if cp.root && parentElement(n) != nil {
		return false
	}
	if cp.firstChild {
		for s := n.PrevSibling; s != nil; s = s.PrevSibling {
			if s.Type == html.ElementNode {
				return false
			}
		}
	}
	if cp.lastChild {
		for s := n.NextSibling; s != nil; s = s.NextSibling {
			if s.Type == html.ElementNode {
				return false
			}
		}
	}
	return true
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func parentElement(n *html.Node) *html.Node {
	if p := n.Parent; p != nil && p.Type == html.ElementNode {
		return p
	}
	return nil
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
