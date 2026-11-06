// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package loop

import (
	"context"
	"os"
	"testing"
	"time"

	"nimbleterminal.dev/ntbrowser/cookies"
	"nimbleterminal.dev/ntbrowser/netpolicy"
	"nimbleterminal.dev/ntbrowser/profile"
)

// These tests use real HTML scripts and the loop, but no server or network.
// Navigation is observed at the UI boundary and the destination supplied by
// the harness; application navigation policy/HTTP transport have separate tests.
func TestCompatibilityFixtures(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "closed"
		if enabled {
			name = "session-flow"
		}
		t.Run(name, func(t *testing.T) {
			p, _ := profile.Builtin("browse")
			if enabled {
				p.Cookies = profile.CookiesSession
				p.SessionStorage = profile.SessionStorageOn
				p.DynamicCode = profile.DynamicOn
				p.ScriptNavigation = profile.ScriptNavOrigin
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			t.Cleanup(func() {
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("loop did not stop")
				}
			})
			sink := make(chanSink, 256)
			l := New(ctx, Config{Profile: p, Width: 400}, sink)
			go func() { defer close(done); l.Run() }()
			fetcher := netpolicy.New(netpolicy.Policy{Cookies: cookies.New()})
			load := func(file, url, marker string, navigation, painted bool) {
				t.Helper()
				html, err := os.ReadFile("testdata/compat/" + file + ".html")
				if err != nil {
					t.Fatal(err)
				}
				generation := l.LoadPage(string(html), url, fetcher)
				sawMarker, sawNavigation, sawPaint := false, !navigation, !painted
				deadline := time.After(5 * time.Second)
				for !sawMarker || !sawNavigation || !sawPaint {
					select {
					case <-deadline:
						t.Fatalf("%s: marker=%v navigation=%v paint=%v", file, sawMarker, sawNavigation, sawPaint)
					case msg := <-sink:
						switch m := msg.(type) {
						case LogMsg:
							if m.Level == "error" {
								t.Fatalf("%s: %s", file, m.Line)
							}
							if m.Line == "PASS:"+marker {
								sawMarker = true
							}
						case ScriptNavigateMsg:
							if !navigation || m.Source != url || m.Navigation != generation || m.Mode != "replace" || m.Href != "https://compat.test/next.html" {
								t.Fatalf("%s: unexpected navigation %+v", file, m)
							}
							sawNavigation = true
						case FrameMsg:
							if m.Err != nil {
								t.Fatal(m.Err)
							}
							if painted && m.Navigation == generation && m.Result != nil {
								for _, b := range m.Result.Boxes {
									if b.Text == "compat-ready" {
										sawPaint = true
									}
								}
							}
						}
					}
				}
			}
			load("encoding", "https://compat.test/encoding.html", "encoding", false, true)
			if !enabled {
				load("closed", "https://compat.test/", "closed", false, false)
				return
			}
			load("start", "https://compat.test/start.html", "start", true, false)
			load("next", "https://compat.test/next.html", "next", false, true)
			load("isolated", "https://other.test/", "isolated", false, false)
			load("next", "https://compat.test/next.html", "next", false, true)
		})
	}
}
