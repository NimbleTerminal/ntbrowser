// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"context"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntpicture"

	"nimbleterminal.dev/ntbrowser/loop"
)

// Clear every terminal identity hint ntpicture recognizes, then set the
// requested ones, so results do not depend on the terminal running the tests.
func setTerminalEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, k := range []string{"TERM", "TERM_PROGRAM", "KITTY_WINDOW_ID", "KITTY_INSTALLATION_DIR", "GHOSTTY_RESOURCES_DIR", "WEZTERM_EXECUTABLE", "WEZTERM_PANE", "KONSOLE_VERSION", "TMUX", "NTPICTURE_KITTY", "NTPICTURE_TMUX_PASSTHROUGH"} {
		t.Setenv(k, "")
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
}

// collectRaw runs a command tree once, without feeding messages back, and
// returns the raw terminal output it produced.
func collectRaw(cmd tea.Cmd) string {
	var out strings.Builder
	var run func(tea.Cmd)
	run = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		msg := cmd()
		if v := reflect.ValueOf(msg); msg != nil && v.Kind() == reflect.Slice {
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
	}
	run(cmd)
	return out.String()
}

func startupModel(t *testing.T) *Model {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	l := loop.New(ctx, loop.Config{Profile: testLoopProfile, Width: 400}, dropSink{})
	go l.Run()
	m := New(l, testProfile(nil, false, NavFree), Options{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	return m
}

// Discovery must not wait for the first page: the Kitty query goes out with
// the program's Init, while the page is still loading.
func TestKittyProbeStartsAtInit(t *testing.T) {
	setTerminalEnv(t, map[string]string{"TERM": "xterm-256color"})
	m := startupModel(t)
	out := collectRaw(m.Init()) // not drive: the address bar's cursor blink re-arms forever
	if !strings.Contains(out, "\x1b_Ga=q") {
		t.Fatalf("Init sent no Kitty query: %q", out)
	}
}

// Every viewer and picture shares the session Init probed, so a page never
// re-discovers the terminal and the first frame already knows its mode.
func TestViewerUsesStartupSession(t *testing.T) {
	setTerminalEnv(t, map[string]string{"TERM": "xterm-256color"})
	m := startupModel(t)
	drive(m, m.session.SetKittyCapability(ntpicture.KittyCapabilitySupported))
	_, cmd := m.Update(cssFrame(t, m, loop.FrameMsg{Gen: 1, Result: renderTall(t, `<p>v1</p>`)}))
	out := drive(m, cmd)
	if m.viewer == nil || m.viewer.Picture().Session() != m.session {
		t.Fatal("the viewer does not use the startup terminal session")
	}
	if m.pictureOptions().Session != m.session {
		t.Fatal("media pictures do not use the startup terminal session")
	}
	if !strings.Contains(out, "\x1b_G") || m.viewer.Picture().Mode() != ntpicture.PictureKitty {
		t.Fatalf("first frame did not paint in Kitty mode (mode %v)", m.viewer.Picture().Mode())
	}
}

// In a terminal that identifies itself as Kitty-capable (rex, kitty, ghostty,
// …) the first frame waits for the probe reply, bounded by ntpicture's 250 ms
// timeout, rather than painting glyphs and switching a moment later.
func TestFirstFrameWaitsForProbeInKnownTerminal(t *testing.T) {
	setTerminalEnv(t, map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "rex"})
	m := startupModel(t)
	init := m.session.Init()
	_, cmd := m.Update(cssFrame(t, m, loop.FrameMsg{Gen: 1, Result: renderTall(t, `<p>v1</p>`)}))
	if m.viewer != nil {
		t.Fatal("first frame painted before the terminal answered")
	}
	_, cmd2 := m.Update(cssFrame(t, m, loop.FrameMsg{Gen: 2, Result: renderTall(t, `<p>v2</p>`)}))
	drive(m, tea.Batch(cmd, cmd2))
	if m.viewer != nil {
		t.Fatal("a second held frame painted before the terminal answered")
	}
	out := drive(m, m.session.SetKittyCapability(ntpicture.KittyCapabilitySupported))
	if m.viewer == nil {
		t.Fatal("the held frame was not painted after the reply")
	}
	if m.viewer.Picture().Mode() != ntpicture.PictureKitty || !strings.Contains(out, "\x1b_G") {
		t.Fatalf("held frame did not paint in Kitty mode (mode %v)", m.viewer.Picture().Mode())
	}
	if !strings.Contains(m.viewer.View(), "\U0010EEEE") {
		t.Fatal("held frame did not paint a Kitty placeholder grid")
	}
	_ = init
}

// An unknown terminal keeps the old behavior: paint at once (glyphs), and
// switch when and if the terminal answers.
func TestFirstFramePaintsAtOnceInUnknownTerminal(t *testing.T) {
	setTerminalEnv(t, map[string]string{"TERM": "xterm-256color"})
	m := startupModel(t)
	_, cmd := m.Update(cssFrame(t, m, loop.FrameMsg{Gen: 1, Result: renderTall(t, `<p>v1</p>`)}))
	drive(m, cmd)
	if m.viewer == nil {
		t.Fatal("first frame held in an unidentified terminal")
	}
}

// Shutdown closes the shared session after the pictures, releasing probes.
func TestQuitClosesTerminalSession(t *testing.T) {
	setTerminalEnv(t, map[string]string{"TERM": "xterm-256color"})
	m := startupModel(t)
	drive(m, m.quit())
	_, cmd := m.Update(shutdownReadyMsg{})
	if cmd == nil || m.session.Snapshot().Kitty != ntpicture.KittyCapabilityUnknown {
		t.Fatal("quit did not produce the final command")
	}
	if cmd := m.session.SetKittyCapability(ntpicture.KittyCapabilitySupported); cmd != nil {
		t.Fatal("session still open after shutdown")
	}
}
