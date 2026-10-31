package main

import "testing"

func TestDecoderRefusesPathNames(t *testing.T) {
	d := newVideoDecoder(t.TempDir())
	defer d.Close()
	for _, name := range []string{"", ".", "..", "../x", "/etc/passwd", "a/b", `a\b`} {
		if err := d.Load(name); err == nil || err.Error() != "bad media name" {
			t.Errorf("Load(%q) = %v", name, err)
		}
	}
}
