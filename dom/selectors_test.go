// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package dom

import (
	"strings"
	"testing"
)

func TestAttributePresenceSelectors(t *testing.T) {
	tr := mustParse(t, `<main><div id="a" class="one" data-x="" title="yes"></div><div id="b" data-x="value"></div><span id="c" title=""></span></main>`, Limits{})
	for _, tc := range []struct{ selector, want string }{
		{"[data-x]", "ab"}, {"div[data-x]", "ab"}, {"*[title]", "ac"}, {"#b[data-x]", "b"}, {".one[data-x][title]", "a"}, {"main div[ DATA-X ]", "ab"}, {"main [title]", "ac"}, {"span[data-x]", ""},
	} {
		nodes, err := tr.Query(tr.Root(), tc.selector, true)
		if err != nil {
			t.Fatal(tc.selector, err)
		}
		got := ""
		for _, n := range nodes {
			id, _ := n.Attr("id")
			got += id
		}
		if got != tc.want {
			t.Fatalf("%s: %q want %q", tc.selector, got, tc.want)
		}
	}
	first, err := tr.Query(tr.Root(), "[data-x]", false)
	if err != nil || len(first) != 1 {
		t.Fatal(first, err)
	}
	tr.RemoveAttr(first[0], "data-x")
	nodes, err := tr.Query(tr.Root(), "[data-x]", true)
	if err != nil || len(nodes) != 1 {
		t.Fatal("query did not reflect mutation", err)
	}
}

// TestAttributeValueSelectors covers the CSS attribute comparisons with quoted
// and identifier values; es-module-shims probes script[type=esms-options].
func TestAttributeValueSelectors(t *testing.T) {
	tr := mustParse(t, `<main><script id="a" type="esms-options"></script><div id="b" class="one two" lang="en-US" data-x="value" title="a]b"></div><div id="c" lang="en" data-x="" title="x y"></div><span id="d" lang="english"></span></main>`, Limits{})
	for _, tc := range []struct{ selector, want string }{
		{"script[type=esms-options]", "a"}, {`script[type="esms-options"]`, "a"}, {"script[type='esms-options']", "a"}, {"[type=ESMS-options]", ""},
		{"[data-x=value]", "b"}, {`[data-x=""]`, "c"}, {`[TITLE = "a]b"]`, "b"}, {"[class~=two]", "b"}, {"[class~=on]", ""}, {`[title~="x y"]`, ""},
		{"[lang|=en]", "bc"}, {"[lang^=en]", "bcd"}, {"[lang$=US]", "b"}, {"[lang*=gl]", "d"}, {`main div[data-x^=""]`, ""}, {"div[class~=one][lang|=en]", "b"}, {"main [data-x][lang=en]", "c"},
	} {
		nodes, err := tr.Query(tr.Root(), tc.selector, true)
		if err != nil {
			t.Fatal(tc.selector, err)
		}
		got := ""
		for _, n := range nodes {
			id, _ := n.Attr("id")
			got += id
		}
		if got != tc.want {
			t.Fatalf("%s: %q want %q", tc.selector, got, tc.want)
		}
	}
}

// TestSelectorLists: comma-separated groups return each match once, in tree
// order; es-module-shims queries
// "script[type=module-shim],script[type=importmap-shim],link[rel=modulepreload-shim]".
func TestSelectorLists(t *testing.T) {
	tr := mustParse(t, `<main><link id="a" rel="modulepreload-shim"><div id="b" class="one"><p id="c" title="a,b"></p></div><script id="d" type="module-shim"></script></main>`, Limits{})
	for _, tc := range []struct{ selector, want string }{
		{"script[type=module-shim],script[type=importmap-shim],link[rel=modulepreload-shim]", "ad"},
		{"p, div", "bc"}, {"div,div p,.one", "bc"}, {`[title="a,b"],script`, "cd"}, {"main p , main script", "cd"}, {"span,em", ""},
	} {
		nodes, err := tr.Query(tr.Root(), tc.selector, true)
		if err != nil {
			t.Fatal(tc.selector, err)
		}
		got := ""
		for _, n := range nodes {
			id, _ := n.Attr("id")
			got += id
		}
		if got != tc.want {
			t.Fatalf("%s: %q want %q", tc.selector, got, tc.want)
		}
	}
	first, err := tr.Query(tr.Root(), "script,link", false)
	if err != nil || len(first) != 1 {
		t.Fatal(first, err)
	}
	if id, _ := first[0].Attr("id"); id != "a" {
		t.Fatalf("first match should follow tree order, got %s", id)
	}
	for _, bad := range []string{"p,", ",p", "p,,div", strings.Repeat("p,", 8) + "p", "p[title=a,b]", "p,div>"} {
		if _, err := tr.Query(tr.Root(), bad, true); err == nil {
			t.Fatal("accepted", bad)
		}
	}
}

func TestAttributeSelectorUnsupportedAndBounds(t *testing.T) {
	tr := mustParse(t, "<p title=x>hello</p>", Limits{})
	for _, selector := range []string{"p[", "p]", "p[]", "p[[title]]", "p[title=]", "p[title=\"x]", "p[title='x\"]", "p[title=x i]", "p[title=\"a\\\"b\"]", "p[title==x]", "p[=x]", "p[title!=x]", "p[title]tail", "p[ns|title]", "p[ti tle]", "p[title]>", strings.Repeat("p ", 9), "p" + strings.Repeat("[x]", 9), strings.Repeat("x", 4097)} {
		if _, err := tr.Query(tr.Root(), selector, true); err == nil {
			t.Fatal("accepted unsupported selector", selector)
		}
	}
}

func TestParsedScriptsRetainedWithinDOMBudgets(t *testing.T) {
	tr := mustParse(t, `<script nonce="x">alert(1)</script><script type="application/json">{}</script>`, Limits{})
	nodes, err := tr.Query(tr.Root(), "script[nonce]", true)
	if err != nil || len(nodes) != 1 || !nodes[0].ParserScript() || TextContent(nodes[0]) != "alert(1)" {
		t.Fatal("script DOM missing", err)
	}
	if strings.Contains(tr.Serialize(), "script") || strings.Contains(tr.Serialize(), "alert(1)") {
		t.Fatal("script leaked to renderer")
	}
	if _, err := Parse(strings.NewReader(`<script>`+strings.Repeat("x", 2048)+`</script>`), Limits{TotalBytes: 1024}); err == nil {
		t.Fatal("script bypassed DOM byte budget")
	}
}

func TestLongParsedScriptUsesBoundedTextNodes(t *testing.T) {
	source := strings.Repeat("/*π*/", 14000)
	tr := mustParse(t, "<script>"+source+"</script>", Limits{})
	if len(tr.Scripts) != 1 || tr.Scripts[0].Inline != source {
		t.Fatal("script changed while splitting")
	}
	nodes, _ := tr.Query(tr.Root(), "script", true)
	for _, child := range nodes[0].Children() {
		if len(child.Text()) > DefaultLimits.TextBytes {
			t.Fatal("oversize text node")
		}
	}
}
