package main

import (
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"nimbleterminal.dev/ntbrowser/mediaproc"
	"nimbleterminal.dev/ntbrowser/profile"
	"nimbleterminal.dev/ntbrowser/sandbox"
)

// TestRealHelperDecodes runs the real binary under its real sandbox and
// decodes a real file. Opt-in, like ntvideo's libmpv tests.
func TestRealHelperDecodes(t *testing.T) {
	source := os.Getenv("NTVIDEO_TEST_FILE")
	if source == "" {
		t.Skip("set NTVIDEO_TEST_FILE")
	}
	bin := filepath.Join(t.TempDir(), "ntbrowser-media")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	bin, _ = filepath.EvalSymlinks(bin)
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "clip"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	p, _ := profile.Builtin("browse")
	h, err := sandbox.StartHelper(p.WithVideo(), bin, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	frames := make(chan *image.RGBA, 4)
	statuses := make(chan mediaproc.Status, 64)
	c := mediaproc.NewClient(h.Conn, func(s mediaproc.Status) {
		select {
		case statuses <- s:
		default:
		}
	}, func(img *image.RGBA) {
		select {
		case frames <- img:
		default:
		}
	})
	for _, err := range []error{c.Handshake("video"), c.Resize(320, 240), c.Open("clip"), c.Play()} {
		if err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case img := <-frames:
			t.Logf("frame %v", img.Bounds())
			return
		case s := <-statuses:
			t.Logf("status %+v", s)
			if s.State == mediaproc.Failed {
				t.Fatalf("failed: %s", s.Err)
			}
		case <-c.Dead():
			t.Fatal("helper died")
		case <-deadline:
			t.Fatal("no frame within 10s")
		}
	}
}
