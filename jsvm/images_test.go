// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

func imageEval(t *testing.T, s *Sandbox, src string) {
	t.Helper()
	if err := s.Eval("images.js", src); err != nil {
		t.Fatal(err)
	}
}

func TestImageConstructorAndReflection(t *testing.T) {
	s, _, _ := formSandbox(t, `<img id="parsed" src="/parsed.png">`)
	defer s.Close()
	s.cfg.Location = "https://example.test/path/page"
	imageEval(t, s, `const img = new Image(12, 23); const created = document.createElement("img");`)
	if got := evalString(t, s, `[img instanceof Image, img instanceof HTMLImageElement, created instanceof Image, document.getElementById("parsed") instanceof HTMLImageElement, img.tagName, img.isConnected, img.width, img.height, img.complete, img.src, img.naturalWidth].join("|")`); got != "true|true|true|true|IMG|false|12|23|true||0" {
		t.Fatal(got)
	}
	imageEval(t, s, `img.src = "a.png"; img.alt = "hello"; img.width = -1; img.height = 4294967298;`)
	if got := evalString(t, s, `[img.src,img.getAttribute("src"),img.getAttribute("alt"),img.width,img.height,img.complete].join("|")`); got != "https://example.test/path/a.png|a.png|hello|4294967295|2|false" {
		t.Fatal(got)
	}
}

func TestImageDetachedLifecycleAndDecode(t *testing.T) {
	s, _, _ := formSandbox(t, "")
	defer s.Close()
	imageEval(t, s, `let events=[]; const img=new Image(); img.onload=function(e){events.push([e.type, this===img,e.target===img,img.complete,img.naturalWidth,img.naturalHeight].join(":"));}; img.addEventListener("load",()=>events.push("listener")); img.src="/ok.png"; img.decode().then(()=>events.push("decoded"));`)
	if evalString(t, s, `events.length`) != "0" {
		t.Fatal("synchronous event")
	}
	s.UpdateImages(func(src string) (int, int, bool) {
		if src != "/ok.png" {
			t.Fatal(src)
		}
		return 0, 0, false
	})
	if evalString(t, s, `img.complete`) != "false" {
		t.Fatal("completed before decode")
	}
	s.UpdateImages(func(string) (int, int, bool) { return 3, 4, true })
	if got := evalString(t, s, `events.sort().join("|")`); got != "decoded|listener|load:true:true:true:3:4" {
		t.Fatal(got)
	}
	if got := evalString(t, s, `[img.width,img.height,img.isConnected].join()`); got != "3,4,false" {
		t.Fatal(got)
	}
	s.UpdateImages(func(string) (int, int, bool) { t.Fatal("settled image fetched again"); return 0, 0, true })
}

func TestImageReplacementFailureAndRemoval(t *testing.T) {
	s, _, _ := formSandbox(t, "")
	defer s.Close()
	imageEval(t, s, `let events=[]; const img=new Image(); img.onload=()=>events.push("load"); img.onerror=()=>events.push("error"); img.src="old"; img.decode().catch(e=>events.push(e.name));`)
	s.UpdateImages(func(string) (int, int, bool) { return 0, 0, false })
	imageEval(t, s, `img.setAttribute("SRC","new");`)
	s.UpdateImages(func(src string) (int, int, bool) {
		if src != "new" {
			t.Fatal(src)
		}
		return 0, 0, true
	})
	if got := evalString(t, s, `[events.join(),img.complete,img.naturalWidth].join("|")`); got != "EncodingError,error|true|0" {
		t.Fatal(got)
	}
	imageEval(t, s, `img.src="another"; img.removeAttribute("src"); img.decode().catch(e=>events.push(e.name));`)
	s.UpdateImages(func(string) (int, int, bool) { t.Fatal("removed src fetched"); return 0, 0, true })
	if got := evalString(t, s, `[events.join(),img.complete,img.currentSrc].join("|")`); got != "EncodingError,error,EncodingError|true|" {
		t.Fatal(got)
	}
}

