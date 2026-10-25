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
	f.Fuzz(func(t *testing.T, html string) {
		tr, err := Parse(strings.NewReader(html), Limits{})
		if err != nil {
			return
		}
		out := tr.Serialize()
		if len(out) > tr.Bytes() {
			t.Fatalf("serialized %d > accounted %d", len(out), tr.Bytes())
		}
		if _, err := document.Parse(context.Background(), strings.NewReader(out), document.DefaultLimits); err != nil && !errors.Is(err, document.ErrLimit) {
			t.Fatalf("nthtml rejects within-budget output: %v", err)
		}
	})
}
