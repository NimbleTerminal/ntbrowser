// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import (
	"github.com/dop251/goja"
	"nimbleterminal.dev/ntbrowser/dom"
	"strings"
	"testing"
	"time"
)

func disclosureSandbox(t *testing.T) (*Sandbox, *messageHost) {
	tree, err := dom.Parse(strings.NewReader(`<details id=a name=g open></details><details id=b name=g></details><div id=p popover></div><div id=q popover></div>`), dom.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	h := &messageHost{tasks: map[int]goja.Callable{}}
	s := New(Config{}, tree, h)
	t.Cleanup(s.Close)
	return s, h
}
func TestDisclosureBindingsAndCoalescedEvents(t *testing.T) {
	s, h := disclosureSandbox(t)
	imageEval(t, s, `const a=document.getElementById('a'),b=document.getElementById('b'),p=document.getElementById('p'),q=document.getElementById('q'); let events=[]; p.ontoggle=e=>events.push(e.oldState+'>'+e.newState); p.showPopover(); p.hidePopover(); p.showPopover(); if(!p.matches(':popover-open')||p.matches(':open'))throw Error('state'); b.open=true; if(a.open||!b.open||b.name!=='g')throw Error('group');`)
	if got := evalString(t, s, `events.length`); got != "0" {
		t.Fatal("synchronous toggle")
	}
	messageStep(t, s, h)
	if got := evalString(t, s, `events.join(',')`); got != "closed>open" {
		t.Fatal(got)
	}
	imageEval(t, s, `q.showPopover();if(p.matches(':popover-open')||!q.matches(':popover-open'))throw Error('auto peers');p.onbeforetoggle=e=>{if(e.newState==='open')e.preventDefault()};p.showPopover();if(p.matches(':popover-open'))throw Error('cancel');`)
	imageEval(t, s, `let invalid='';p.removeAttribute('popover');try{p.showPopover()}catch(e){invalid=e.name};if(invalid!=='InvalidStateError')throw Error(invalid);`)
}
func TestDisclosureSharedCheckpointAndShutdown(t *testing.T) {
	s, h := disclosureSandbox(t)
	b := s.tree.ByID("b")
	imageEval(t, s, `let count=0;document.getElementById('b').ontoggle=()=>count++`)
	if err := s.tree.SetAttr(b, "open", ""); err != nil {
		t.Fatal(err)
	}
	if len(h.order) != 0 {
		t.Fatal("foreign write scheduled task")
	}
	if err := s.FlushMutations(); err != nil {
		t.Fatal(err)
	}
	for len(h.tasks) > 0 {
		messageStep(t, s, h)
	}
	if got := evalString(t, s, `count`); got != "1" {
		t.Fatal(got)
	}
	imageEval(t, s, `document.getElementById('p').showPopover()`)
	s.Close()
	if len(h.tasks) != 0 {
		t.Fatal("task survived shutdown")
	}
}

func TestDisclosureQuotaWatchdogAndDetachedTarget(t *testing.T) {
	s, h := disclosureSandbox(t)
	imageEval(t, s, `const p=document.getElementById('p');let seen=0;p.ontoggle=()=>seen++;p.showPopover();p.remove();`)
	s.Reap()
	for len(h.tasks) > 0 {
		messageStep(t, s, h)
	}
	if got := evalString(t, s, `seen`); got != "1" {
		t.Fatal(got)
	}
	// A saturated timer pool defers scheduling, while native pending data stays bounded.
	h.deny = true
	imageEval(t, s, `for(let i=0;i<300;i++){let d=document.createElement('details');document.body.appendChild(d);d.open=true}`)
	if len(s.disclosurePending) != maxDisclosureToggles || len(h.tasks) != 0 {
		t.Fatalf("pending %d tasks %d", len(s.disclosurePending), len(h.tasks))
	}
	h.deny = false
	if err := s.FlushMutations(); err != nil {
		t.Fatal(err)
	}
	if len(h.tasks) != 1 {
		t.Fatal("expected one shared-budget task")
	}
	s.cfg.Slice = 5 * time.Millisecond
	s.cfg.Strikes = 1
	err := s.Eval("disclosure-watchdog.js", `const q=document.getElementById('q');q.onbeforetoggle=()=>{while(true){}};q.showPopover()`)
	if err == nil || !s.Disabled() {
		t.Fatalf("watchdog failed: %v", err)
	}
}
