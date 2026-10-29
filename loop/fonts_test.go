package loop

import (
	"context"
	"net/url"
	"testing"
	"time"

	"nimbleterminal.dev/ntbrowser/netpolicy"
)

// The default build registers the CJK fallback face; -tags nocjk leaves it
// out, so CJK text draws as missing glyphs (cjkLinked, per build tag).
func TestCJKFallbackFace(t *testing.T) {
	origin, _ := url.Parse("https://example.com/cjk.html")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 64)
	l := New(ctx, Config{Width: 400, MinFrame: 5 * time.Millisecond}, sink)
	go l.Run()
	l.LoadPage(`<p>漢字かな한글 Tiếng ₹</p>`, origin.String(), netpolicy.New(netpolicy.Policy{Origin: origin}))
	want := 6 // 漢字かな한글
	if cjkLinked {
		want = 0
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("no frame")
		case m := <-sink:
			if f, ok := m.(FrameMsg); ok {
				if f.Err != nil {
					t.Fatal(f.Err)
				}
				if f.Result.MissingGlyphs != want {
					t.Fatalf("MissingGlyphs = %d, want %d (cjkLinked=%v)", f.Result.MissingGlyphs, want, cjkLinked)
				}
				return
			}
		}
	}
}
