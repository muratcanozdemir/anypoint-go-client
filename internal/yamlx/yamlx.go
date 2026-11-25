// Package yamlx parses the block-style YAML subset used by policy
// definitions into JSON-like values (map[string]any, []any, string, bool,
// float64, nil): mappings, sequences, plain and quoted scalars, literal (|)
// and folded (>) block scalars, simple flow sequences/maps and comments.
// Anchors, tags and multi-document streams are not supported.
package yamlx

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type line struct {
	n      int    // 1-based line number
	indent int    // leading spaces
	text   string // content without indentation and comments
	raw    string // full line, for block scalars
}

type parser struct {
	lines []line
	i     int
}

// Unmarshal parses a YAML document.
func Unmarshal(src []byte) (any, error) {
	p := &parser{}
	for i, raw := range strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n") {
		if strings.Contains(raw, "\t") && strings.TrimLeft(raw, " ") != strings.TrimLeft(raw, " \t") {
			return nil, fmt.Errorf("line %d: tabs are not allowed for indentation", i+1)
		}
		text := strings.TrimRight(stripComment(raw), " ")
		trimmed := strings.TrimLeft(text, " ")
		if trimmed == "---" || trimmed == "..." {
			continue
		}
		p.lines = append(p.lines, line{n: i + 1, indent: len(text) - len(trimmed), text: trimmed, raw: raw})
	}
	p.skipBlank()
	if p.i >= len(p.lines) {
		return nil, nil
	}
	v, err := p.node(p.lines[p.i].indent)
	if err != nil {
		return nil, err
	}
	p.skipBlank()
	if p.i < len(p.lines) {
		return nil, fmt.Errorf("line %d: unexpected indentation", p.lines[p.i].n)
	}
	return v, nil
}

func (p *parser) skipBlank() {
	for p.i < len(p.lines) && p.lines[p.i].text == "" {
		p.i++
	}
}

func isSeqItem(t string) bool { return t == "-" || strings.HasPrefix(t, "- ") }

// node parses the mapping or sequence starting at the current line.
func (p *parser) node(indent int) (any, error) {
	p.skipBlank()
	if isSeqItem(p.lines[p.i].text) {
		return p.sequence(indent)
	}
	if _, _, ok := splitKey(p.lines[p.i].text); !ok {
		l := p.lines[p.i]
		p.i++
		return scalar(l.text), nil
	}
	return p.mapping(indent)
}

func (p *parser) sequence(indent int) (any, error) {
	out := []any{}
	for p.skipBlank(); p.i < len(p.lines); p.skipBlank() {
		l := &p.lines[p.i]
		if l.indent < indent || !isSeqItem(l.text) {
			break
		}
		if l.indent > indent {
			return nil, fmt.Errorf("line %d: bad indentation of a sequence entry", l.n)
		}
		rest := strings.TrimLeft(strings.TrimPrefix(l.text, "-"), " ")
		if rest == "" {
			p.i++
			p.skipBlank()
			if p.i >= len(p.lines) || p.lines[p.i].indent <= indent {
				out = append(out, nil)
				continue
			}
			v, err := p.node(p.lines[p.i].indent)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
			continue
		}
		// "- key: value" starts a mapping, "- - x" a nested sequence: re-read
		// the rest of the line as if it were indented on its own.
		if _, _, ok := splitKey(rest); ok || isSeqItem(rest) {
			l.indent += len(l.text) - len(rest)
			l.text = rest
			v, err := p.node(l.indent)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
			continue
		}
		p.i++
		if v, ok, err := p.blockScalar(rest, indent); ok || err != nil {
			if err != nil {
				return nil, err
			}
			out = append(out, v)
			continue
		}
		out = append(out, scalar(rest))
	}
	return out, nil
}

func (p *parser) mapping(indent int) (any, error) {
	out := map[string]any{}
	for p.skipBlank(); p.i < len(p.lines); p.skipBlank() {
		l := p.lines[p.i]
		if l.indent < indent {
			break
		}
		if l.indent > indent {
			return nil, fmt.Errorf("line %d: bad indentation of a mapping entry", l.n)
		}
		if isSeqItem(l.text) {
			break
		}
		key, val, ok := splitKey(l.text)
		if !ok {
			return nil, fmt.Errorf("line %d: expected \"key: value\"", l.n)
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("line %d: duplicate key %q", l.n, key)
		}
		p.i++
		if val != "" {
			v, isBlock, err := p.blockScalar(val, indent)
			if err != nil {
				return nil, err
			}
			if !isBlock {
				v = scalar(val)
			}
			out[key] = v
			continue
		}
		p.skipBlank()
		switch {
		case p.i < len(p.lines) && p.lines[p.i].indent > indent:
			v, err := p.node(p.lines[p.i].indent)
			if err != nil {
				return nil, err
			}
			out[key] = v
		case p.i < len(p.lines) && p.lines[p.i].indent == indent && isSeqItem(p.lines[p.i].text):
			v, err := p.sequence(indent) // "key:\n- a" (sequence not indented)
			if err != nil {
				return nil, err
			}
			out[key] = v
		default:
			out[key] = nil
		}
	}
	return out, nil
}

