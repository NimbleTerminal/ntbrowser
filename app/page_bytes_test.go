// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"nimbleterminal.dev/ntbrowser/dom"
	"nimbleterminal.dev/ntbrowser/loop"
	"nimbleterminal.dev/ntbrowser/netpolicy"
)

// runCmd executes a command tree and returns the first pageLoadedMsg it yields.
func runCmd(cmd tea.Cmd) (pageLoadedMsg, bool) {
	if cmd == nil {
		return pageLoadedMsg{}, false
	}
	msg := cmd()
	if v := reflect.ValueOf(msg); msg != nil && v.Kind() == reflect.Slice {
		for i := 0; i < v.Len(); i++ {
			if m, ok := runCmd(v.Index(i).Interface().(tea.Cmd)); ok {
				return m, true
			}
		}
		return pageLoadedMsg{}, false
	}
	m, ok := msg.(pageLoadedMsg)
	return m, ok
}

// Options.PageBytes raises the navigation body cap: a 1.5 MiB document loads under
// a 2 MiB budget and is refused under the 1 MiB default.
func TestPageBytesRaisesNavigationCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<p>" + strings.Repeat("x", 3<<19) + "</p>"))
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := loop.New(ctx, loop.Config{Profile: testLoopProfile, Width: 400, DOMLimits: dom.Limits{TotalBytes: 2 << 20}}, dropSink{})
	go l.Run()
	m := New(l, testProfile(nil, true, NavFree), Options{})
	m.pageBytes = 2 << 20
	loaded, ok := runCmd(m.navigate(srv.URL+"/", pushNew, reachAny))
	if !ok || loaded.err != nil || len(loaded.html) < 3<<19 {
		t.Fatalf("1.5 MiB page under a 2 MiB budget: ok=%v err=%v len=%d", ok, loaded.err, len(loaded.html))
	}
	m.pageBytes = 0
	loaded, ok = runCmd(m.navigate(srv.URL+"/", pushNew, reachAny))
	if !ok || !errors.Is(loaded.err, netpolicy.ErrTooLarge) {
		t.Fatalf("default cap: ok=%v err=%v", ok, loaded.err)
	}
}
