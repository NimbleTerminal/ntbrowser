// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package dom

import (
	"errors"
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

// TestCompoundSelectors: a compound stacks a tag, an id and classes.
func TestCompoundSelectors(t *testing.T) {
	tr := mustParse(t, `<main><div id="a" class="box one"></div><div id="b" class="box"></div><span id="c" class="box one"></span><div id="d"></div></main>`, Limits{})
	for _, tc := range []struct{ selector, want string }{
		{"div.box", "ab"}, {".box.one", "ac"}, {"div.box.one", "a"}, {"div#b", "b"}, {"div#b.box", "b"}, {"#a.one", "a"}, {"span.one#c", "c"}, {"*.box", "abc"}, {"main div.box", "ab"}, {"div.box[id=b]", "b"}, {"div.missing", ""}, {"span#a", ""},
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
	for _, bad := range []string{"div.", ".", "#", "div..a", "div#", "a#b#", "div>p", "1div", "div.a>"} {
		if _, err := tr.Query(tr.Root(), bad, true); err == nil {
			t.Fatal("accepted", bad)
		}
	}
}

// TestHasSelector: :has(relative selector) with descendant or leading-child
// combinators, once per compound and never nested; the gloss docs query
// "pre:has(code)" to make code blocks focusable.
func TestHasSelector(t *testing.T) {
	tr := mustParse(t, `<main><pre id="a"><code>x</code></pre><pre id="b">plain</pre><pre id="c"><span><code>deep</code></span></pre><div id="d" class="box"><p><a href="#">l</a></p></div><div id="e" class="box"><a href="#">direct</a></div><section id="f"><pre><code>in</code></pre></section></main>`, Limits{})
	for _, tc := range []struct{ selector, want string }{
		{"pre:has(code)", "ac"}, {"pre:has(> code)", "a"}, {"pre:has(> span code)", "c"}, {"pre:has(>code)", "a"},
		{":has(> code)", "a"}, {"div.box:has(a)", "de"}, {"div.box:has(> a)", "e"}, {"div.box:has(p a)", "d"}, {"div:has(> p a)", "d"},
		{"main :has(code)", "acf"}, {"section:has(pre code)", "f"}, {"section:has(pre code), pre:has(> code)", "af"}, {"pre[id=c]:has(code)", "c"}, {"pre:has(em)", ""}, {"main:has(code) pre:has(> code)", "a"},
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
	for _, bad := range []string{"pre:has()", "pre:has(code", "pre:has(code))", "pre:has(:has(code))", "pre:has(code, em)", "pre:has(+ code)", "pre:has(~ code)", "pre:has(code):has(em)", ":not(pre)", "pre:first-child", "pre:has(code)tail", "pre:has(code)[id=c]"} {
		if _, err := tr.Query(tr.Root(), bad, true); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	// The parser's own caps (8,000 nodes, depth 48) keep a :has() query under
	// 400k visits, so the work bound is a backstop; a state already near it
	// trips on the next search and the query reports ErrLimit.
	chain := strings.Repeat("<div>", 40) + "<span></span>" + strings.Repeat("</div>", 40)
	deep := mustParse(t, strings.Repeat(chain, 150), Limits{})
	if nodes, err := deep.Query(deep.Root(), "div:has(span)", true); err != nil || len(nodes) != 6000 {
		t.Fatalf("under the bound: %d %v", len(nodes), err)
	}
	q := &queryState{work: maxSelectorWork - 5}
	pred, err := compoundPredicate("div:has(span)", q, true)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := deep.Query(deep.Root(), "div", false)
	if pred(first[0]) || !q.exceeded {
		t.Fatal("work bound did not stop the search")
	}
	_ = errors.Is
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
