// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package netpolicy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

// countingServer reports how many TCP connections its clients opened.
func countingServer(t *testing.T, contentType, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var conns atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		fmt.Fprint(w, body)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			conns.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	return srv, &conns
}

// Each external script gets its own derived fetcher; they must share the
// page's connections instead of handshaking once per script.
func TestDerivedScriptFetchersReuseConnection(t *testing.T) {
	srv, conns := countingServer(t, "text/javascript", "1")
	u, _ := url.Parse(srv.URL)
	page := New(Policy{Origin: u, AllowPrivate: true})
	for i := range 3 {
		if _, err := page.ForScriptSources(srv.URL, nil, false).Script(context.Background(), "/s.js", "", ""); err != nil {
			t.Fatalf("script %d: %v", i, err)
		}
	}
	if n := conns.Load(); n != 1 {
		t.Fatalf("3 script fetches opened %d connections, want 1", n)
	}
}

// Frame fetchers derive from the page's as well.
func TestDerivedFrameFetchersReuseConnection(t *testing.T) {
	srv, conns := countingServer(t, "text/html", "<p>ok</p>")
	u, _ := url.Parse(srv.URL)
	page := New(Policy{Origin: u, AllowPrivate: true})
	for _, f := range []*Fetcher{page, page.ForFrameSources(srv.URL, nil, false), page.ForFramePage(srv.URL, nil, false)} {
		if _, err := f.Do(context.Background(), Request{URL: "/"}); err != nil {
			t.Fatal(err)
		}
	}
	if n := conns.Load(); n != 1 {
		t.Fatalf("page and frame fetchers opened %d connections, want 1", n)
	}
}

// A session's pool is shared by fetchers built independently: the navigation,
// the page and its subresources.
func TestPoolSharedAcrossFetchers(t *testing.T) {
	srv, conns := countingServer(t, "text/html", "<p>ok</p>")
	u, _ := url.Parse(srv.URL)
	pool := NewPool()
	defer pool.Close()
	for range 3 {
		f := New(Policy{Pool: pool, Origin: u, AllowPrivate: true})
		if _, err := f.Do(context.Background(), Request{URL: "/"}); err != nil {
			t.Fatal(err)
		}
	}
	if n := conns.Load(); n != 1 {
		t.Fatalf("3 fetchers on one pool opened %d connections, want 1", n)
	}
}

// A connection dialed under AllowPrivate skipped the address check; a policy
// without it must never be handed that connection.
func TestPoolSeparatesPrivateConnections(t *testing.T) {
	pool := NewPool()
	defer pool.Close()
	private, public := pool.transport(true), pool.transport(false)
	if private == public {
		t.Fatal("private-network and public policies share one transport")
	}
	if pool.transport(true) != private || pool.transport(false) != public {
		t.Fatal("transports are not stable per pool")
	}
	srv, _ := countingServer(t, "text/html", "<p>ok</p>")
	u, _ := url.Parse(srv.URL)
	if _, err := New(Policy{Pool: pool, Origin: u, AllowPrivate: true}).Do(context.Background(), Request{URL: "/"}); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Policy{Pool: pool, Origin: u}).Do(context.Background(), Request{URL: "/"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("loopback over a warm shared pool: %v, want ErrDenied", err)
	}
}

// Admission is a property of the policy alone; no fetcher is needed to ask.
func TestPolicyAllowedNeedsNoFetcher(t *testing.T) {
	origin, _ := url.Parse("https://example.test/")
	p := Policy{Origin: origin, AllowHosts: []string{".cdn.test"}}
	for raw, want := range map[string]bool{
		"https://example.test/a":   true,
		"https://img.cdn.test/a":   true,
		"https://other.test/a":     false,
		"http://127.0.0.1/a":       false,
		"file:///etc/passwd":       false,
		"https://u:p@example.test": false,
	} {
		u, _ := url.Parse(raw)
		err := p.Allowed(u)
		if (err == nil) != want {
			t.Errorf("%s: %v, want allowed=%v", raw, err, want)
		}
		if ferr := New(p).Allowed(u); (ferr == nil) != (err == nil) {
			t.Errorf("%s: fetcher says %v, policy says %v", raw, ferr, err)
		}
	}
}

// The per-policy timeout still bounds the wait for headers now that the
// transport is shared and carries no timeout of its own.
func TestHeaderTimeoutIsPerPolicy(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	f, base := openFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}, Policy{Timeout: 50 * time.Millisecond})
	started := time.Now()
	if _, err := f.Do(context.Background(), Request{URL: base + "/"}); err == nil {
		t.Fatal("a stalled server was not timed out")
	}
	if took := time.Since(started); took > 2*time.Second {
		t.Fatalf("timeout took %v", took)
	}
}
