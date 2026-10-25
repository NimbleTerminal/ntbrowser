package app

import (
	"context"
	"net/url"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/nthtml/document"

	"nimbleterminal.dev/ntbrowser/loop"
	"nimbleterminal.dev/ntbrowser/netpolicy"
)

type dropSink struct{}

func (dropSink) Send(tea.Msg) {}

func renderTall(t *testing.T, body string) *document.Result {
	t.Helper()
	res, err := document.Render(context.Background(), strings.NewReader(
		`<!doctype html><html><body>`+body+`<div style="height:3000px"></div></body></html>`), document.Options{Width: 400})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// A re-flow of the same page keeps the reader's scroll offset; the first
// frame of a new page starts at the top. Both go through the same FrameMsg
// path, distinguished only by pageLoadedMsg having arrived in between.
func TestReflowKeepsScrollAndNewPageResetsIt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := loop.New(ctx, loop.Config{Width: 400}, dropSink{})
	go l.Run()
	m := New(l, netpolicy.Policy{}, NavFree, "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})

	first := renderTall(t, `<p>v1</p>`)
	m.Update(loop.FrameMsg{Gen: 1, Result: first})
	if m.viewer == nil {
		t.Fatal("no viewer after first frame")
	}
	_ = m.viewer.Pan(0, 900)
	if y := m.viewer.Picture().Crop().Min.Y; y != 900 {
		t.Fatalf("pan did not take: crop y=%d", y)
	}

	m.Update(loop.FrameMsg{Gen: 2, Result: renderTall(t, `<p>v2 (script mutated the page)</p>`)})
	if y := m.viewer.Picture().Crop().Min.Y; y != 900 {
		t.Fatalf("re-flow lost the scroll offset: crop y=%d", y)
	}
	if m.viewer.Result().Document() == first.Document() {
		t.Fatal("viewer still shows the old Result")
	}

	// A stale frame (older generation) never regresses the view.
	m.Update(loop.FrameMsg{Gen: 1, Result: first})
	if m.viewer.Result().Document() == first.Document() {
		t.Fatal("stale frame was applied")
	}

	// New page: pageLoadedMsg precedes its first frame.
	m.Update(pageLoadedMsg{html: "<p>next</p>", base: mustURL(t, "https://example.test/next"), seq: m.navSeq, hist: pushNew})
	m.Update(loop.FrameMsg{Gen: 3, Result: renderTall(t, `<p>next page</p>`)})
	if y := m.viewer.Picture().Crop().Min.Y; y != 0 {
		t.Fatalf("new page did not start at the top: crop y=%d", y)
	}
	if m.hIndex != 0 || len(m.history) != 1 {
		t.Fatalf("history not pushed: %v @%d", m.history, m.hIndex)
	}
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
