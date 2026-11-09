// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"context"
	"image/color"
	"testing"

	tea "charm.land/bubbletea/v2"

	"nimbleterminal.dev/ntbrowser/loop"
)

// In auto mode the terminal's reported background selects the page's color
// scheme; an explicit scheme ignores the report.
func TestTerminalBackgroundSelectsColorScheme(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := loop.New(ctx, loop.Config{Profile: testLoopProfile, Width: 400}, dropSink{})
	m := New(l, testProfile(nil, false, NavFree), "")
	m.Update(tea.BackgroundColorMsg{Color: color.Black})
	if l.ColorScheme() != "light" {
		t.Fatal("background changed the scheme without auto mode")
	}
	m.SetColorSchemeAuto(true)
	m.Update(tea.BackgroundColorMsg{Color: color.Black})
	if l.ColorScheme() != "dark" {
		t.Fatalf("dark terminal gave scheme %q", l.ColorScheme())
	}
	m.Update(tea.BackgroundColorMsg{Color: color.White})
	if l.ColorScheme() != "light" {
		t.Fatalf("light terminal gave scheme %q", l.ColorScheme())
	}
}
