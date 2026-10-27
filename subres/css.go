// Package subres loads a page's subresources (stylesheets, images, CSS
// backgrounds) for ntbrowser. This file holds the CSS text tools: a rule
// counter that mirrors nthtml's budget, the @import scanner and the url()
// absolutizer. All three share one tokenizer that understands comments,
// strings and nested parentheses, and nothing else about CSS.
package subres

import (
	"net/url"
	"strings"
)

// Import is one leading @import statement of a stylesheet.
type Import struct {
	URL         string // as written, unresolved
	Media       string // condition after the URL, trimmed; "" when none
	Unsupported bool   // layer()/layer/supports() present: fail closed (decision 17)
	start, end  int    // byte range of the statement in the sheet, end exclusive (after ';')
}

// Range reports the statement's byte range in the sheet it was scanned from.
func (im Import) Range() (start, end int) { return im.start, im.end }

type scanner struct {
	s string
	i int
}

func isCSSSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' }

// skipTrivia advances over whitespace and comments.
func (sc *scanner) skipTrivia() {
	for sc.i < len(sc.s) {
		switch {
		case isCSSSpace(sc.s[sc.i]):
			sc.i++
		case strings.HasPrefix(sc.s[sc.i:], "/*"):
			end := strings.Index(sc.s[sc.i+2:], "*/")
			if end < 0 {
				sc.i = len(sc.s)
				return
			}
			sc.i += 2 + end + 2
		default:
			return
		}
	}
}

// skipString advances past a quoted string starting at sc.i (a quote char).
func (sc *scanner) skipString() {
	q := sc.s[sc.i]
	sc.i++
	for sc.i < len(sc.s) {
		c := sc.s[sc.i]
		if c == '\\' && sc.i+1 < len(sc.s) {
			sc.i += 2
			continue
		}
		sc.i++
		if c == q {
			return // like nthtml's scanner, a string runs to its closing quote, newlines included
		}
	}
}

// next returns the index of the next top-level occurrence of one of the
// stop bytes, skipping comments, strings and parenthesised groups, or -1.
func (sc *scanner) next(stops string) int {
	depth := 0
	for sc.i < len(sc.s) {
		c := sc.s[sc.i]
		switch {
		case c == '"' || c == '\'':
			sc.skipString()
			continue
		case strings.HasPrefix(sc.s[sc.i:], "/*"):
			sc.skipTrivia()
			continue
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			if depth > 0 {
				depth--
			}
		case depth == 0 && strings.IndexByte(stops, c) >= 0:
			return sc.i
		}
		sc.i++
	}
	return -1
}

// matchBrace returns the index of the '}' closing the '{' at open, or -1.
func matchBrace(s string, open int) int {
	sc := &scanner{s: s, i: open + 1}
	depth := 1
	for {
		idx := sc.next("{}")
		if idx < 0 {
			return -1
		}
		if s[idx] == '{' {
			depth++
		} else {
			depth--
			if depth == 0 {
				return idx
			}
		}
		sc.i = idx + 1
	}
}

// splitTopLevel splits s on top-level commas (outside strings, parentheses
// and brackets), trimming each part and dropping empty ones.
func splitTopLevel(s string) []string {
	var out []string
	sc := &scanner{s: s}
	start := 0
	for {
		idx := sc.next(",")
		if idx < 0 {
			break
		}
		out = appendPart(out, s[start:idx])
		sc.i = idx + 1
		start = sc.i
	}
	return appendPart(out, s[start:])
}

func appendPart(out []string, part string) []string {
	if p := strings.TrimSpace(stripComments(part)); p != "" {
		out = append(out, p)
	}
	return out
}

// stripComments removes /* */ comments exactly as nthtml's parseCSS does
// (no replacement text, an unterminated comment eats the rest).
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
		s = s[:i] + s[i+2+j+2:]
	}
}

func isMediaPrelude(prelude string) bool {
	if len(prelude) < 6 || !strings.EqualFold(prelude[:6], "@media") {
		return false
	}
	if len(prelude) == 6 {
		return true
	}
	c := prelude[6]
	return isCSSSpace(c) || c == '(' || c == '/'
}

// CountRules counts what nthtml counts against Limits.Rules: every
// comma-separated selector of every style rule, at any depth inside @media
// blocks whatever their condition; nothing inside other at-rule blocks
// (@supports, @font-face, @keyframes, @layer), which nthtml skips; statement
// at-rules, strings and comments skipped. An unparseable group still counts
// its parts (over-counting is the safe direction).
func CountRules(css string) int {
	return countBlock(stripComments(strings.TrimPrefix(css, "\uFEFF")))
}

