// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package loop

import (
	"context"
	"testing"

	"nimbleterminal.dev/ntbrowser/cookies"
	"nimbleterminal.dev/ntbrowser/jsvm"
	"nimbleterminal.dev/ntbrowser/netpolicy"
	"nimbleterminal.dev/ntbrowser/profile"
)

func TestCookieGateAndNavigationLifetime(t *testing.T) {
	for _, on := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		p, _ := profile.Builtin("browse")
		if on {
			p.Cookies = profile.CookiesSession
		}
		store := cookies.New()
		f := netpolicy.New(netpolicy.Policy{Cookies: store})
		sink := make(chanSink, 256)
		l := New(ctx, Config{Profile: p, Width: 400, Script: jsvm.Config{CookieRead: func() string { return "bypass=1" }}}, sink)
		go l.Run()
		l.LoadPage(`<script>document.cookie="session=1; Path=/";console.log("enabled:"+navigator.cookieEnabled);console.log("first:"+document.cookie)</script>`, "https://example.test/", f)
		value := ""
		if on {
			value = "session=1"
		}
		enabled := "false"
		if on {
			enabled = "true"
		}
		waitLog(t, sink, "enabled:"+enabled)
		waitLog(t, sink, "first:"+value)
		l.LoadPage(`<script>console.log("next:"+document.cookie)</script>`, "https://example.test/next", f)
		waitLog(t, sink, "next:"+value)
		l.LoadPage(`<script>console.log("other:"+document.cookie)</script>`, "https://unrelated.test/", f)
		waitLog(t, sink, "other:")
		cancel()
	}
}