// blockScalar reads a | or > scalar whose header is val, with content
// indented more than parent.
func (p *parser) blockScalar(val string, parent int) (any, bool, error) {
	if val == "" || (val[0] != '|' && val[0] != '>') {
		return nil, false, nil
	}
	header := val[1:]
	chomp := byte(0)
	for _, c := range []byte(header) {
		switch {
		case c == '-' || c == '+':
			chomp = c
		case c >= '1' && c <= '9':
		default:
			return nil, false, fmt.Errorf("bad block scalar header %q", val)
		}
	}
	var body []string
	indent := -1
	for p.i < len(p.lines) {
		raw := strings.TrimRight(p.lines[p.i].raw, " ")
		content := strings.TrimLeft(raw, " ")
		ind := len(raw) - len(content)
		if content == "" {
			body = append(body, "")
			p.i++
			continue
		}
		if ind <= parent {
			break
		}
		if indent < 0 {
			indent = ind
		}
		if ind < indent {
			break
		}
		body = append(body, raw[indent:])
		p.i++
	}
	// trailing blank lines belong to the chomping, not the content
	trail := 0
	for len(body) > 0 && body[len(body)-1] == "" {
		body = body[:len(body)-1]
		trail++
	}
	var s string
	if val[0] == '|' {
		s = strings.Join(body, "\n")
	} else {
		var b strings.Builder
		for i, ln := range body {
			switch {
			case i == 0:
			case ln == "" || body[i-1] == "" || strings.HasPrefix(ln, " ") || strings.HasPrefix(body[i-1], " "):
				b.WriteByte('\n')
			default:
				b.WriteByte(' ')
			}
			b.WriteString(ln)
		}
		s = b.String()
	}
	switch chomp {
	case '-':
	case '+':
		s += "\n" + strings.Repeat("\n", trail)
	default:
		if len(body) > 0 {
			s += "\n"
		}
	}
	return s, true, nil
}

// splitKey splits "key: value" (or "key:") outside of quotes.
func splitKey(t string) (key, val string, ok bool) {
	if t == "" || t[0] == '[' || t[0] == '{' {
		return "", "", false
	}
	if t[0] == '"' || t[0] == '\'' {
		end := closingQuote(t)
		if end < 0 || end+1 >= len(t) || t[end+1] != ':' {
			return "", "", false
		}
		rest := t[end+2:]
		if rest != "" && rest[0] != ' ' {
			return "", "", false
		}
		k, _ := scalar(t[:end+1]).(string)
		return k, strings.TrimSpace(rest), true
	}
	for i := 0; i < len(t); i++ {
		if t[i] == ':' && (i+1 == len(t) || t[i+1] == ' ') {
			return strings.TrimSpace(t[:i]), strings.TrimSpace(t[i+1:]), true
		}
	}
	return "", "", false
}

func closingQuote(t string) int {
	q := t[0]
	for i := 1; i < len(t); i++ {
		switch {
		case q == '"' && t[i] == '\\':
			i++
		case q == '\'' && t[i] == '\'' && i+1 < len(t) && t[i+1] == '\'':
			i++
		case t[i] == q:
			return i
		}
	}
	return -1
}

// stripComment removes a "#" comment that starts the line or follows a space,
// outside of quotes.
func stripComment(s string) string {
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q == '"' && c == '\\':
			i++
		case q != 0:
			if c == q {
				q = 0
			}
		case (c == '"' || c == '\'') && (i == 0 || strings.ContainsRune(" :-[{,", rune(s[i-1]))):
			q = c
		case c == '#' && (i == 0 || s[i-1] == ' '):
			return s[:i]
		}
	}
	return s
}

// scalar converts a plain, quoted or flow scalar.
func scalar(s string) any {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return nil
	case s[0] == '"':
		if v, err := strconv.Unquote(s); err == nil {
			return v
		}
		return strings.Trim(s, `"`)
	case s[0] == '\'':
		return strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(s, "'"), "'"), "''", "'")
	case s[0] == '[' && strings.HasSuffix(s, "]"):
		out := []any{}
		for _, part := range splitFlow(s[1 : len(s)-1]) {
			out = append(out, scalar(part))
		}
		return out
	case s[0] == '{' && strings.HasSuffix(s, "}"):
		out := map[string]any{}
		for _, part := range splitFlow(s[1 : len(s)-1]) {
			if k, v, ok := splitKey(part); ok {
				out[k] = scalar(v)
			}
		}
		return out
	}
	switch s {
	case "~", "null", "Null", "NULL":
		return nil
	case "true", "True", "TRUE":
		return true
	case "false", "False", "FALSE":
		return false
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && !strings.ContainsAny(s, "_xXoObB") {
		return f
	}
	return s
}

// splitFlow splits a flow collection's content on top-level commas.
func splitFlow(s string) []string {
	var out []string
	depth, start := 0, 0
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			if c == '\\' && q == '"' {
				i++
			} else if c == q {
				q = 0
			}
		case c == '"' || c == '\'':
			q = c
		case c == '[' || c == '{':
			depth++
		case c == ']' || c == '}':
			depth--
		case c == ',' && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if strings.TrimSpace(s[start:]) != "" {
		out = append(out, s[start:])
	}
	return out
}

// ErrNotMapping is returned by Map when the document is not a mapping.
var ErrNotMapping = errors.New("YAML document is not a mapping")

// Map parses a YAML document that must be a mapping.
func Map(src []byte) (map[string]any, error) {
	v, err := Unmarshal(src)
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, ErrNotMapping
	}
	return m, nil
}
