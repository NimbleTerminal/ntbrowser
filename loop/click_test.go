package loop

import (
	"context"
	"image"
	"testing"
	"time"
)

const clickPage = `<!doctype html><html><body>
<div id="card" style="padding:8px"><p>Card body text</p></div>
<div style="padding:8px"><p>A div with a listener but no id</p></div>
<a href="/go">go</a>
<script>
  document.getElementById("card").addEventListener("click", e => console.log("card click", e.target.id));
  document.querySelectorAll("div")[1].addEventListener("click", e => { console.log("anon click", e.target.tagName); });
</script></body></html>`

// Element-level clicks: a document-pixel area is mapped to the smallest
// anchored box, which the serializer guarantees for listener-bearing
// elements (synthetic id), then dispatched with bubbling.
func TestClickAtHitsElementsAndMintedAnchors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 64)
	l := New(ctx, Config{Width: 400, MinFrame: 5 * time.Millisecond}, sink)
	go l.Run()
	l.LoadPage(clickPage, "https://example.test/", nil)

	var frame FrameMsg
	deadline := time.After(5 * time.Second)
	for frame.Result == nil {
		select {
		case <-deadline:
			t.Fatal("no frame")
		case m := <-sink:
			if f, ok := m.(FrameMsg); ok && f.Err == nil {
				frame = f
			}
		}
	}
	var card, anon image.Rectangle
	for _, b := range frame.Result.Boxes {
		switch {
		case b.Anchor == "card":
			card = b.Bounds
		case len(b.Anchor) > 5 && b.Anchor[:5] == "nt-h-":
			anon = b.Bounds
		}
	}
	if card.Empty() || anon.Empty() {
		t.Fatalf("anchored boxes missing: card=%v anon=%v", card, anon)
	}
	// Click inside the card's paragraph: the <p> has no anchor, the card does.
	l.ClickAt(image.Rect(card.Min.X+10, card.Min.Y+10, card.Min.X+11, card.Min.Y+11))
	l.ClickAt(image.Rect(anon.Min.X+10, anon.Min.Y+10, anon.Min.X+11, anon.Min.Y+11))
	want := map[string]bool{"card click card": false, "anon click DIV": false}
	deadline = time.After(3 * time.Second)
	for {
		done := true
		for _, ok := range want {
			done = done && ok
		}
		if done {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("missing click logs: %v", want)
		case m := <-sink:
			if lm, ok := m.(LogMsg); ok {
				if _, exp := want[lm.Line]; exp {
					want[lm.Line] = true
				} else if lm.Level == "error" {
					t.Fatalf("page error: %s", lm.Line)
				}
			}
		}
	}
}
