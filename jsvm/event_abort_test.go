// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import (
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
	"nimbleterminal.dev/ntbrowser/dom"
)

type abortTimerHost struct {
	nopHost
	fn      goja.Callable
	delay   time.Duration
	cleared int
	deny    bool
}

func (h *abortTimerHost) SetTimer(d time.Duration, repeat bool, f goja.Callable, args []goja.Value) (int, bool) {
	if h.deny {
		return 0, false
	}
	h.delay = d
	h.fn = f
	return 7, true
}
func (h *abortTimerHost) ClearTimer(id int) { h.cleared = id }
func TestEventAbortTimeout(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader("<p>x</p>"), dom.Limits{})
	h := &abortTimerHost{}
	s := New(Config{}, tree, h)
	defer s.Close()
	eventEval(t, s, `const timeout=AbortSignal.timeout(25);let fired=0;timeout.addEventListener('abort',()=>fired++);assert(!timeout.aborted)`)
	if h.delay != 25*time.Millisecond || h.fn == nil {
		t.Fatal("timer not scheduled")
	}
	if err := s.Call(h.fn, goja.Undefined()); err != nil {
		t.Fatal(err)
	}
	eventEval(t, s, `assert(timeout.aborted&&timeout.reason.name==='TimeoutError'&&fired===1)`)
	h.deny = true
	eventEval(t, s, `let denied=false;try{AbortSignal.timeout(0)}catch(e){denied=true};assert(denied)`)
	h.deny = false
	eventEval(t, s, `globalThis.pending=AbortSignal.timeout(1000)`)
	s.Close()
	if h.cleared != 7 {
		t.Fatal("Close did not clear timeout")
	}
}

func TestEventAbortSignalsStayAliveWhileObserved(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader("<p>x</p>"), dom.Limits{})
	h := &abortTimerHost{}
	s := New(Config{}, tree, h)
	defer s.Close()
	eventEval(t, s, `let fires=0;const source=new AbortController();(function(){AbortSignal.timeout(5).onabort=()=>fires++;AbortSignal.any([source.signal]).onabort=()=>fires++})()`)
	s.collectNow()
	if len(s.signalPins) != 2 {
		t.Fatalf("expected observed signal pins: %d", len(s.signalPins))
	}
	if err := s.Call(h.fn, goja.Undefined()); err != nil {
		t.Fatal(err)
	}
	eventEval(t, s, `source.abort();assert(fires===2)`)
	if len(s.signalPins) != 0 {
		t.Fatal("aborted signals still pinned")
	}
}
