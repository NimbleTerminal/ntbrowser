// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package dom

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"nimbleterminal.dev/nthtml/document"
)

// Real pages carry invalid UTF-8 and NUL bytes; the parser must not panic
// and the serializer must still produce text nthtml accepts.
func TestParseSurvivesInvalidUTF8AndNUL(t *testing.T) {
	tr, err := Parse(strings.NewReader("<p>a\xff\xfe\x00b</p><title>t\xc3</title>"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	out := tr.Serialize()
	if !utf8.ValidString(out) || strings.ContainsRune(out, 0) {
		t.Fatalf("serializer emitted invalid UTF-8 or NUL: %q", out)
	}
	if _, err := document.Parse(context.Background(), strings.NewReader(out), document.DefaultLimits); err != nil {
		t.Fatalf("nthtml rejected serialized output: %v", err)
	}
}

// Goja cannot execute ES modules; module scripts are not collected.
func TestModuleScriptsAreSkipped(t *testing.T) {
	tr, _ := Parse(strings.NewReader(`<script type="module">import x from "y"</script><script>1</script>`), Limits{})
	if len(tr.Scripts) != 1 || tr.Scripts[0].Inline != "1" {
		t.Fatalf("scripts: %+v", tr.Scripts)
	}
}

// Framework templates (Vue, Alpine, Angular), i18n custom elements and
// attribute-heavy markup are ordinary pages: non-conforming names and
// excess attributes are dropped, never the whole page.
func TestParseToleratesFrameworkMarkup(t *testing.T) {
	many := ""
	for i := 0; i < 40; i++ {
		many += fmt.Sprintf(` data-a%d="%d"`, i, i)
	}
	page := `<body>
<div id="v" @click="go()" :class="c" v-if="ok" [hidden]="x" (input)="h($event)" #ref>vue</div>
<my-元素 id="cx">custom</my-元素>
<p id="p"` + many + `>heavy</p>
</body>`
	tr, err := Parse(strings.NewReader(page), Limits{})
	if err != nil {
		t.Fatalf("page rejected: %v", err)
	}
	v := tr.ByID("v")
	if v == nil || TextContent(v) != "vue" {
		t.Fatalf("vue element lost: %v", v)
	}
	if _, ok := v.Attr("v-if"); !ok {
		t.Fatal("a valid hyphenated attribute was dropped")
	}
	if _, ok := v.Attr("@click"); ok {
		t.Fatal("an invalid attribute name was kept")
	}
	if cx := tr.ByID("cx"); cx != nil && cx.Tag() == "my-元素" {
		t.Fatal("an invalid tag name was kept")
	}
	if !strings.Contains(tr.Serialize(), "custom") {
		t.Fatal("the invalid element's text was lost")
	}
	p := tr.ByID("p")
	if p == nil || TextContent(p) != "heavy" {
		t.Fatal("attribute-heavy element lost")
	}
	if _, ok := p.Attr("data-a30"); !ok { // id + data-a0..a30 = 32 = the cap
		t.Fatal("attributes under the cap were dropped")
	}
	if _, ok := p.Attr("data-a31"); ok {
		t.Fatal("attributes over the cap were kept")
	}
}
