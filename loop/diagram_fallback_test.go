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
)

func TestDiagramBudgetClassification(t *testing.T) {
	for _, resource := range []string{"diagram count", "diagram pixels", "bytes", "nodes", "height", "SVG bytes"} {
		err := fmt.Errorf("%w: %s", document.ErrLimit, resource)
		want := strings.HasPrefix(resource, "diagram ")
		if diagramBudgetExceeded(err) != want {
			t.Fatal(resource)
		}
	}
	if diagramBudgetExceeded(errors.New("other: diagram count")) {
		t.Fatal("unrelated error accepted")
	}
}

// diagramPage holds count full-size SVGs (a viewBox without a declared size
// is a diagram, not an icon, so each one spends the renderer's diagram count).
func diagramPage(count int) string {
	return `<style>svg{width:12px;height:12px}h1{color:#ff0000}</style><h1>Repository</h1>` +
		strings.Repeat(`<svg aria-label="icon" viewBox="0 0 12 12"><path d="M0 0H12V12H0Z"/></svg>`, count) +
		`<a href="/after">Readable after icons</a>`
}
func TestDiagramBudgetFallbackKeepsPageAndResetsOnNavigation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 512)
	l := New(ctx, Config{Profile: testProfile, Width: 400, MinFrame: time.Millisecond, Fonts: document.MinimalFonts}, sink)
	go l.Run()
	l.LoadPage(diagramPage(242), "https://example.test/icons", nil)
	f := nextFrame(t, sink)
	if f.Err != nil {
		t.Fatal(f.Err)
	}
	text, _ := f.Result.TTY(ctx, 80)
	if !strings.Contains(text, "Readable after icons") || !strings.Contains(strings.Join(f.Warnings, ";"), diagramFallbackWarning) {
		t.Fatal("page/notice missing", text, f.Warnings)
	}
	if !isRed(boxColor(f.Result, "h1")) {
		t.Fatal("diagram fallback discarded CSS")
	}
	l.Resize(350)
	resized := frameWhere(t, sink, 5*time.Second, func(f FrameMsg) bool { return f.Result != nil && f.Result.Bounds().Dx() == 350 })
	if resized.Err != nil || !strings.Contains(strings.Join(resized.Warnings, ";"), diagramFallbackWarning) {
		t.Fatal("fallback lost on resize", resized.Err)
	}
	done := make(chan bool, 1)
	l.post(func() { done <- l.page.diagramsDisabled })
	if !<-done {
		t.Fatal("fallback not retained")
	}
	l.LoadPage("<p>New page</p>", "https://example.test/new", nil)
	pageState(t, l)
	l.post(func() { done <- l.page.diagramsDisabled })
	if <-done {
		t.Fatal("fallback leaked to next document")
	}
}

func TestChunkedDiagramPageRemainsReadable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 512)
	l := New(ctx, Config{Profile: testProfile, Width: 400, Chunked: true, MinFrame: time.Millisecond, Fonts: document.MinimalFonts}, sink)
	go l.Run()
	l.LoadPage(diagramPage(40), "https://example.test/icons", nil)
	f := nextFrame(t, sink)
	if f.Err != nil {
		t.Fatal(f.Err)
	}
	if f.Chunk.Count > 1 {
		l.MoveChunk(f.Gen, 0, 1)
		f = frameWhere(t, sink, 5*time.Second, func(f FrameMsg) bool { return f.Result != nil && f.Chunk.Index == f.Chunk.Count-1 })
	}
	text, _ := f.Result.TTY(ctx, 80)
	if f.Err != nil || !strings.Contains(text, "Readable after icons") {
		t.Fatal(f.Err, text)
	}
}
