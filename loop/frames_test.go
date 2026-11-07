// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package loop

import (
	"context"
	"fmt"
	"html"
	"image"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"nimbleterminal.dev/ntbrowser/jsvm"
	"nimbleterminal.dev/ntbrowser/profile"
)

func TestIsolatedFrameRoundTrip(t *testing.T) {
	p := externalProfile
	p.Frames = profile.FramesOrigin
	l, sink, f := scriptTestLoop(t, p, "https://example.test")
	child := `<p>child pixels</p><script>
 if(parent===window||top!==parent)throw Error('relationships');
 let denied=false;try{parent.document}catch(e){denied=e.name==='SecurityError'}if(!denied)throw Error('DOM leak');
 addEventListener('message',e=>{if(e.source!==parent||e.origin!=='https://example.test')throw Error('source');parent.postMessage({reply:e.data.question},e.origin)});
 parent.postMessage('ready','*');
 </script>`
	l.LoadPage(`<iframe id=f srcdoc="`+html.EscapeString(child)+`"></iframe><script>
 const frame=document.getElementById('f');
 addEventListener('message',e=>{
 if(e.source!==frame.contentWindow||e.origin!=='https://example.test')throw Error('identity');
 if(e.data==='ready'){e.source.postMessage({question:42},'/');return}
 if(e.data.reply!==42)throw Error('reply');console.log('frame-roundtrip');
 });
 frame.addEventListener('load',()=>console.log('child-loaded'));
 addEventListener('load',()=>console.log('parent-loaded'));
 </script>`, "https://example.test/", f)
	// Messages and loads can arrive in either order.
	wanted := map[string]bool{"frame-roundtrip": false, "child-loaded": false, "parent-loaded": false}
	deadline := time.After(8 * time.Second)
	for {
		all := true
		for _, v := range wanted {
			all = all && v
		}
		if all {
			break
		}
		select {
		case m := <-sink:
			if log, ok := m.(LogMsg); ok {
				if log.Level == "error" {
					t.Fatal(log.Line)
				}
				if _, ok := wanted[log.Line]; ok {
					wanted[log.Line] = true
				}
			}
		case <-deadline:
			t.Fatalf("missing events: %v", wanted)
		}
	}
	done := make(chan bool, 1)
	l.post(func() {
		for _, f := range l.page.frames {
			done <- f.image != nil
			return
		}
		done <- false
	})
	if !<-done {
		t.Fatal("no child raster")
	}
}

