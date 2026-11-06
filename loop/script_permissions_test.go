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
	"sync/atomic"
	"testing"
	"time"
)

func permissionFixture(t *testing.T, p profile.Profile) (*Loop, chanSink, *netpolicy.Fetcher) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sink := make(chanSink, 256)
	l := New(ctx, Config{Profile: p, Width: 400}, sink)
	go l.Run()
	u, _ := url.Parse("http://page.test/")
	return l, sink, netpolicy.New(netpolicy.Policy{Origin: u, AllowPrivate: true, AllowHosts: []string{"127.0.0.1"}})
}
func waitScriptPermission(t *testing.T, sink chanSink) ScriptPermissionMsg {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("missing permission request")
		case msg := <-sink:
			if p, ok := msg.(ScriptPermissionMsg); ok && !p.Expired {
				return p
			}
			if log, ok := msg.(LogMsg); ok && log.Level == "error" {
				t.Fatal(log.Line)
			}
		}
	}
}
func TestScriptPermissionOnceSessionAndSourceScope(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/javascript")
		fmt.Fprint(w, `console.log("EXECUTED")`)
	}))
	defer server.Close()
	p := externalProfile
	p.ScriptPrompts = true
	l, sink, f := permissionFixture(t, p)
	html := fmt.Sprintf(`<script>let sc=document.createElement("script");sc.src=%q;document.head.appendChild(sc)</script>`, server.URL+"/script.js")
	l.LoadPage(html, "http://page.test/first", f)
	first := waitScriptPermission(t, sink)
	if hits.Load() != 0 {
		t.Fatal("fetched before approval")
	}
	l.DecideScriptPermission(first.ID, first.Navigation, "once")
	waitLog(t, sink, "EXECUTED")
	l.LoadPage(html, "http://page.test/second", f)
	second := waitScriptPermission(t, sink)
	l.DecideScriptPermission(second.ID, second.Navigation, "session")
	waitLog(t, sink, "EXECUTED")
	l.LoadPage(html, "http://page.test/third", f)
	waitLog(t, sink, "EXECUTED")
	l.LoadPage(html, "http://different.test/", f)
	other := waitScriptPermission(t, sink)
	l.DecideScriptPermission(first.ID, first.Navigation, "session") // stale approval cannot authorize this page
	l.DecideScriptPermission(other.ID, other.Navigation, "deny")
	_, _, _ = pageState(t, l)
	if hits.Load() != 3 {
		t.Fatal("source or generation scope escaped", hits.Load())
	}
}
func TestSavedScriptGrantDoesNotAuthorizeFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		fmt.Fprint(w, `console.log("EXECUTED")`)
	}))
	defer server.Close()
	p := externalProfile
	p.Fetch = profile.FetchOrigin
	p.ScriptGrants = []profile.ScriptGrant{{Source: "http://page.test", Destination: server.URL}}
	l, sink, _ := permissionFixture(t, p)
	base, _ := url.Parse("http://page.test/")
	f := netpolicy.New(netpolicy.Policy{Origin: base, AllowPrivate: true})
	html := fmt.Sprintf(`<script>let sc=document.createElement("script");sc.src=%q;sc.onload=()=>fetch(%q).then(()=>{throw Error("fetch authority leaked")}).catch(()=>console.log("FETCH DENIED"));document.head.appendChild(sc)</script>`, server.URL, server.URL)
	l.LoadPage(html, "http://page.test/", f)
	waitLog(t, sink, "EXECUTED")
	waitLog(t, sink, "FETCH DENIED")
}
func TestScriptPermissionDenialAndQueueBound(t *testing.T) {
	p := externalProfile
	p.ScriptPrompts = true
	l, sink, f := permissionFixture(t, p)
	l.LoadPage(`<script>for(let i=0;i<10;i++){let s=document.createElement("script");s.src="http://cdn.test/"+i;s.onerror=()=>console.log("DENIED");document.head.appendChild(s)}</script>`, "http://page.test/", f)
	count := 0
	denied := 0
	deadline := time.After(5 * time.Second)
	for count < 8 || denied < 2 {
		select {
		case <-deadline:
			t.Fatal("queue not bounded", count, denied)
		case msg := <-sink:
			switch v := msg.(type) {
			case ScriptPermissionMsg:
				if !v.Expired {
					count++
				}
			case LogMsg:
				if v.Line == "DENIED" {
					denied++
				}
			}
		}
	}
	if count != 8 {
		t.Fatal(count)
	}
	done := make(chan struct{})
	l.post(func() {
		if len(l.page.scriptPermissions) != 8 {
			t.Error("bad pending count")
		}
		for id := range l.page.scriptPermissions {
			l.decideScriptPermission(id, l.navGen.Load(), "deny")
		}
		close(done)
	})
	<-done
}

func TestScriptPermissionExpiryDoesNotFetch(t *testing.T) {
	p := externalProfile
	p.ScriptPrompts = true
	l, sink, f := permissionFixture(t, p)
	l.LoadPage(`<script>let s=document.createElement("script");s.src="http://cdn.test/a.js";s.onerror=()=>console.log("EXPIRED");document.head.appendChild(s)</script>`, "http://page.test/", f)
	request := waitScriptPermission(t, sink)
	l.post(func() { l.page.scriptPermissions[request.ID].timer.Reset(time.Millisecond) })
	waitLog(t, sink, "EXPIRED")
	l.DecideScriptPermission(request.ID, request.Navigation, "session")
	done := make(chan struct{})
	l.post(func() {
		if len(l.scriptSessions) != 0 || len(l.page.scriptPermissions) != 0 {
			t.Error("expired request revived")
		}
		close(done)
	})
	<-done
}
