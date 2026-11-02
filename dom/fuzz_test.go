// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package dom

import (
	"context"
	"errors"
	"strings"
	"testing"

	"nimbleterminal.dev/nthtml/document"
)

// Whatever the page, a tree inside dom.Limits serializes to at most its
// accounted bytes and to HTML nthtml's parser accepts.
func FuzzParseSerializeRoundTrip(f *testing.F) {
	f.Add("<p>x</p>")
	f.Add("<a href='/x'>y</a><ul><li>z")
	f.Add("<style>a</b</style>")
	f.Add("<p>a\xff\x00b</p>")
	f.Add("<link rel=stylesheet href=a.css media=print><noscript><p>x</p></noscript>")
	f.Fuzz(func(t *testing.T, html string) {
		tr, err := Parse(strings.NewReader(html), Limits{})
		if err != nil {
			return
		}
		out := tr.Serialize()
		if len(out) > tr.Bytes() {
			t.Fatalf("serialized %d > accounted %d", len(out), tr.Bytes())
		}
		if err := tr.SetStyle("a.css", "p{color:red}"); err == nil {
			if styled := tr.Serialize(); len(styled) > tr.Bytes() {
				t.Fatalf("with an injected sheet: serialized %d > accounted %d", len(styled), tr.Bytes())
			}
		}
		if _, err := document.Parse(context.Background(), strings.NewReader(out), document.DefaultLimits); err != nil && !errors.Is(err, document.ErrLimit) {
			t.Fatalf("nthtml rejects within-budget output: %v", err)
		}
	})
}
