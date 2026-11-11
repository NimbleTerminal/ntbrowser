// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package loop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"nimbleterminal.dev/nthtml/document"
	"nimbleterminal.dev/nttypeset"
)

func chunkHistory(n int) string {
	var s strings.Builder
	s.WriteString("<body>")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&s, `<section id="message-%d"><p>Message %d</p><p><a href="/go/%d">open %d</a></p></section>`, i, i, i, i)
	}
	s.WriteString("</body>")
	return s.String()
}

func TestChunkRendererLazyNavigationAndResidency(t *testing.T) {
	ctx := context.Background()
	opts := document.Options{Width: 400, Fonts: document.MinimalFonts}
	doc, err := document.Parse(ctx, strings.NewReader(chunkHistory(1000)), opts.Limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = doc.Render(ctx, opts); !errors.Is(err, document.ErrLimit) {
		t.Fatalf("fixture must exceed whole-page raster budget: %v", err)
	}
	r, err := newChunkRender(ctx, doc, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.source.maps) != 0 {
		t.Fatal("index eagerly extracted chunks")
	}
	for _, index := range []int{0, 999, 400, 999} {
		res, info, err := r.render(ctx, chunkRequest{index: index}, opts)
		if err != nil {
			t.Fatal(err)
		}
		if info.Index != index || info.Count != 1000 {
			t.Fatalf("unexpected chunk: %+v", info)
		}
		text, err := res.TTY(ctx, 80)
		if err != nil || !strings.Contains(text, fmt.Sprintf("Message %d", index)) || strings.Contains(text, fmt.Sprintf("Message %d", (index+1)%1000)) {
			t.Fatalf("TTY leaked another chunk: %q %v", text, err)
		}
		if len(r.source.maps) > 2 {
			t.Fatalf("retained %d source maps", len(r.source.maps))
		}
		if _, ok := res.Anchors[fmt.Sprintf("message-%d", index)]; !ok {
			t.Fatal("lost source anchor")
		}
		if len(res.Links) == 0 || res.Links[0].Href != fmt.Sprintf("/go/%d", index) {
			t.Fatalf("lost link: %+v", res.Links)
		}
	}
	_, info, err := r.render(ctx, chunkRequest{anchor: "#message-700"}, opts)
	if err != nil || info.Index != 700 {
		t.Fatalf("fragment jump: %+v %v", info, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := r.render(cancelled, chunkRequest{index: 700}, opts); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	recovered, _, err := r.render(ctx, chunkRequest{index: 700}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := recovered.Anchors["message-700"]; !ok {
		t.Fatal("cancelled cache hit evicted source map")
	}
}

func TestChunkRendererOversizedUnitAndPixelBudget(t *testing.T) {
	ctx := context.Background()
	opts := document.Options{Width: 400, Fonts: document.MinimalFonts}
	html := `<body><p id="first">first</p><pre>` + strings.Repeat("x", 17000) + `</pre><p id="last">last</p></body>`
	doc, err := document.Parse(ctx, strings.NewReader(html), opts.Limits)
	if err != nil {
		t.Fatal(err)
	}
	r, err := newChunkRender(ctx, doc, opts)
	if err != nil {
		t.Fatal(err)
	}
	_, info, err := r.render(ctx, chunkRequest{index: 1}, opts)
	if !errors.Is(err, document.ErrLimit) || info.Count != 3 {
		t.Fatalf("oversized unit silently truncated: %+v %v", info, err)
	}
	res, _, err := r.render(ctx, chunkRequest{index: 2}, opts)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := res.TTY(ctx, 80)
	if !strings.Contains(text, "last") {
		t.Fatalf("last inaccessible: %q", text)
	}
	opts.Limits.Pixels = 100
	r, err = newChunkRender(ctx, doc, opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = r.render(ctx, chunkRequest{}, opts); !errors.Is(err, document.ErrLimit) && !errors.Is(err, nttypeset.ErrLimit) {
		t.Fatalf("pixel budget bypassed: %v", err)
	}
}

func nextChunkFrame(t *testing.T, sink chanSink) FrameMsg {
	t.Helper()
	timer := time.NewTimer(patience)
	defer timer.Stop()
	for {
		select {
		case msg := <-sink:
			if f, ok := msg.(FrameMsg); ok {
				return f
			}
		case <-timer.C:
			t.Fatal("no chunk frame")
			return FrameMsg{}
		}
	}
}

func TestChunkLoopMutationResizeNavigationAndStaleInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 256)
	l := New(ctx, Config{Chunked: true, Profile: testProfile, Width: 400, Fonts: document.MinimalFonts, MinFrame: time.Millisecond}, sink)
	go l.Run()
	l.LoadPage(chunkHistory(300), "https://example.test/", nil)
	first := nextChunkFrame(t, sink)
	if first.Err != nil {
		t.Fatal(first.Err)
	}
	l.MoveChunk(first.Gen, 0, 1)
	last := nextChunkFrame(t, sink)
	if last.Err != nil || last.Chunk.Index != 299 {
		t.Fatalf("last: %+v", last)
	}
	l.MoveChunk(first.Gen, 0, -1) // a stale UI frame must not change local coordinates
	done := make(chan struct{})
	l.post(func() {
		if l.page.chunkReq.index != 299 {
			t.Error("stale input moved the chunk")
		}
		close(done)
	})
	<-done
	l.post(func() {
		n := l.page.Tree.ByID("message-299")
		if err := l.page.Tree.SetText(n, "edited last"); err != nil {
			t.Error(err)
		}
	})
	edited := nextChunkFrame(t, sink)
	if edited.Err != nil {
		t.Fatal(edited.Err)
	}
	text, _ := edited.Result.TTY(ctx, 80)
	if edited.Chunk.Index != 299 || !strings.Contains(text, "edited last") {
		t.Fatalf("mutation lost position/content: %+v %q", edited.Chunk, text)
	}
	l.Resize(600)
	resized := nextChunkFrame(t, sink)
	if resized.Err != nil || resized.Result.Bounds().Dx() != 600 || resized.Chunk.Index != 299 {
		t.Fatalf("resize: %+v %v", resized.Chunk, resized.Err)
	}
	l.RevealChunk(resized.Gen, "#message-20")
	revealed := nextChunkFrame(t, sink)
	if revealed.Err != nil || revealed.Chunk.Index != 20 {
		t.Fatalf("reveal: %+v %v", revealed.Chunk, revealed.Err)
	}
	l.LoadPage("<p>new page</p>", "https://example.test/new", nil)
	fresh := nextChunkFrame(t, sink)
	if fresh.Err != nil || fresh.Chunk.Index != 0 || fresh.Chunk.Count != 1 {
		t.Fatalf("new page: %+v %v", fresh.Chunk, fresh.Err)
	}
}

func TestChunkFocusAndLinkIdentity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 256)
	l := New(ctx, Config{Chunked: true, Profile: testProfile, Width: 400, Fonts: document.MinimalFonts, MinFrame: time.Millisecond}, sink)
	go l.Run()
	var html strings.Builder
	html.WriteString("<body>")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&html, `<section id="s%d"><a id="a%d" href="/same">same</a></section>`, i, i)
	}
	html.WriteString(`<script>document.getElementById("a199").focus(); document.getElementById("a199").addEventListener("click",e=>{e.preventDefault();console.log("last link");});</script></body>`)
	l.LoadPage(html.String(), "https://example.test/", nil)
	f := nextChunkFrame(t, sink)
	if f.Err != nil || f.Chunk.Index != 199 {
		t.Fatalf("script focus did not reveal distant chunk: %+v %v", f.Chunk, f.Err)
	}
	if _, ok := f.Result.Anchors["a199"]; !ok {
		t.Fatalf("link source identity lost: %+v", f.Result.Anchors)
	}
	if len(f.Result.Links) == 0 {
		t.Fatal("no links")
	}
	l.LinkActivatedAt(f.Gen, f.Result.Links[0])
	timer := time.NewTimer(patience)
	defer timer.Stop()
	for {
		select {
		case msg := <-sink:
			switch v := msg.(type) {
			case NavigateMsg:
				t.Fatalf("wrong duplicate link activated: %s", v.Href)
			case LogMsg:
				if v.Line == "last link" {
					return
				}
			}
		case <-timer.C:
			t.Fatal("link event missing")
			return
		}
	}
}

func TestChunkInlineLinkHandler(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 64)
	l := New(ctx, Config{Chunked: true, Profile: testProfile, Width: 400, Fonts: document.MinimalFonts, MinFrame: time.Millisecond}, sink)
	go l.Run()
	l.LoadPage(`<p id="paragraph">before <a id="inline" href="/next">inline link</a> after</p><script>document.getElementById("inline").addEventListener("click", e=>{e.preventDefault();console.log("inline prevented");});</script>`, "https://example.test/", nil)
	f := nextChunkFrame(t, sink)
	if f.Err != nil {
		t.Fatal(f.Err)
	}
	if len(f.Result.Links) == 0 {
		t.Fatal("missing inline link")
	}
	l.LinkActivatedAt(f.Gen, f.Result.Links[0])
	timer := time.NewTimer(patience)
	defer timer.Stop()
	for {
		select {
		case msg := <-sink:
			switch v := msg.(type) {
			case NavigateMsg:
				t.Fatal("inline listener was bypassed")
				return
			case LogMsg:
				if v.Line == "inline prevented" {
					return
				}
			}
		case <-timer.C:
			t.Fatal("no inline click")
			return
		}
	}
}
