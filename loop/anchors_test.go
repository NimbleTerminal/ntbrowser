// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package loop

import (
	"context"
	"net/url"
	"nimbleterminal.dev/ntbrowser/dom"
	"nimbleterminal.dev/ntbrowser/netpolicy"
	"strings"
	"testing"
	"time"

	"nimbleterminal.dev/ntbrowser/subres"
	"nimbleterminal.dev/nthtml/document"
)

func TestAnchorBindingsHaveNoHostEffectsBeforeActivation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 64)
	p := testProfile
	l := New(ctx, Config{Profile: p, Width: 400, SubresourceTransport: func(string) subres.Transport { t.Error("unexpected resource session"); return nil }, Subresources: subres.Config{Mode: subres.ModeOff}}, sink)
	go l.Run()
	l.LoadPage(`<a id=a>go</a><script>const a=document.getElementById("a");a.href="https://other.test/next";a.target="_blank";a.download="saved";a.ping="https://beacon.test/";a.relList.add("opener");</script>`, "https://example.test/", nil)
	var link document.Link
	deadline := time.After(patience)
	ready := false
	for !ready {
		select {
		case <-deadline:
			t.Fatal("no frame")
		case msg := <-sink:
			switch v := msg.(type) {
			case NavigateMsg:
				t.Fatalf("attribute write navigated: %+v", v)
			case LogMsg:
				if v.Level == "error" {
					t.Fatal(v.Line)
				}
			case FrameMsg:
				if v.Err != nil {
					t.Fatal(v.Err)
				}
				if len(v.Result.Links) == 1 {
					link = v.Result.Links[0]
					ready = true
				}
			}
		}
	}
	l.LinkActivated(link)
	for {
		select {
		case <-deadline:
			t.Fatal("reader activation did not navigate")
		case msg := <-sink:
			if nav, ok := msg.(NavigateMsg); ok {
				if nav.Href != "https://other.test/next" || nav.Form {
					t.Fatalf("wrong action: %+v", nav)
				}
				return
			}
		}
	}
}

func TestAnchorClickUsesFinalDestination(t *testing.T) {
	for _, action := range []string{"viewer", "focus"} {
		t.Run(action, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sink := make(chanSink, 64)
			l := New(ctx, Config{Profile: testProfile, Width: 400}, sink)
			go l.Run()
			l.LoadPage(`<a id=a href="/original">go</a><script>const a=document.getElementById("a");a.addEventListener("click",()=>{a.href="https://OTHER.test:443/changed"});a.focus();</script>`, "https://example.test/", nil)
			var link document.Link
			deadline := time.After(patience)
			ready := false
			for !ready {
				select {
				case <-deadline:
					t.Fatal("no frame")
				case msg := <-sink:
					if f, ok := msg.(FrameMsg); ok && f.Err == nil && len(f.Result.Links) > 0 {
						link = f.Result.Links[0]
						ready = true
					}
				}
			}
			if action == "viewer" {
				l.LinkActivated(link)
			} else {
				l.Activate()
			}
			for {
				select {
				case <-deadline:
					t.Fatal("no navigation")
				case msg := <-sink:
					if nav, ok := msg.(NavigateMsg); ok {
						if nav.Href != "https://other.test/changed" {
							t.Fatal(nav.Href)
						}
						return
					}
				}
			}
		})
	}
}

func TestAnchorExecutionAndLocalSchemesNotOpenable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 64)
	l := New(ctx, Config{Profile: testProfile, Width: 400}, sink)
	go l.Run()
	l.LoadPage(`<a id=a href="/original">go</a><script>const a=document.getElementById("a");a.addEventListener("click",()=>{a.href="javascript:console.log('executed')"});</script>`, "https://example.test/", nil)
	deadline := time.After(patience)
	var link document.Link
	for link.Href == "" {
		select {
		case <-deadline:
			t.Fatal("no frame")
		case msg := <-sink:
			if f, ok := msg.(FrameMsg); ok && f.Err == nil && len(f.Result.Links) > 0 {
				link = f.Result.Links[0]
			}
		}
	}
	l.LinkActivated(link)
	l.post(func() { l.send(LogMsg{"log", "checkpoint"}) })
	for {
		select {
		case <-deadline:
			t.Fatal("no checkpoint")
		case msg := <-sink:
			switch v := msg.(type) {
			case NavigateMsg:
				t.Fatal("unsafe scheme navigated")
			case LogMsg:
				if v.Line == "executed" {
					t.Fatal("javascript URL executed")
				}
				if v.Line == "checkpoint" {
					return
				}
			}
		}
	}
}

func TestAnchorCanonicalURLsStillFaceNetworkPolicy(t *testing.T) {
	for _, raw := range []string{"http://0x7f000001/", "http://2130706433/", "http://[::1]/", "https://user:pass@example.test/", "file:///tmp/private", "javascript:alert(1)", "data:text/html,hello"} {
		t.Run(raw, func(t *testing.T) {
			tree, err := dom.Parse(strings.NewReader(`<a id=a>go</a>`), dom.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			n := tree.ByID("a")
			if err := tree.SetAttr(n, "href", raw); err != nil {
				t.Fatal(err)
			}
			href := anchorHref(&Page{Tree: tree, Base: "https://example.test/"}, n)
			if href == "" {
				return
			}
			u, err := url.Parse(href)
			if err != nil {
				t.Fatal(err)
			}
			if err := netpolicy.New(netpolicy.Policy{AnyOrigin: true}).Allowed(u); err == nil {
				t.Fatalf("canonical URL bypassed policy: %s -> %s", raw, href)
			}
		})
	}
}