func TestImageHandlersAndDecodeBudget(t *testing.T) {
	s, _, _ := formSandbox(t, "")
	defer s.Close()
	s.cfg.MaxListeners = 2
	imageEval(t, s, `let count=0; const img=new Image(); const fn=()=>count++; img.addEventListener("load",fn); img.onload=fn; img.onload=null; img.src="ok";`)
	s.UpdateImages(func(string) (int, int, bool) { return 1, 1, true })
	if evalString(t, s, `count`) != "1" {
		t.Fatal("handler removal removed listener")
	}
	imageEval(t, s, `img.decode(); img.decode();`)
	err := s.Eval("budget.js", `img.decode()`)
	if err == nil || !strings.Contains(err.Error(), "too many pending image decodes") {
		t.Fatal(err)
	}
	s.UpdateImages(nil)
	imageEval(t, s, `img.decode()`)
}

func TestImageEmptySourceAndNonBubblingEvents(t *testing.T) {
	s, _, _ := formSandbox(t, "")
	defer s.Close()
	imageEval(t, s, `let seen=[];document.addEventListener("error",()=>seen.push("bubble"));const i=new Image();document.body.appendChild(i);i.onerror=e=>{e.preventDefault();seen.push([e.bubbles,e.cancelable,e.defaultPrevented,i.complete].join())};i.src="";`)
	s.UpdateImages(func(string) (int, int, bool) { t.Fatal("empty image src fetched page"); return 0, 0, true })
	if got := evalString(t, s, `seen.join("|")`); got != "false,false,false,true" {
		t.Fatal(got)
	}
}

func TestImageKeepsCurrentImageWhileReplacementLoads(t *testing.T) {
	s, _, _ := formSandbox(t, "")
	defer s.Close()
	imageEval(t, s, `const i=new Image();i.src="old"`)
	s.UpdateImages(func(string) (int, int, bool) { return 2, 3, true })
	imageEval(t, s, `i.src="new"`)
	s.UpdateImages(func(string) (int, int, bool) { return 0, 0, false })
	if got := evalString(t, s, `[i.complete,i.currentSrc,i.naturalWidth,i.naturalHeight].join()`); got != "false,old,2,3" {
		t.Fatal(got)
	}
	s.UpdateImages(func(string) (int, int, bool) { return 4, 5, true })
	if got := evalString(t, s, `[i.complete,i.currentSrc,i.naturalWidth,i.naturalHeight].join()`); got != "true,new,4,5" {
		t.Fatal(got)
	}
}

func TestPendingDetachedImageRetainedUntilCompletion(t *testing.T) {
	s, tree, h := formSandbox(t, "")
	defer s.Close()
	before := tree.Live()
	imageEval(t, s, `(()=>{const i=new Image();i.onload=()=>console.log("survived");i.src="image"})()`)
	s.UpdateImages(func(string) (int, int, bool) { return 0, 0, false })
	runtime.GC()
	s.Reap()
	if tree.Live() != before+1 {
		t.Fatal("pending detached image collected")
	}
	s.UpdateImages(func(string) (int, int, bool) { return 2, 3, true })
	if len(h.lines) != 1 || h.lines[0] != "log: survived" {
		t.Fatal(h.lines)
	}
	deadline := time.Now().Add(patience)
	for tree.Live() > before && time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(5 * time.Millisecond)
		s.Reap()
	}
	if tree.Live() != before || len(s.images) != 0 || s.nListen != 0 {
		t.Fatalf("image leaked after completion: nodes=%d states=%d listeners=%d", tree.Live(), len(s.images), s.nListen)
	}
}

func TestImageRequestOrderAndHandlerReplacement(t *testing.T) {
	s, _, _ := formSandbox(t, "")
	defer s.Close()
	imageEval(t, s, `let events=[];const a=new Image(), b=new Image();a.onload=()=>events.push("old");a.addEventListener("load",()=>events.push("listener"));a.onload=()=>events.push("new");b.src="first";a.src="second";`)
	var order []string
	s.UpdateImages(func(src string) (int, int, bool) { order = append(order, src); return 1, 1, true })
	if strings.Join(order, ",") != "first,second" {
		t.Fatal(order)
	}
	if got := evalString(t, s, `events.join()`); got != "new,listener" {
		t.Fatal(got)
	}
}
