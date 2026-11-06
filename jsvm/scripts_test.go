// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestInsertedScriptsLifecycle(t *testing.T) {
	s, tree, _ := formSandbox(t, "")
	defer s.Close()
	s.cfg.DynamicScripts = true
	s.cfg.AllowDynamicCode = true
	imageEval(t, s, `var runs=0;let sc=document.createElement("script");sc.text="runs++";if(runs!==0)throw Error("detached ran");document.head.appendChild(sc);if(runs!==1)throw Error("inline not synchronous");sc.remove();document.body.appendChild(sc);sc.text="runs+=100";`)
	if got := evalString(t, s, `[runs,sc instanceof HTMLScriptElement,sc.async].join()`); got != "1,true,true" {
		t.Fatal(got)
	}
	if strings.Contains(tree.Serialize(), "runs++") || strings.Contains(tree.Serialize(), "<script") {
		t.Fatal("script leaked into render")
	}
	imageEval(t, s, `let empty=document.createElement("script");document.body.appendChild(empty);empty.appendChild(document.createTextNode("runs++"));let holder=document.createElement("div");let nested=document.createElement("script");nested.text="runs++";holder.appendChild(nested);document.body.appendChild(holder);let module=document.createElement("script");module.type="module";module.text="runs+=100";document.body.appendChild(module);`)
	if got := evalString(t, s, `runs`); got != "3" {
		t.Fatal(got)
	}
	// A collected wrapper must not erase a connected element's started state.
	imageEval(t, s, `sc=null;`)
	runtime.GC()
	imageEval(t, s, `document.querySelector("script").text="runs+=1000";`)
	if got := evalString(t, s, `runs`); got != "3" {
		t.Fatal(got)
	}
}

func TestInsertedInlineRequiresDynamicGateAndKeepsWatchdog(t *testing.T) {
	s, _, _ := formSandbox(t, "")
	defer s.Close()
	s.cfg.DynamicScripts = true
	imageEval(t, s, `var ran=false;let a=document.createElement("script");a.text="ran=true";document.body.appendChild(a);`)
	if evalString(t, s, `ran`) != "false" {
		t.Fatal("dynamic-code gate bypassed")
	}
	s.cfg.AllowDynamicCode = true
	s.cfg.Slice = 5 * time.Millisecond
	s.cfg.Strikes = 1
	err := s.Eval("outer.js", `let spin=document.createElement("script");spin.text="for(;;){}";document.body.appendChild(spin)`)
	if err == nil || !s.Disabled() {
		t.Fatalf("watchdog lost: %v disabled=%v", err, s.Disabled())
	}
}

func TestInsertedScriptPendingLimitAndDetachedEvents(t *testing.T) {
	s, _, _ := formSandbox(t, "")
	defer s.Close()
	s.cfg.DynamicScripts = true
	var requests []ScriptRequest
	s.cfg.LoadScript = func(r ScriptRequest) { requests = append(requests, r) }
	imageEval(t, s, `var result=[];var first;for(let i=0;i<32;i++){let sc=document.createElement("script");sc.src="/"+i+".js";sc.onload=function(e){result.push(this===sc&&e.target===sc&&!e.bubbles)};document.body.appendChild(sc);sc.remove();if(i===0)first=sc;}let rejected=false;try{let extra=document.createElement("script");extra.src="/extra.js";document.body.appendChild(extra)}catch(e){rejected=true}if(!rejected)throw Error("pending limit bypassed");`)
	if len(requests) != MaxPendingScripts {
		t.Fatal(len(requests))
	}
	runtime.GC()
	s.CompleteScript(requests[0].ID, "loaded.js", `result.push("executed")`, nil)
	if got := evalString(t, s, `result.join()`); got != "executed,true" {
		t.Fatal(got)
	}
	imageEval(t, s, `let another=document.createElement("script");another.src="/another.js";document.body.appendChild(another);`)
	if len(requests) != MaxPendingScripts+1 {
		t.Fatal("slot not reclaimed")
	}
}

func TestInsertedScriptTotalLimitAndStartedGCState(t *testing.T) {
	s, _, _ := formSandbox(t, "")
	defer s.Close()
	s.cfg.DynamicScripts = true
	s.cfg.AllowDynamicCode = true
	s.cfg.Slice = time.Second
	imageEval(t, s, `var seen=0;for(let i=0;i<256;i++){let sc=document.createElement("script");sc.text="seen++";document.body.appendChild(sc);sc.remove()}let denied=false;try{let sc=document.createElement("script");sc.text="seen++";document.body.appendChild(sc)}catch(e){denied=true}if(!denied||seen!==256)throw Error("total cap bypassed");`)
}

func TestScriptPreparationTriggersAndSnapshot(t *testing.T) {
	s, _, _ := formSandbox(t, "")
	defer s.Close()
	s.cfg.DynamicScripts = true
	s.cfg.AllowDynamicCode = true
	var requests []ScriptRequest
	s.cfg.LoadScript = func(r ScriptRequest) { requests = append(requests, r) }
	imageEval(t, s, `let sc=document.createElement("script");if(!sc.async)throw Error("force async missing");sc.setAttribute("async","");sc.removeAttribute("async");if(sc.async)throw Error("force async not cleared");sc.type="module";sc.src="/first.js";document.head.appendChild(sc);sc.type="text/javascript";`)
	if len(requests) != 0 {
		t.Fatal("type mutation started script")
	}
	imageEval(t, s, `sc.setAttribute("src","/second.js");sc.src="/third.js";sc.remove();document.head.appendChild(sc);`)
	if len(requests) != 1 || requests[0].URL != "/second.js" || requests[0].Async {
		t.Fatalf("preparation snapshot: %+v", requests)
	}
}
