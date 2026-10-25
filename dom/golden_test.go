package dom

import (
	"bytes"
	"flag"
	"os"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

// The serializer's output for a representative page is pinned byte for
// byte: scripts stripped, "</" in style escaped, attributes escaped.
func TestSerializeGolden(t *testing.T) {
	in, err := os.ReadFile("testdata/golden/basic.html")
	if err != nil {
		t.Fatal(err)
	}
	tr, err := Parse(bytes.NewReader(in), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	got := tr.Serialize()
	if *update {
		if err := os.WriteFile("testdata/golden/basic.out.html", []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile("testdata/golden/basic.out.html")
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("golden mismatch:\n%s", got)
	}
}
