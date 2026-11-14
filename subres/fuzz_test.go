// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package subres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"nimbleterminal.dev/nthtml/document"
)

// CountRules is the loader's estimate of what nthtml will charge against
// Limits.Rules. Under-counting lets a stylesheet through that then fails the
// whole frame, so: if nthtml can lay the sheet out at all, it must also do
// so within CountRules' figure.
func FuzzCountRules(f *testing.F) {
	for _, css := range []string{
		"p{color:red} h1,h2 , h3{margin:0}",
		"@media\n\t(min-width: 1px){a{color:red} b{color:blue}}",
		"@supports (display:grid){a{}} b{}",
		"a[title=\"x,y\"]{} b:not(.c,.d){} /* , } */ e{}",
		"@media x}y {\na{} b{} c{}\n}\np{}",
		"@font-face{src:url(x)} a{} @media a}b{c{}d{}e{}}",
		"a{content:\"\\n\"} b{} c{}",
		"\uFEFF@charset \"x\"; @import url(a.css); a{}",
		"a{b:c}}}{{d,e{}",
	} {
		f.Add(css)
	}
	ctx := context.Background()
	f.Fuzz(func(t *testing.T, css string) {
		n := CountRules(css)
		if n < 0 || n > document.MaxLimits.Rules+1 {
			t.Fatalf("CountRules = %d for %d bytes", n, len(css))
		}
		if len(css) > 4096 || strings.ContainsAny(css, "<\x00") {
			return // would leave the <style> element; the count above still ran
		}
		doc := "<style>" + css + "</style><p>x</p>"
		unlimited := document.DefaultLimits
		unlimited.Rules = 1 << 20
		if _, err := document.Parse(ctx, strings.NewReader(doc), unlimited); err != nil {
			return // nthtml refuses it for another reason
		}
		limited := document.DefaultLimits
		limited.Rules = max(n, 1) // zero means nthtml's default
		if _, err := document.Parse(ctx, strings.NewReader(doc), limited); errors.Is(err, document.ErrLimit) {
			t.Fatalf("CountRules = %d under-counts: %v\n%q", n, err, css)
		}
	})
}
