// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import (
	"fmt"
	"strings"
	"testing"
)

func TestLocationInspectionAndClosedGate(t *testing.T) {
	s := dynamicSandbox(t, false)
	s.cfg.Location = "https://example.test:8443/a?x=1#old"
	if got := evalString(t, s, `[location.href,location.origin,location.protocol,location.host,location.hostname,location.port,location.pathname,location.search,location.hash,String(location),document.location===window.location,location instanceof Location].join("|")`); got != "https://example.test:8443/a?x=1#old|https://example.test:8443|https:|example.test:8443|example.test|8443|/a|?x=1|#old|https://example.test:8443/a?x=1#old|true|true" {
		t.Fatal(got)
	}
	for _, expr := range []string{`location.assign("/next")`, `location.replace("/next")`, `location.reload()`, `location.href="/next"`, `window.location="/next"`, `document.location="/next"`, `location.hash="new"`} {
		if got := evalString(t, s, `(()=>{try{`+expr+`;return "allowed"}catch(e){return e.name}})()`); got != "SecurityError" {
			t.Fatal(expr, got)
		}
	}
	if got := evalString(t, s, `location.href`); got != s.cfg.Location {
		t.Fatal("blocked write changed URL")
	}
}
func TestLocationRequestsAndUnforgeability(t *testing.T) {
	s := dynamicSandbox(t, false)
	s.cfg.Location = "https://example.test/a?x=1#old"
	var requests []string
	s.cfg.Navigate = func(href, mode string) error { requests = append(requests, mode+":"+href); return nil }
	for _, tc := range []struct{ src, want string }{
		{`location.assign("../next")`, "assign:https://example.test/next"},
		{`location.replace("?q=hello world")`, "replace:https://example.test/a?q=hello%20world"},
		{`location.reload()`, "reload:https://example.test/a?x=1#old"},
		{`top.parent.frames.location.replace("/next")`, "replace:https://example.test/next"},
		{`location.pathname="/two"`, "assign:https://example.test/two?x=1#old"},
		{`location.hash="new"`, "assign:https://example.test/a?x=1#new"},
		{`document.location="/doc"`, "assign:https://example.test/doc"},
	} {
		imageEval(t, s, tc.src)
		if requests[len(requests)-1] != tc.want {
			t.Fatal(requests)
		}
	}
	n := len(requests)
	for _, raw := range []string{"javascript:1", "file:///etc/passwd", "data:text/html,hi", "https://user:pass@example.test/", strings.Repeat("x", 2049)} {
		if err := s.Eval("invalid.js", fmt.Sprintf("location.assign(%q)", raw)); err == nil {
			t.Fatal(raw)
		}
	}
	if len(requests) != n {
		t.Fatal("unsafe destination reached host")
	}
	imageEval(t, s, `try{Object.defineProperty(location,"href",{value:"evil"})}catch(e){};location.assign=()=>42;`)
	imageEval(t, s, `location.assign("/still-host")`)
	if requests[len(requests)-1] != "assign:https://example.test/still-host" {
		t.Fatal(requests)
	}
}
