// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package loop

import (
	"context"
	"image/color"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nimbleterminal.dev/ntbrowser/profile"
	"nimbleterminal.dev/ntbrowser/subres"
)

func imageLog(t *testing.T, sink chanSink, want string) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case <-timeout:
			t.Fatalf("no image log %q", want)
		case msg := <-sink:
			if v, ok := msg.(LogMsg); ok {
				if v.Line == want {
					return
				}
				if v.Level == "error" {
					t.Fatalf("script error: %s", v.Line)
				}
			}
		}
	}
}

func TestDetachedImageSharesRendererCache(t *testing.T) {
	h := newHits()
	png := pngBytes(t, 3, 4, color.RGBA{255, 0, 0, 255})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.count(r); serve("image/png", png)(w, r) }))
	defer srv.Close()
	l, sink := newSubresLoop(t, subres.Config{Mode: subres.ModeOrigin, MaxImages: 1})
	l.LoadPage(`<p>images</p><script>
 const image=new Image();
 image.onload=function(e){
  if(this!==image||image.isConnected||image.naturalWidth!==3||image.naturalHeight!==4) throw Error("bad detached image");
  document.body.appendChild(image);
  const second=document.createElement("img");
  second.onload=()=>console.log("both loaded");
  second.src="/tiny.png";
  document.body.appendChild(second);
 };
 image.src="/tiny.png";
 image.decode().then(()=>console.log("decoded"));
 </script>`, srv.URL+"/", nil)
	imageLog(t, sink, "both loaded")
	frameWhere(t, sink, 5*time.Second, func(f FrameMsg) bool { return len(imgBoxes(f.Result)) == 2 })
	if h.get("/tiny.png") != 1 {
		t.Fatalf("duplicate request: %d", h.get("/tiny.png"))
	}
}

func TestImageErrorsAndSharedCaps(t *testing.T) {
	h := newHits()
	png := pngBytes(t, 2, 2, color.RGBA{255, 0, 0, 255})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.count(r); serve("image/png", png)(w, r) }))
	defer srv.Close()
	l, sink := newSubresLoop(t, subres.Config{Mode: subres.ModeOrigin, MaxImages: 1, MaxImagePixels: 1})
	l.LoadPage(`<script>
 const a=new Image();
 a.onerror=()=>{if(!a.complete||a.naturalWidth!==0)throw Error("bad error state");const b=new Image(); b.onerror=()=>console.log("both denied"); b.src="/over-count";};
 a.src="/over-pixels";
 </script>`, srv.URL+"/", nil)
	imageLog(t, sink, "both denied")
	if h.get("/over-pixels") != 1 || h.get("/over-count") != 0 {
		t.Fatalf("caps: %+v", h.m)
	}
}

func TestScriptImageRespectsClosedSubresourcesGate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := profile.Profile{Scripts: profile.ScriptsInline}
	sink := make(chanSink, 64)
	l := New(ctx, Config{Profile: p, Width: 400, Subresources: subres.Config{Mode: subres.ModeAny}, SubresourceTransport: func(string) subres.Transport { t.Error("closed profile constructed transport"); return nil }}, sink)
	go l.Run()
	l.LoadPage(`<img src="https://example.test/attached"><script>const i=new Image();i.onerror=()=>console.log("denied");i.src="https://example.test/detached";</script>`, "https://example.test/", nil)
	imageLog(t, sink, "denied")
}

func TestImageReplacementDropsStaleCompletion(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	png := pngBytes(t, 2, 3, color.RGBA{255, 0, 0, 255})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			close(started)
			<-release
		}
		serve("image/png", png)(w, r)
	}))
	defer srv.Close()
	l, sink := newSubresLoop(t, subres.Config{Mode: subres.ModeOrigin})
	l.LoadPage(`<script>let count=0;const i=new Image();i.onload=()=>{count++;console.log("loaded:"+i.currentSrc.split("/").pop())};i.src="/old";</script>`, srv.URL+"/", nil)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("no old request")
	}
	l.post(func() { l.report(l.page.Sandbox.Eval("replace.js", `i.src="/new"`)) })
	imageLog(t, sink, "loaded:new")
	close(release)
	// A checkpoint after the old result has arrived must not dispatch another load.
	l.post(func() { l.report(l.page.Sandbox.Eval("check.js", `setTimeout(()=>console.log("count:"+count),100)`)) })
	imageLog(t, sink, "count:1")
}

func TestImageNavigationDiscardsCompletion(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer srv.Close()
	l, sink := newSubresLoop(t, subres.Config{Mode: subres.ModeOrigin})
	l.LoadPage(`<script>const i=new Image();i.onload=i.onerror=()=>console.log("stale");i.src="/slow";</script>`, srv.URL+"/", nil)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("no request")
	}
	l.LoadPage(`<p>new</p><script>setTimeout(()=>console.log("new page"),100)</script>`, srv.URL+"/next", nil)
	timeout := time.After(3 * time.Second)
	for {
		select {
		case <-timeout:
			t.Fatal("navigation timeout")
		case m := <-sink:
			if v, ok := m.(LogMsg); ok {
				if strings.Contains(v.Line, "stale") {
					t.Fatal(v.Line)
				}
				if v.Line == "new page" {
					return
				}
			}
		}
	}
}

func TestDetachedImageOriginDenial(t *testing.T) {
	h := newHits()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.count(r)
		serve("image/png", pngBytes(t, 1, 1, color.RGBA{}))(w, r)
	}))
	defer other.Close()
	l, sink := newSubresLoop(t, subres.Config{Mode: subres.ModeOrigin})
	l.LoadPage(`<script>const i=new Image();i.onerror=()=>console.log("origin denied");i.src="`+other.URL+`/image";</script>`, "https://example.test/", nil)
	imageLog(t, sink, "origin denied")
	if h.get("/image") != 0 {
		t.Fatal("cross-origin request escaped origin policy")
	}
}
