// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"context"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"nimbleterminal.dev/ntbrowser/loop"
)

// drive runs a Cmd the way Bubble Tea would, feeding every message back into
// the model until nothing is left, and returns the raw terminal output
// (Kitty APCs travel as tea.RawMsg strings).
func drive(m *Model, cmd tea.Cmd) string {
	var out strings.Builder
	var run func(tea.Cmd)
	run = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		msg := cmd()
		if msg == nil {
			return
		}
		if v := reflect.ValueOf(msg); v.Kind() == reflect.Slice {
			for i := 0; i < v.Len(); i++ {
				run(v.Index(i).Interface().(tea.Cmd))
			}
			return
		}
		if raw, ok := msg.(tea.RawMsg); ok {
			if s, ok := raw.Msg.(string); ok {
				out.WriteString(s)
			}
		}
		_, next := m.Update(msg)
		run(next)
	}
	run(cmd)
	return out.String()
}

// A re-flow must not repaint the placeholder grid, and frames go out as
// zlib-compressed pixels rather than PNG (perf analysis 2026-10-27: every
// frame with a new Kitty image id forces Bubble Tea to redraw all cells and
// the terminal to decode a PNG; HN scrolled at ~0.6 MB per keypress).
func TestViewerKeepsKittyIDAndSendsZlib(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := loop.New(ctx, loop.Config{Profile: testLoopProfile, Width: 400}, dropSink{})
	go l.Run()
	m := New(l, testProfile(nil, false, NavFree), "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	_, cmd := m.Update(loop.FrameMsg{Gen: 1, Result: renderTall(t, `<p>v1</p>`)})
	drive(m, cmd)
	// A Kitty-capable terminal with 1×1 px cells: tiny frames, same code path.
	drive(m, m.viewer.Picture().SetTerminal(true, 1, 1))

	_, cmd = m.Update(loop.FrameMsg{Gen: 2, Result: renderTall(t, `<p>v2</p>`)})
	out := drive(m, cmd)
	view := m.viewer.View()
	if !strings.Contains(view, "\U0010EEEE") {
		t.Fatalf("expected a Kitty placeholder grid, got %q", view)
	}
	if !strings.Contains(out, "\x1b_G") {
		t.Fatal("no Kitty transmission observed")
	}
	if !strings.Contains(out, "o=z") {
		apc := out[strings.Index(out, "\x1b_G"):]
		t.Fatalf("frame not zlib-compressed: %q", apc[:min(80, len(apc))])
	}

	_, cmd = m.Update(loop.FrameMsg{Gen: 3, Result: renderTall(t, `<p>v3 (re-flow)</p>`)})
	drive(m, cmd)
	if m.viewer.View() != view {
		t.Fatal("re-flow changed the placeholder grid: the Kitty image id is not stable")
	}
}
