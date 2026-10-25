package dom

import (
	"context"
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
