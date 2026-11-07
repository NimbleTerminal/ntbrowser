// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package dom

import (
	"errors"
	"strings"
	"testing"
)

func TestInnerHTMLContextAndInertScripts(t *testing.T) {
	tree, err := Parse(strings.NewReader(`<table id=t></table><div id=d></div><script id=s></script>`), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, source, want string }{
		{"t", "<tr><td>hello &amp; bye</td></tr>", "<tbody><tr><td>hello &amp; bye</td></tr></tbody>"},
		{"d", `<p onclick="bad()">one</p><script src="/evil.js">bad()</script>`, `<p>one</p><script src="/evil.js">bad()</script>`},
		{"s", `if(a<b) x='&';`, `if(a<b) x='&';`},
	} {
		n := tree.ByID(tc.id)
		if err := tree.SetInnerHTML(n, tc.source); err != nil {
			t.Fatal(err)
		}
		if got := tree.InnerHTML(n); got != tc.want {
			t.Fatalf("%s: %q", tc.id, got)
		}
	}
	script := tree.ByID("d").children[1]
	if !script.ParserScript() || strings.Contains(tree.Serialize(), "evil.js") {
		t.Fatal("fragment script not inert")
	}
	if len(tree.Scripts) != 1 {
		t.Fatal("fragment changed execution plan")
	}
}

func TestInnerHTMLAtomicBudgetAndRetainedChildren(t *testing.T) {
	tree, _ := Parse(strings.NewReader(`<div id=d><b id=old>old</b></div>`), Limits{Nodes: 12})
	n, old := tree.ByID("d"), tree.ByID("old")
	before, live, bytes := tree.Serialize(), tree.Live(), tree.Bytes()
	for _, markup := range []string{strings.Repeat("<i></i>", 20), strings.Repeat("x", MaxFragmentBytes+1), strings.Repeat("<!--x-->", MaxFragmentTokens+1)} {
		if err := tree.SetInnerHTML(n, markup); !errors.Is(err, ErrLimit) {
			t.Fatal("missing budget failure", err)
		}
		if tree.Serialize() != before || tree.Live() != live || tree.Bytes() != bytes || old.Parent() != n {
			t.Fatal("failed write mutated tree")
		}
	}
	if err := tree.SetInnerHTML(n, `<span>new</span>`); err != nil {
		t.Fatal(err)
	}
	if old.Parent() != nil || tree.Node(old.ID()) != old || TextContent(old) != "old" {
		t.Fatal("old subtree lost")
	}
	if err := tree.Append(n, old); err != nil {
		t.Fatal(err)
	}
	if len(tree.Serialize()) > tree.Bytes() {
		t.Fatal("serialized budget undercharged")
	}
}

func FuzzInnerHTMLBounded(f *testing.F) {
	for _, s := range []string{`<table><tr><td>x`, `<script>x</script>`, `<svg><foreignObject><p>x</p></foreignObject></svg>`, strings.Repeat("<b>", 80)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, source string) {
		tree, err := Parse(strings.NewReader(`<div id=target>old</div>`), Limits{})
		if err != nil {
			t.Fatal(err)
		}
		n := tree.ByID("target")
		before := tree.Serialize()
		live, bytes := tree.Live(), tree.Bytes()
		if err := tree.SetInnerHTML(n, source); err != nil {
			if tree.Serialize() != before || tree.Live() != live || tree.Bytes() != bytes {
				t.Fatal("non-atomic parse")
			}
		} else if tree.Live() > DefaultLimits.Nodes || tree.Bytes() > DefaultLimits.TotalBytes || len(tree.Serialize()) > tree.Bytes() {
			t.Fatal("budget exceeded")
		}
	})
}
