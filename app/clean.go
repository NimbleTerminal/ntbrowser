package app

import "strings"

// clean is the terminal boundary for page-controlled text: titles, link
// hrefs in prompts, render warnings, error strings that echo page input, and
// log lines (which jsvm already strips; this is the second layer). It drops
// C0 controls, DEL, C1 controls and the Unicode line/paragraph separators,
// so nothing the status line prints can start an escape sequence or break a
// line. It is not ansi.Truncate, which preserves sequences.
func clean(s string) string {
	if strings.IndexFunc(s, unsafeRune) < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !unsafeRune(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func unsafeRune(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || r == 0x2028 || r == 0x2029
}
