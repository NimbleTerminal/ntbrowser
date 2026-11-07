// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package dom

import (
	"errors"
	"strings"
	"testing"
)

func mustParse(t *testing.T, html string, l Limits) *Tree {
	t.Helper()
	tr, err := Parse(strings.NewReader(html), l)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func firstLink(t *testing.T, tr *Tree) *Node {
	t.Helper()
	n := tr.find(tr.Root(), func(n *Node) bool { return n.tag == "link" })
	if n == nil {
		t.Fatal("no <link>")
	}
	return n
}

func TestLinkedStylesRecognition(t *testing.T) {
	tr := mustParse(t, `<head>
<link rel="stylesheet" href="a.css">
<link rel="Stylesheet preload" href="b.css">
<link rel="alternate stylesheet" href="alt.css">
<link rel="stylesheet" href="dis.css" disabled>
<link rel="stylesheet" href="less.css" type="text/less">
<link rel="stylesheet" href="">
<noscript><link rel="stylesheet" href="n.css"></noscript>
<link rel="stylesheet" href="a.css">
</head>`, Limits{})
	got := tr.LinkedStyles()
	if strings.Join(got, ",") != "a.css,b.css" {
		t.Fatalf("LinkedStyles = %v", got)
	}
}

func TestImageSourcesOrderAndDedup(t *testing.T) {
	tr := mustParse(t, `<img src="x.png"><img src=""><img src="x.png"><noscript><img src="n.png"></noscript><img src="data:image/png;base64,AA">`, Limits{})
	got := tr.ImageSources()
	if strings.Join(got, ",") != "x.png,data:image/png;base64,AA" {
		t.Fatalf("ImageSources = %v", got)
	}
}

func TestStyleTextsSkipNoscript(t *testing.T) {
	tr := mustParse(t, `<style>a{}</style><noscript><style>n{}</style></noscript><p>x</p><style>b{}</style>`, Limits{})
	got := tr.StyleTexts()
	if strings.Join(got, "|") != "a{}|b{}" {
		t.Fatalf("StyleTexts = %q", got)
	}
}

func TestSerializeInjectsStyleAfterLink(t *testing.T) {
	tr := mustParse(t, `<head><link rel="stylesheet" href="a.css"><link rel="stylesheet" href="a.css"></head><body><p>x</p></body>`, Limits{})
	if err := tr.SetStyle("a.css", "h1{color:red}"); err != nil {
		t.Fatal(err)
	}
	out := tr.Serialize()
	want := `<link rel="stylesheet" href="a.css"><style>h1{color:red}</style>`
	if strings.Count(out, want) != 1 || strings.Count(out, "<style>") != 1 {
		t.Fatalf("twin emitted wrongly:\n%s", out)
	}
}

func TestSerializeWrapsMedia(t *testing.T) {
	cases := []struct {
		media, want string
		none        bool
	}{
		{"print", `<style>@media print {h1{color:red}}</style>`, false},
		{"all", `<style>h1{color:red}</style>`, false},
		{strings.Repeat("s", 1025), "", true},
		{"print}</style><h1>x", "", true},
	}
	for _, c := range cases {
		tr := mustParse(t, `<link rel="stylesheet" href="a.css" media="`+c.media+`"><p>x</p>`, Limits{})
		if err := tr.SetStyle("a.css", "h1{color:red}"); err != nil {
			t.Fatal(err)
		}
		out := tr.Serialize()
		if c.none {
			if strings.Contains(out, "<style>") || strings.Contains(out, "</style><h1>") {
				t.Fatalf("media %q: twin must not be emitted:\n%s", c.media[:min(20, len(c.media))], out)
			}
			continue
		}
		if !strings.Contains(out, c.want) {
			t.Fatalf("media %q: want %q in\n%s", c.media, c.want, out)
		}
	}
}

func TestSerializeSkipsNoscript(t *testing.T) {
	tr := mustParse(t, `<body><noscript><p>enable JS</p></noscript><p>ok</p></body>`, Limits{})
	out := tr.Serialize()
	if strings.Contains(out, "enable JS") || strings.Contains(out, "<noscript") || !strings.Contains(out, "<p>ok</p>") {
		t.Fatalf("noscript leaked:\n%s", out)
	}
}

func TestSetStyleSanitizesAndCharges(t *testing.T) {
	for _, media := range []string{"", "print"} {
		attr := ""
		if media != "" {
			attr = ` media="` + media + `"`
		}
		tr := mustParse(t, `<link rel="stylesheet" href="a.css"`+attr+`><p>x</p>`, Limits{})
		before := tr.Bytes()
		raw := "a{}</style><b>"
		if err := tr.SetStyle("a.css", raw); err != nil {
			t.Fatal(err)
		}
		sanitized := `a{}<\/style><b>`
		if !strings.Contains(tr.Serialize(), sanitized) {
			t.Fatalf("media %q: not sanitized:\n%s", media, tr.Serialize())
		}
		want := before + len(sanitized) + 15
		if media != "" {
			want += 10 + len(media)
		}
		if tr.Bytes() != want {
			t.Fatalf("media %q: Bytes()=%d want %d", media, tr.Bytes(), want)
		}
		if err := tr.SetStyle("a.css", raw); err != nil || tr.Bytes() != want {
			t.Fatalf("media %q: same text must be a no-op: %v %d", media, err, tr.Bytes())
		}
		if err := tr.SetStyle("a.css", "a{}"); err != nil || tr.Bytes() != want-(len(sanitized)-3) {
			t.Fatalf("media %q: shorter text must credit the difference: %v %d", media, err, tr.Bytes())
		}
		if err := tr.SetStyle("a.css", ""); err != nil || tr.Bytes() != before || strings.Contains(tr.Serialize(), "<style>") {
			t.Fatalf("media %q: empty text must remove the twin: %v %d", media, err, tr.Bytes())
		}
		if err := tr.SetStyle("a.css", raw); err != nil {
			t.Fatal(err)
		}
		tr.ClearStyles()
		if tr.Bytes() != before {
			t.Fatalf("media %q: ClearStyles left %d, want %d", media, tr.Bytes(), before)
		}
	}
}

func TestMediaAttrChangeRecharges(t *testing.T) {
	tr := mustParse(t, `<link rel="stylesheet" href="a.css"><p>x</p>`, Limits{})
	if err := tr.SetStyle("a.css", "h1{color:red}"); err != nil {
		t.Fatal(err)
	}
	check := func(step string) {
		t.Helper()
		if out := tr.Serialize(); len(out) > tr.Bytes() {
			t.Fatalf("%s: serialized %d > accounted %d", step, len(out), tr.Bytes())
		}
	}
	check("after SetStyle")
	link := firstLink(t, tr)
	base := tr.Bytes()
	media := "screen and (max-width: 600px)"
	if err := tr.SetAttr(link, "media", media); err != nil {
		t.Fatal(err)
	}
	check("after SetAttr media")
	if tr.Bytes() != base+attrCost("media", media)+10+len(media) {
		t.Fatalf("SetAttr media: Bytes()=%d want %d", tr.Bytes(), base+attrCost("media", media)+10+len(media))
	}
	if !strings.Contains(tr.Serialize(), "@media "+media+" {") {
		t.Fatal("wrapper not emitted after media change")
	}
	tr.RemoveAttr(link, "media")
	check("after RemoveAttr media")
	if tr.Bytes() != base {
		t.Fatalf("RemoveAttr media: Bytes()=%d want %d", tr.Bytes(), base)
	}
}

func TestSetStyleOverBudgetKeepsOld(t *testing.T) {
	tr := mustParse(t, `<link rel="stylesheet" href="a.css"><p>x</p>`, Limits{TotalBytes: 4096})
	if err := tr.SetStyle("a.css", "p{color:red}"); err != nil {
		t.Fatal(err)
	}
	err := tr.SetStyle("a.css", strings.Repeat("p{color:red}", 400))
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("want ErrLimit, got %v", err)
	}
	if !strings.Contains(tr.Serialize(), "<style>p{color:red}</style>") {
		t.Fatal("old text not kept after ErrLimit")
	}
	if out := tr.Serialize(); len(out) > tr.Bytes() {
		t.Fatalf("serialized %d > accounted %d", len(out), tr.Bytes())
	}
}

