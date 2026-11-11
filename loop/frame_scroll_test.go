// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package loop

import (
	"html"
	"image"
	"nimbleterminal.dev/ntbrowser/profile"
	"testing"
	"time"
)

func TestFrameScriptScrollAndClickAfterScroll(t *testing.T) {
	p := externalProfile
	p.Frames = profile.FramesOrigin
	l, sink, f := scriptTestLoop(t, p, "https://example.test")
	child := `<div style="height:500px;background:red"></div><button id=b>Scrolled button</button><div style="height:300px"></div><script>
 document.getElementById('b').addEventListener('click',()=>parent.postMessage('scrolled-click','*'));
 addEventListener('load',()=>{scrollTo({top:480,behavior:'instant'});if(scrollY!==480||pageYOffset!==480)throw Error('scroll aliases:'+scrollY+':'+pageYOffset);parent.postMessage('scroll-requested','*')});
 </script>`
	l.LoadPage(`<iframe id=f height=150 srcdoc="`+html.EscapeString(child)+`"></iframe><script>addEventListener('message',e=>console.log(e.data))</script>`, "https://example.test/", f)
	waitLog(t, sink, "scroll-requested")
	deadline := time.After(patience)
	for {
		ready := make(chan bool, 1)
		l.post(func() {
			if l.page.Tree.Dirty() {
				ready <- false
				return
			}
			n := l.page.Tree.ByID("f")
			shown, ok := l.page.displayedFrames[n.ID()]
			if !ok || shown.offset.Y != 480 || shown.frame.generation != shown.generation {
				ready <- false
				return
			}
			child := shown.frame.child
			if child.page.Tree.Dirty() || !child.FrameCurrent(shown.generation) {
				ready <- false
				return
			}
			var point image.Point
			for _, b := range child.page.boxes {
				if b.Anchor == "b" && b.Bounds.Dy() > 1 {
					point = b.Bounds.Min.Add(image.Pt(5, 5))
					break
				}
			}
			if point.Y < 480 {
				ready <- false
				return
			}
			for _, b := range l.page.boxes {
				if b.Anchor == "f" && b.Bounds.Dy() > 1 {
					local := point.Sub(shown.offset)
					at := b.Bounds.Min.Add(image.Pt(local.X*b.Bounds.Dx()/shown.image.Bounds().Dx(), local.Y*b.Bounds.Dy()/shown.image.Bounds().Dy()))
					ready <- l.clickFrame(image.Rectangle{Min: at, Max: at.Add(image.Pt(1, 1))})
					return
				}
			}
			ready <- false
		})
		if <-ready {
			break
		}
		select {
		case <-deadline:
			t.Fatal("scrolled child was not presented")
		case <-time.After(10 * time.Millisecond):
		}
	}
	waitLog(t, sink, "scrolled-click")
}

func TestSameOriginWindowScrollAndClamp(t *testing.T) {
	p := externalProfile
	p.Frames = profile.FramesOrigin
	p.FrameDOM = "same-origin"
	l, sink, f := scriptTestLoop(t, p, "https://example.test")
	l.LoadPage(`<iframe id=f height=150 srcdoc="<div style='height:1500px'>tall child</div>"></iframe><script>
 const f=document.getElementById('f');
 f.addEventListener('load',()=>{
  const w=f.contentWindow;
  w.scrollTo(0,100);if(w.scrollY!==100)throw Error('proxy scrollTo');
  w.scrollBy({top:20});if(w.pageYOffset!==120)throw Error('proxy scrollBy');
  w.scrollTo(0,1e9);if(w.scrollY<=120||w.scrollY>=16384)throw Error('upper clamp');
  w.scrollTo(0,-1);if(w.scrollY!==0)throw Error('lower clamp');
  console.log('proxy-scroll-ok');
 });
 </script>`, "https://example.test/", f)
	waitLog(t, sink, "proxy-scroll-ok")
}
