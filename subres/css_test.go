package subres

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"nimbleterminal.dev/nthtml/document"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCountRulesAgreesWithNthtml(t *testing.T) {
	cases := map[string]string{
		"hn.css":      fixture(t, "hn.css"),
		"atrules.css": fixture(t, "atrules.css"),
		"imports.css": fixture(t, "imports.css"),
		"plain":       "p{color:red} h1,h2 , h3{margin:0}",
		"media-ws":    "@media\n\t(min-width: 1px){a{color:red} b{color:blue}}",
		"media-print": "@media print{a,b{}} c{}",
		"supports":    "@supports (display:grid){a{}} b{}",
		"strings":     "a[title=\"x,y\"]{} b:not(.c,.d){} /* , } */ e{}",
		"nested":      "@media screen{@media (min-width:1px){a{} b{}} c{}} d{}",
		// nthtml's prelude scanner stops only at ';' or '{', never at '}'
		"prelude-brace":  "@media x}y {\na{} b{} c{}\n}\np{}",
		"fontface-brace": "@font-face{src:url(x)} a{} @media a}b{c{}d{}e{}}",
		// a string does not end at a newline
		"string-newline": "a{content:\"\\n\"} b{} c{}",
	}
	ctx := context.Background()
	for name, css := range cases {
		n := CountRules(css)
		doc := "<style>" + css + "</style><p>x</p>"
		if _, err := document.Parse(ctx, strings.NewReader(doc), document.Limits{Rules: n}); err != nil {
			t.Fatalf("%s: nthtml needs more than CountRules=%d: %v", name, n, err)
		}
		if n <= 1 {
			continue // Limits{Rules: 0} means nthtml's default, so a budget of n-1 cannot be expressed for n == 1
		}
		if _, err := document.Parse(ctx, strings.NewReader(doc), document.Limits{Rules: n - 1}); !errors.Is(err, document.ErrLimit) {
			t.Fatalf("%s: nthtml accepted Rules=%d, CountRules=%d over-counts: %v", name, n-1, n, err)
		}
	}
}

func TestCountRulesHN(t *testing.T) {
	if n := CountRules(fixture(t, "hn.css")); n != 124 {
		t.Fatalf("CountRules(hn.css) = %d, want 124", n)
	}
}

func TestImportsFixture(t *testing.T) {
	stmts, rest := Imports(fixture(t, "imports.css"))
	want := []Import{
		{URL: "a.css"},
		{URL: "b.css", Media: "screen and (max-width: 400px)"},
		{URL: "c.css", Unsupported: true},
		{URL: "d.css", Unsupported: true},
	}
	if len(stmts) != len(want) {
		t.Fatalf("Imports = %+v", stmts)
	}
	for i := range want {
		if stmts[i].URL != want[i].URL || stmts[i].Media != want[i].Media || stmts[i].Unsupported != want[i].Unsupported {
			t.Fatalf("stmt %d = %+v, want %+v", i, stmts[i], want[i])
		}
	}
	if !strings.HasPrefix(rest, ".x{color:red}") || !strings.Contains(rest, `@import "late.css";`) {
		t.Fatalf("rest = %q", rest)
	}
}

func TestImportsSkipBOMAndCharset(t *testing.T) {
	css := "\uFEFF@charset \"utf-8\";@import url(a.css);p{}"
	stmts, rest := Imports(css)
	if len(stmts) != 1 || stmts[0].URL != "a.css" || rest != "p{}" {
		t.Fatalf("Imports = %+v rest=%q", stmts, rest)
	}
	if n := CountRules(css); n != 1 {
		t.Fatalf("CountRules = %d, want 1", n)
	}
}

func TestAbsolutizeBasic(t *testing.T) {
	const sheet = "https://cdn.test/css/t.css"
	cases := map[string]string{
		"a{background:url(arrow.png)}":                `a{background:url("https://cdn.test/css/arrow.png")}`,
		"a{background:url('../img/a.png')}":           `a{background:url("https://cdn.test/img/a.png")}`,
		"a{background:url(/root.png)}":                `a{background:url("https://cdn.test/root.png")}`,
		"a{background:url(data:image/png;base64,AA)}": "a{background:url(data:image/png;base64,AA)}",
		"a{background:url(https://x.test/a.png)}":     "a{background:url(https://x.test/a.png)}",
		"a{mask:url(#frag)}":                          "a{mask:url(#frag)}",
	}
	for in, want := range cases {
		if got := Absolutize(in, sheet); got != want {
			t.Fatalf("Absolutize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAbsolutizeIgnoresCommentsAndStrings(t *testing.T) {
	in := "/* url(x.png) */ a{content:\"url(y.png)\"; background:url( \"a b.png\" )}"
	want := "/* url(x.png) */ a{content:\"url(y.png)\"; background:url(\"https://cdn.test/css/a%20b.png\")}"
	if got := Absolutize(in, "https://cdn.test/css/t.css"); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestAbsolutizeBadSheetURL(t *testing.T) {
	in := "a{background:url(arrow.png)}"
	if got := Absolutize(in, "::"); got != in {
		t.Fatalf("got %q", got)
	}
}