func TestSetStyleFollowsHrefChange(t *testing.T) {
	tr := mustParse(t, `<link rel="stylesheet" href="a.css"><p>x</p>`, Limits{})
	before := tr.Bytes()
	if err := tr.SetStyle("a.css", "h1{color:red}"); err != nil {
		t.Fatal(err)
	}
	link := firstLink(t, tr)
	if err := tr.SetAttr(link, "href", "b.css"); err != nil {
		t.Fatal(err)
	}
	out := tr.Serialize()
	if strings.Contains(out, "<style>") {
		t.Fatalf("old sheet still emitted:\n%s", out)
	}
	if got := tr.LinkedStyles(); strings.Join(got, ",") != "b.css" {
		t.Fatalf("LinkedStyles = %v", got)
	}
	// The href attribute itself changed size; account for that delta only.
	want := before - attrCost("href", "a.css") + attrCost("href", "b.css")
	if tr.Bytes() != want {
		t.Fatalf("Bytes()=%d want %d (orphan sheet must be credited at Serialize)", tr.Bytes(), want)
	}
}

func TestSerializeWithinBytesAfterStyles(t *testing.T) {
	fixtures := []string{
		`<head><link rel="stylesheet" href="a.css"><link rel="stylesheet" href="a.css"></head><body><p>x</p></body>`,
		`<link rel="stylesheet" href="a.css" media="print"><p>x</p>`,
		`<link rel="stylesheet" href="a.css" media="all"><p>x</p>`,
		`<link rel="stylesheet" href="a.css" media="print}</style><h1>x"><p>x</p>`,
		`<body><noscript><p>enable JS</p></noscript><p>ok</p></body>`,
		`<link rel="stylesheet" href="a.css" media="` + strings.Repeat("s", 1025) + `"><p>x</p>`,
	}
	for _, f := range fixtures {
		tr := mustParse(t, f, Limits{})
		if err := tr.SetStyle("a.css", "h1{color:red}</style>"); err != nil {
			t.Fatal(err)
		}
		if out := tr.Serialize(); len(out) > tr.Bytes() {
			t.Fatalf("%q: serialized %d > accounted %d", f[:40], len(out), tr.Bytes())
		}
	}
}

