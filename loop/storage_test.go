// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package loop

import (
	"context"
	"nimbleterminal.dev/ntbrowser/jsvm"
	"nimbleterminal.dev/ntbrowser/profile"
	"nimbleterminal.dev/ntbrowser/sessionstorage"
	"testing"
)

func TestSessionStorageNavigationAndGate(t *testing.T) {
	for _, on := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		p, _ := profile.Builtin("browse")
		if on {
			p.SessionStorage = profile.SessionStorageOn
		}
		sink := make(chanSink, 256)
		// The profile must override a low-level VM storage capability.
		l := New(ctx, Config{Profile: p, Width: 400, Script: jsvm.Config{SessionStorage: sessionstorage.New(0, 0).Open("https://bypass.test/")}}, sink)
		go l.Run()
		l.LoadPage(`<script>try{sessionStorage.setItem("key","saved");console.log("stored")}catch(e){console.log(e.name)}</script>`, "https://example.test/a", nil)
		if !on {
			waitLog(t, sink, "SecurityError")
			cancel()
			continue
		}
		waitLog(t, sink, "stored")
		for _, tc := range []struct{ url, want string }{
			{"https://example.test:443/b", "saved"},
			{"https://other.test/", "null"},
			{"http://example.test/", "null"},
			{"https://example.test:444/", "null"},
			{"https://example.test/a", "saved"},
		} {
			l.LoadPage(`<script>console.log("value:"+sessionStorage.getItem("key"))</script>`, tc.url, nil)
			waitLog(t, sink, "value:"+tc.want)
		}
		l.LoadPage(`<script>try{sessionStorage.getItem("key")}catch(e){console.log(e.name)}</script>`, "file:///tmp/local.html", nil)
		waitLog(t, sink, "SecurityError")
		cancel()
	}
}
