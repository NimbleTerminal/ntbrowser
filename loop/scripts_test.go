// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package loop

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"nimbleterminal.dev/ntbrowser/netpolicy"
	"nimbleterminal.dev/ntbrowser/profile"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func scriptTestLoop(t *testing.T, p profile.Profile, base string) (*Loop, chanSink, *netpolicy.Fetcher) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sink := make(chanSink, 256)
	l := New(ctx, Config{Profile: p, Width: 400}, sink)
	go l.Run()
	u, _ := url.Parse(base)
	return l, sink, netpolicy.New(netpolicy.Policy{Origin: u, AllowPrivate: true})
}

func TestDynamicScriptFixtureOrdering(t *testing.T) {
	second := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		if r.URL.Path == "/first.js" {
			select {
			case <-second:
			case <-r.Context().Done():
				return
			}
			fmt.Fprint(w, `order.push("first")`)
		} else {
			fmt.Fprint(w, `order.push("second")`)
			close(second)
		}
	}))
	defer server.Close()
	l, sink, f := scriptTestLoop(t, externalProfile, server.URL)
	html, err := os.ReadFile("testdata/compat/dynamic.html")
	if err != nil {
		t.Fatal(err)
	}
	l.LoadPage(string(html), server.URL+"/", f)
	waitLog(t, sink, "PASS:dynamic")
}

func TestDynamicScriptDenials(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `throw Error("HTML executed")`)
	}))
	defer server.Close()
	for _, tc := range []struct {
		name  string
		p     profile.Profile
		attrs string
		fetch bool
	}{
		{"gate", testProfile, "", false}, {"empty", externalProfile, `sc.src="  ";`, false}, {"integrity", externalProfile, `sc.integrity="sha256-invalid";`, false}, {"mime", externalProfile, "", true}, {"origin", externalProfile, `sc.src="https://unrelated.test/no.js";`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := hits.Load()
			l, sink, f := scriptTestLoop(t, tc.p, server.URL)
			l.LoadPage(`<script>let sc=document.createElement("script");sc.src="/bad.js";`+tc.attrs+`sc.onload=()=>{throw Error("denied script loaded")};sc.onerror=()=>console.log("PASS:denied");document.head.appendChild(sc);</script>`, server.URL+"/", f)
			waitLog(t, sink, "PASS:denied")
			want := int32(0)
			if tc.fetch {
				want = 1
			}
			if hits.Load()-before != want {
				t.Fatal("unexpected network activity")
			}
		})
	}
}

func TestDynamicScriptNavigationCancels(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(cancelled) }))
	defer server.Close()
	l, sink, f := scriptTestLoop(t, externalProfile, server.URL)
	l.LoadPage(`<script>let sc=document.createElement("script");sc.src="/slow.js";sc.onload=sc.onerror=()=>console.log("STALE");document.head.appendChild(sc);</script>`, server.URL+"/", f)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request not started")
	}
	l.LoadPage(`<script>console.log("NEW")</script>`, server.URL+"/next", f)
	waitLog(t, sink, "NEW")
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("request not cancelled")
	}
	// A task barrier ensures the replacement realm is responsive; the generation
	// assertion in completion prevents even an already queued old result dispatch.
	if base, _, _ := pageState(t, l); base != server.URL+"/next" {
		t.Fatal(base)
	}
	for {
		select {
		case msg := <-sink:
			if m, ok := msg.(LogMsg); ok && m.Line == "STALE" {
				t.Fatal("stale handler dispatched")
			}
		default:
			return
		}
	}
}

func TestDynamicAsyncBypassesOrderedWait(t *testing.T) {
	release := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		if r.URL.Path == "/slow.js" {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			fmt.Fprint(w, `console.log("SLOW")`)
		} else {
			fmt.Fprint(w, `if(document.currentScript.src.indexOf("fast.js")<0)throw Error("currentScript missing");console.log("FAST")`)
		}
	}))
	defer server.Close()
	defer close(release)
	l, sink, f := scriptTestLoop(t, externalProfile, server.URL)
	l.LoadPage(`<script>let slow=document.createElement("script");slow.async=false;slow.src="/slow.js";document.head.appendChild(slow);let fast=document.createElement("script");fast.src="/fast.js";document.head.appendChild(fast);</script>`, server.URL+"/", f)
	waitLog(t, sink, "FAST")
	release <- struct{}{}
	waitLog(t, sink, "SLOW")
}