func TestSeparateHostStyleAllowance(t *testing.T) {
	tr := mustParse(t, `<link id="sheet" rel="stylesheet" href="a.css"><div id="target"></div>`, Limits{TotalBytes: 1024, ExternalStyleBytes: 4096})
	before := tr.Bytes()
	css := "/*" + strings.Repeat("x", 2048) + "*/div{color:red}"
	if err := tr.SetStyle("a.css", css); err != nil {
		t.Fatal(err)
	}
	if tr.Bytes() <= 1024 || tr.contentBytes() != before {
		t.Fatal("styles not separately charged")
	}
	if _, err := tr.NewText(strings.Repeat("x", 1024)); !errors.Is(err, ErrLimit) {
		t.Fatalf("script allocation escaped DOM cap: %v", err)
	}
	if err := tr.SetInnerHTML(tr.ByID("target"), "<b>ok</b>"); err != nil {
		t.Fatal(err)
	}
	if err := tr.SetInnerHTML(tr.ByID("target"), strings.Repeat("x", 1024)); !errors.Is(err, ErrLimit) {
		t.Fatalf("innerHTML escaped DOM cap: %v", err)
	}
	charged := tr.Bytes()
	if err := tr.SetStyle("a.css", strings.Repeat("x", 4096)); !errors.Is(err, ErrLimit) || tr.Bytes() != charged {
		t.Fatalf("style overflow changed accounting: %v", err)
	}
	if len(tr.Serialize()) > tr.Bytes() {
		t.Fatal("serialized bytes exceed accounting")
	}
	tr.ClearStyles()
	if tr.styleBytes != 0 || tr.Bytes() != tr.contentBytes() {
		t.Fatal("clear did not credit host bytes")
	}
	if err := tr.SetStyle("a.css", css); err != nil {
		t.Fatal(err)
	}
	tr.RemoveAttr(tr.ByID("sheet"), "href")
	tr.Serialize()
	if tr.styleBytes != 0 {
		t.Fatal("orphan stylesheet retains charge")
	}
}
