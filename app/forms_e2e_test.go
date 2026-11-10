// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"nimbleterminal.dev/ntbrowser/loop"
	"nimbleterminal.dev/nthtml"
)

// TestSearchFormEndToEnd drives the whole path a reader takes: load a page
// with a GET search form, Tab to the field (edit mode), type, press Enter,
// and land on the results page, which echoes the query it received.
func TestSearchFormEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/":
			w.Write([]byte(`<!doctype html><title>Search</title><form action="/search"><input name=q placeholder=Query><button>Go</button></form>`))
		case "/search":
			w.Write([]byte(`<!doctype html><title>Results</title><p id=out>got ` + r.URL.RawQuery + `</p>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 256)
	l := loop.New(ctx, loop.Config{Profile: testLoopProfile, Width: 400, MinFrame: 5 * time.Millisecond}, sink)
	go l.Run()
	m := New(l, testProfile(nil, true, NavFree), srv.URL+"/")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	drive(m, m.Init())

	// pump feeds loop messages to the model until done says stop. A
	// NavigateMsg's command (the page fetch) is run here, the way Bubble Tea
	// would, and its pageLoadedMsg fed back.
	pump := func(what string, done func(tea.Msg) bool) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case <-deadline:
				t.Fatalf("timed out waiting for %s (status %q, mode %v)", what, m.status.text, m.mode)
			case msg := <-sink:
				_, cmd := m.Update(msg)
				if _, nav := msg.(loop.NavigateMsg); nav && cmd != nil {
					loaded := cmd()
					if pl, ok := loaded.(pageLoadedMsg); ok && pl.err != nil {
						t.Fatalf("load: %v", pl.err)
					}
					m.Update(loaded)
				}
				switch msg.(type) {
				case loop.FrameMsg, nthtml.PaintReady:
					drive(m, cmd)
				}
				if done(msg) {
					return
				}
			}
		}
	}
	isFrame := func(msg tea.Msg) bool {
		f, ok := msg.(loop.FrameMsg)
		return ok && f.Result != nil && f.Result.Bounds().Dx() == m.documentWidth() && m.frameInputReady()
	}
	pump("first frame", isFrame)

	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	pump("focus", func(msg tea.Msg) bool { _, ok := msg.(loop.FocusMsg); return ok })
	if m.mode != modeEdit || m.focus.Control.Label != "Query" {
		t.Fatalf("Tab did not open the editor: mode %v focus %+v", m.mode, m.focus)
	}
	typeText(m, "hello world")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	pump("results page", func(msg tea.Msg) bool {
		f, ok := msg.(loop.FrameMsg)
		if !ok || f.Result == nil {
			return false
		}
		text, _ := f.Result.TTY(context.Background(), 60)
		return strings.Contains(text, "got q=hello+world")
	})
	if m.current == nil || m.current.Path != "/search" || m.current.RawQuery != "q=hello+world" || m.mode != modeBrowse {
		t.Fatalf("current %v mode %v", m.current, m.mode)
	}
}
