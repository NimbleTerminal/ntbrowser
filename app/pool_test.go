// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"nimbleterminal.dev/ntbrowser/netpolicy"
	"nimbleterminal.dev/ntbrowser/profile"
	"nimbleterminal.dev/ntbrowser/subres"
)

// One session pool serves successive navigations and the page's
// subresources: a site is dialed once, not once per fetcher.
func TestSessionPoolSharedByNavigationAndSubresources(t *testing.T) {
	var conns atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a.css" {
			w.Header().Set("Content-Type", "text/css")
			fmt.Fprint(w, "p{color:red}")
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<p>hello</p>")
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			conns.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)

	p := openProfile(profile.MediaOff)
	pool := netpolicy.NewPool()
	defer pool.Close()
	m := contentModel(t, p, srv.URL)
	m.policy.Pool = pool
	for _, path := range []string{"/", "/next"} {
		if msg := run(t, m, srv.URL+path); msg.err != nil {
			t.Fatalf("%s: %v", path, msg.err)
		}
	}
	template := PolicyFor(p)
	template.Pool = pool
	sheets := SubresourceTransport(template, subres.Config{Mode: subres.ModeOrigin})(srv.URL + "/")
	if _, err := sheets.Do(context.Background(), netpolicy.Request{URL: "/a.css", Kind: netpolicy.KindStyle}); err != nil {
		t.Fatal(err)
	}
	if n := conns.Load(); n != 1 {
		t.Fatalf("two navigations and a stylesheet opened %d connections, want 1", n)
	}
}