func countBlock(s string) int {
	n := 0
	sc := &scanner{s: s}
	for {
		sc.skipTrivia()
		if sc.i >= len(s) {
			return n
		}
		start := sc.i
		var idx int
		if s[start] == '@' {
			idx = sc.next("{;") // nthtml's scanPreludeEnd: an at-rule prelude ends only at ';' or '{'
		} else {
			idx = sc.next("{;}")
		}
		if idx < 0 {
			return n
		}
		switch s[idx] {
		case ';':
			sc.i = idx + 1 // a statement (@import, @charset, stray ';')
		case '}':
			sc.i = idx + 1 // stray close brace: skip
		case '{':
			close := matchBrace(s, idx)
			if close < 0 {
				return n
			}
			prelude := strings.TrimSpace(s[start:idx])
			body := s[idx+1 : close]
			switch {
			case strings.HasPrefix(prelude, "@"):
				if isMediaPrelude(prelude) {
					n += countBlock(body)
				}
			default:
				n += len(splitTopLevel(prelude))
			}
			sc.i = close + 1
		}
	}
}

// Imports returns the sheet's leading @import statements (after an optional
// BOM, @charset and @layer statements) and the text from the first other
// construct on.
func Imports(css string) (stmts []Import, rest string) {
	s := strings.TrimPrefix(css, "\uFEFF")
	sc := &scanner{s: s}
	restStart := 0
	for {
		sc.skipTrivia()
		if sc.i >= len(s) {
			return stmts, ""
		}
		start := sc.i
		if s[start] != '@' {
			break
		}
		idx := sc.next("{;")
		if idx < 0 || s[idx] == '{' {
			break
		}
		stmt := strings.TrimSpace(s[start:idx])
		lower := strings.ToLower(stmt)
		switch {
		case strings.HasPrefix(lower, "@charset"), strings.HasPrefix(lower, "@layer"):
			sc.i = idx + 1
			restStart = sc.i
			continue
		case strings.HasPrefix(lower, "@import"):
			im := parseImport(strings.TrimSpace(stmt[len("@import"):]))
			im.start, im.end = start, idx+1
			stmts = append(stmts, im)
			sc.i = idx + 1
			restStart = sc.i
			continue
		}
		break
	}
	// rest begins at the first non-import construct, trivia trimmed
	sc2 := &scanner{s: s, i: restStart}
	sc2.skipTrivia()
	return stmts, s[sc2.i:]
}

func parseImport(arg string) Import {
	var im Import
	var after string
	switch {
	case strings.HasPrefix(strings.ToLower(arg), "url("):
		end := strings.IndexByte(arg, ')')
		if end < 0 {
			return Import{Unsupported: true}
		}
		im.URL = unquote(strings.TrimSpace(arg[4:end]))
		after = arg[end+1:]
	case arg != "" && (arg[0] == '"' || arg[0] == '\''):
		end := strings.IndexByte(arg[1:], arg[0])
		if end < 0 {
			return Import{Unsupported: true}
		}
		im.URL = arg[1 : 1+end]
		after = arg[2+end:]
	default:
		return Import{Unsupported: true}
	}
	after = strings.TrimSpace(after)
	lower := strings.ToLower(after)
	if strings.HasPrefix(lower, "layer") || strings.Contains(lower, "supports(") {
		im.Unsupported = true
		return im
	}
	im.Media = after
	return im
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// Absolutize rewrites every relative url() reference outside comments and
// strings against sheetURL, so nthtml's background keys (the url() text as
// written) are fetchable and unambiguous. data:, absolute and fragment
// references are left alone; an unparsable sheetURL returns css unchanged.
func Absolutize(css, sheetURL string) string {
	base, err := url.Parse(sheetURL)
	if err != nil || base.Scheme == "" {
		return css
	}
	var b strings.Builder
	sc := &scanner{s: css}
	last := 0
	for sc.i < len(css) {
		c := css[sc.i]
		switch {
		case c == '"' || c == '\'':
			sc.skipString()
			continue
		case strings.HasPrefix(css[sc.i:], "/*"):
			sc.skipTrivia()
			continue
		case len(css)-sc.i >= 4 && strings.EqualFold(css[sc.i:sc.i+4], "url("):
			open := sc.i + 4
			end := strings.IndexByte(css[open:], ')')
			if end < 0 {
				sc.i = len(css)
				continue
			}
			raw := strings.TrimSpace(css[open : open+end])
			ref := unquote(raw)
			if resolved, ok := resolveRef(base, ref); ok {
				b.WriteString(css[last:sc.i])
				b.WriteString(`url("`)
				b.WriteString(resolved)
				b.WriteString(`")`)
				last = open + end + 1
			}
			sc.i = open + end + 1
			continue
		}
		sc.i++
	}
	if last == 0 {
		return css
	}
	b.WriteString(css[last:])
	return b.String()
}

func resolveRef(base *url.URL, ref string) (string, bool) {
	if ref == "" || strings.HasPrefix(ref, "#") || strings.ContainsAny(ref, "\n\r\"'") {
		return "", false
	}
	if strings.HasPrefix(strings.ToLower(ref), "data:") {
		return "", false
	}
	u, err := url.Parse(ref)
	if err != nil || u.Scheme != "" {
		return "", false
	}
	return base.ResolveReference(u).String(), true
}
