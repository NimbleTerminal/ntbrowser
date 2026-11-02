// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"nimbleterminal.dev/ntbrowser/loop"
)

func hasTerminalControls(s string) bool {
	for _, r := range s {
		if r == '\n' {
			continue
		}
		if unsafeRune(r) {
			return true
		}
	}
	return false
}

// Page-controlled text (title, log lines, error strings, hrefs in the
// confirm prompt) never reaches the terminal with controls in it.
func TestViewNeverCarriesTerminalEscapes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := loop.New(ctx, loop.Config{Profile: testLoopProfile, Width: 400}, dropSink{})
	go l.Run()
	m := New(l, testProfile(nil, false, NavConfirm), "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	osc52 := "\x1b]52;c;SGVsbG8=\x07"
	m.Update(loop.TitleMsg{Title: "Owned" + osc52 + "\x9d title   line"})
	m.Update(loop.LogMsg{Level: "error", Line: "x\x1b[31mred"})
	m.Update(pageLoadedMsg{seq: m.navSeq, err: errors.New("parse \"" + osc52 + "\": bad")})
	for _, probe := range []string{m.title, m.status} {
		if hasTerminalControls(probe) {
			t.Fatalf("stored unsanitized: %q", probe)
		}
	}
	if out := m.View().Content; hasTerminalControls(out) || !strings.Contains(out, "Owned") {
		t.Fatalf("View carries controls or lost the title: %q", out)
	}
	m.current = mustURL(t, "https://example.test/")
	// A link whose href carries controls is refused by url.Parse; the error
	// quotes the input, and the quoted form must still be free of controls.
	m.Update(loop.NavigateMsg{Href: "https://evil.test/" + osc52})
	if hasTerminalControls(m.status) || !strings.HasPrefix(m.status, "bad link:") {
		t.Fatalf("bad-link status: %q", m.status)
	}
	// A parseable off-origin link is shown in the confirm prompt, cleaned.
	m.Update(loop.NavigateMsg{Href: "https://evil.test/\u0085path"})
	if hasTerminalControls(m.status) || !strings.Contains(m.status, "Open https://evil.test/") {
		t.Fatalf("confirm prompt: %q", m.status)
	}
}

// Back/Forward only move when the load succeeds, and a superseded load
// never lands.
func TestHistoryMovesOnlyOnSuccessAndLatestNavigationWins(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := loop.New(ctx, loop.Config{Profile: testLoopProfile, Width: 400}, dropSink{})
	go l.Run()
	m := New(l, testProfile(nil, false, NavFree), "")
	m.history, m.hIndex = []string{"https://a.test/", "https://b.test/"}, 1
	m.current = mustURL(t, "https://b.test/")

	// Back: the Cmd is built (sequence advances), then the load fails.
	if cmd := m.step(-1); cmd == nil {
		t.Fatal("no navigation command")
	}
	m.Update(pageLoadedMsg{seq: m.navSeq, hist: 0, err: errors.New("boom")})
	if m.hIndex != 1 || m.current.String() != "https://b.test/" {
		t.Fatalf("failed Back moved history: index %d current %s", m.hIndex, m.current)
	}
	// Back again, but a newer navigation (a link) starts before it lands.
	_ = m.step(-1)
	backSeq := m.navSeq
	_ = m.admit("https://c.test/", false)
	m.Update(pageLoadedMsg{seq: backSeq, hist: 0, html: "<p>a</p>", base: mustURL(t, "https://a.test/")})
	if m.hIndex != 1 || m.current.String() != "https://b.test/" {
		t.Fatalf("superseded Back landed: index %d current %s", m.hIndex, m.current)
	}
	m.Update(pageLoadedMsg{seq: m.navSeq, hist: pushNew, html: "<p>c</p>", base: mustURL(t, "https://c.test/")})
	if m.hIndex != 2 || len(m.history) != 3 || m.history[2] != "https://c.test/" {
		t.Fatalf("link did not push: %v @%d", m.history, m.hIndex)
	}
	// A successful Back lands and may record a redirected URL.
	_ = m.step(-1)
	m.Update(pageLoadedMsg{seq: m.navSeq, hist: 1, html: "<p>b</p>", base: mustURL(t, "https://www.b.test/")})
	if m.hIndex != 1 || m.history[1] != "https://www.b.test/" {
		t.Fatalf("Back did not land: %v @%d", m.history, m.hIndex)
	}
}

// The terminal size known before the first frame decides the first
// re-flow width; nothing stays at the 800 px default.
func TestFirstFrameReflowsAtTerminalWidth(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chan tea.Msg, 256)
	l := loop.New(ctx, loop.Config{Profile: testLoopProfile, Width: 800, MinFrame: time.Millisecond}, chanSink(sink))
	go l.Run()
	m := New(l, testProfile(nil, false, NavFree), "")
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	l.LoadPage(`<p>hello</p>`, "https://example.test/", nil)

	deadline := time.After(5 * time.Second)
	var first loop.FrameMsg
	for first.Result == nil {
		select {
		case <-deadline:
			t.Fatal("no first frame")
		case msg := <-sink:
			if f, ok := msg.(loop.FrameMsg); ok && f.Err == nil {
				first = f
			}
		}
	}
	if first.Result.Bounds().Dx() != 800 {
		t.Fatalf("first frame width %d", first.Result.Bounds().Dx())
	}
	m.Update(first) // creates the viewer at 200 columns and tells the loop the width
	for {
		select {
		case <-deadline:
			t.Fatal("no re-flow at the terminal width")
		case msg := <-sink:
			if f, ok := msg.(loop.FrameMsg); ok && f.Err == nil && f.Result.Bounds().Dx() == 2000 {
				return // 200 glyph-fallback columns × 10 px, nthtml's own rule
			}
		}
	}
}

type chanSink chan tea.Msg

func (c chanSink) Send(m tea.Msg) { c <- m }

// A log line must survive the frame that follows it; frames only replace the
// status when the renderer has warnings to show.
func TestFrameWithoutWarningsKeepsStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := loop.New(ctx, loop.Config{Profile: testLoopProfile, Width: 400}, dropSink{})
	go l.Run()
	m := New(l, testProfile(nil, false, NavFree), "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(loop.FrameMsg{Gen: 1, Result: renderTall(t, `<p>x</p>`)})
	m.Update(loop.LogMsg{Level: "log", Line: "hello from the page"})
	m.Update(loop.FrameMsg{Gen: 2, Result: renderTall(t, `<p>y</p>`)})
	if !strings.Contains(m.status, "hello from the page") {
		t.Fatalf("frame wiped the status line: %q", m.status)
	}
}

// The "loading …" status is replaced by the first frame of the new page;
// later frames without warnings leave the status alone.
func TestFirstFrameClearsLoadingStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := loop.New(ctx, loop.Config{Profile: testLoopProfile, Width: 400}, dropSink{})
	go l.Run()
	m := New(l, testProfile(nil, false, NavFree), "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(pageLoadedMsg{html: "<p>x</p>", base: mustURL(t, "https://example.test/"), seq: m.navSeq, hist: pushNew})
	if !strings.HasPrefix(m.status, "loading ") {
		t.Fatalf("status after load: %q", m.status)
	}
	m.Update(loop.FrameMsg{Gen: 1, Result: renderTall(t, `<p>x</p>`)})
	if strings.HasPrefix(m.status, "loading ") {
		t.Fatalf("first frame left the loading status: %q", m.status)
	}
}