func TestFrameNetworkGateAndLoad(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<p>network child</p><script>parent.postMessage('network-child','*')</script>`)
	}))
	defer server.Close()
	p := externalProfile
	p.Frames = profile.FramesOrigin
	l, sink, f := scriptTestLoop(t, p, server.URL)
	l.LoadPage(`<iframe src="/child"></iframe><script>addEventListener('message',e=>console.log(e.data))</script>`, server.URL+"/", f)
	waitLog(t, sink, "network-child")
}

func TestFrameEmbeddingPolicy(t *testing.T) {
	for _, header := range []string{"DENY", "SAMEORIGIN", "sandbox allow-scripts", "frame-ancestors 'none'"} {
		t.Run(header, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				if strings.Contains(header, " ") {
					w.Header().Set("Content-Security-Policy", header)
				} else {
					w.Header().Set("X-Frame-Options", header)
				}
				fmt.Fprint(w, "<p>ok</p>")
			}))
			defer server.Close()
			p := externalProfile
			p.Frames = profile.FramesOrigin
			l, _, f := scriptTestLoop(t, p, server.URL)
			_, _, _, _, derived, err := l.fetchFrame(context.Background(), &Page{Base: server.URL, Fetcher: f}, jsvm.FrameSpec{Src: "/child"})
			if derived != nil {
				derived.CloseScriptConnections()
			}
			if (err == nil) != (header == "SAMEORIGIN") {
				t.Fatalf("policy %s: %v", header, err)
			}
		})
	}
}

func TestFrameBrokerRevisionAndOrigin(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dst := New(ctx, Config{}, nil)
	h := &frameHub{endpoints: map[uint64]frameEndpoint{}}
	a, _ := h.allocate()
	b, _ := h.allocate()
	rev := h.bind(a, nil, "https://a.test")
	h.bind(b, dst, "https://b.test")
	if err := h.post(a, rev, jsvm.FramePost{Target: b, TargetOrigin: "/", Data: []byte("1")}); err != nil {
		t.Fatal(err)
	}
	if len(dst.inbox) != 0 {
		t.Fatal("wrong-origin delivery")
	}
	if err := h.post(a, rev, jsvm.FramePost{Target: b, TargetOrigin: "*", Data: []byte("1")}); err != nil {
		t.Fatal(err)
	}
	h.bind(b, nil, "https://c.test")
	(<-dst.inbox)() // must discard before touching the absent VM.
	if h.pending != 0 || h.bytes != 0 {
		t.Fatal("queue budget leak")
	}
	h.bind(a, nil, "https://a.test")
	h.post(a, rev, jsvm.FramePost{Target: b, TargetOrigin: "*", Data: []byte("1")})
	if len(dst.inbox) != 0 {
		t.Fatal("stale sender delivered")
	}
}

func TestFrameSandboxStaticAndOpaque(t *testing.T) {
	for _, sandbox := range []string{"", "allow-scripts"} {
		t.Run(sandbox, func(t *testing.T) {
			p := externalProfile
			p.Frames = profile.FramesOrigin
			l, sink, f := scriptTestLoop(t, p, "https://example.test")
			child := `<p>opaque child</p><script>parent.postMessage('opaque','*')</script>`
			source := `<iframe id=f sandbox="` + sandbox + `" srcdoc="` + html.EscapeString(child) + `"></iframe><script>
    addEventListener('message',e=>{if(e.origin!=='null')throw Error('opaque origin');console.log('opaque-message')});
    addEventListener('load',()=>console.log('static-load'));
    </script>`
			l.LoadPage(source, "https://example.test/", f)
			if sandbox == "" {
				waitLog(t, sink, "static-load")
			} else {
				waitLog(t, sink, "opaque-message")
			}
		})
	}
}

func TestFramesOffAndRemoval(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			p := externalProfile
			if enabled {
				p.Frames = profile.FramesOrigin
			}
			l, sink, f := scriptTestLoop(t, p, "https://example.test")
			l.LoadPage(`<script>
    const f=document.createElement('iframe');f.srcdoc='<p>hello</p>';document.body.appendChild(f);
    const w=f.contentWindow;
    if(w){f.remove();setTimeout(()=>{if(!w.closed)throw Error('not closed');console.log('removed')},20)}
    else console.log('disabled');
   </script>`, "https://example.test/", f)
			if enabled {
				waitLog(t, sink, "removed")
			} else {
				waitLog(t, sink, "disabled")
			}
		})
	}
}

func TestFrameRasterClick(t *testing.T) {
	p := externalProfile
	p.Frames = profile.FramesOrigin
	l, sink, f := scriptTestLoop(t, p, "https://example.test")
	child := `<button id=b>Press</button><script>document.getElementById('b').addEventListener('click',()=>parent.postMessage('clicked','*'))</script>`
	l.LoadPage(`<iframe id=f width=300 height=150 srcdoc="`+html.EscapeString(child)+`"></iframe><script>addEventListener('message',e=>console.log(e.data));addEventListener('load',()=>console.log('ready'))</script>`, "https://example.test/", f)
	waitLog(t, sink, "ready")
	// Wait for the parent to install a frame containing the child's raster.
	deadline := time.After(5 * time.Second)
	for {
		done := make(chan bool, 1)
		l.post(func() {
			if l.page.Tree.Dirty() {
				done <- false
				return
			}
			for _, b := range l.page.boxes {
				if b.Anchor == "f" {
					n := l.page.Tree.ByID("f")
					shown, ok := l.page.displayedFrames[n.ID()]
					if !ok || shown.frame.generation != shown.generation || shown.frame.revision != shown.revision {
						done <- false
						return
					}
					// Child button occupies the top-left of its viewport.
					area := b.Bounds
					area.Min.X += 12
					area.Min.Y += 12
					area.Max = area.Min.Add(image.Pt(1, 1))
					done <- l.clickFrame(area)
					return
				}
			}
			done <- false
		})
		if <-done {
			break
		}
		select {
		case <-deadline:
			t.Fatal("no iframe hit box")
		case <-time.After(10 * time.Millisecond):
		}
	}
	waitLog(t, sink, "clicked")
}

func TestOldFrameShutdownKeepsReplacementEndpoint(t *testing.T) {
	h := &frameHub{endpoints: map[uint64]frameEndpoint{}}
	id, _ := h.allocate()
	old := &Loop{cfg: Config{frameHub: h}}
	replacement := &Loop{}
	h.bind(id, old, "https://a.test")
	h.bind(id, replacement, "https://a.test")
	old.closeFrames(&Page{frameID: id})
	if h.endpoints[id].loop != replacement {
		t.Fatal("old child removed replacement")
	}
}

func TestFrameReplacementRoundTrip(t *testing.T) {
	p := externalProfile
	p.Frames = profile.FramesOrigin
	l, sink, f := scriptTestLoop(t, p, "https://example.test")
	child := `<script>addEventListener('message',e=>parent.postMessage('replacement-reply','*'));parent.postMessage('replacement-ready','*')</script>`
	// Keep the same WindowProxy across replacement, then check the replacement
	// still receives messages after the old child has shut down.
	l.LoadPage(`<iframe id=f srcdoc="<p>old</p>"></iframe><script>
 const f=document.getElementById('f'),w=f.contentWindow;
 f.addEventListener('load',()=>{if(!window.changed){window.changed=true;f.srcdoc=`+strings.ReplaceAll(strconv.Quote(child), "</", "<\\/")+`}});
 addEventListener('message',e=>{
  if(e.source!==w)throw Error('proxy changed');
  if(e.data==='replacement-ready')setTimeout(()=>w.postMessage('ping','*'),20);
  if(e.data==='replacement-reply')console.log('replacement-ok');
 });
 </script>`, "https://example.test/", f)
	waitLog(t, sink, "replacement-ok")
}

func TestFrameTreeLimits(t *testing.T) {
	h := &frameHub{endpoints: map[uint64]frameEndpoint{}}
	for i := 0; i < maxLiveFrames+1; i++ {
		if _, err := h.allocate(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.allocate(); err == nil {
		t.Fatal("live frame cap bypassed")
	}
	for id := range h.endpoints {
		h.remove(id)
	}
	for h.loads < maxFrameLoads {
		id, err := h.allocate()
		if err != nil {
			t.Fatal(err)
		}
		h.remove(id)
	}
	if _, err := h.allocate(); err == nil {
		t.Fatal("churn cap bypassed")
	}
}

func TestGlynxCrossOriginFrameKeepsDOMIsolation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<p>child</p><script>
   let denied=false;try{parent.document}catch(e){denied=e.name==='SecurityError'}
   parent.postMessage(denied?'ISOLATED CHILD':'DOM LEAK','*');
  </script>`)
	}))
	defer server.Close()
	p, _ := profile.Builtin("glynx")
	l, sink, f := permissionFixture(t, p)
	l.LoadPage(fmt.Sprintf(`<script>addEventListener('message',e=>console.log(e.data))</script><iframe src=%q></iframe>`, server.URL), "http://page.test/", f)
	waitLog(t, sink, "ISOLATED CHILD")
}
