// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import (
	"strings"
	"testing"
	"time"
)

func TestPromiseRejectionNativeStack(t *testing.T) {
	s, h := eventSandbox(t, Config{})
	if err := s.Eval("https://example.test/creation.js", `function makeError(){return new Error('xa')} var reason=makeError();Object.defineProperty(reason,'stack',{get(){throw Error('stack getter ran')}});`); err != nil {
		t.Fatal(err)
	}
	if err := s.Eval("https://example.test/reject.js", `Promise.reject(reason);Promise.resolve().then(function callback(){throw new Error('async failure')});Promise.reject('plain failure');`); err != nil {
		t.Fatal(err)
	}
	if len(h.lines) != 3 {
		t.Fatalf("logs: %v", h.lines)
	}
	for i, want := range []string{"makeError (https://example.test/creation.js:1:", "https://example.test/reject.js:1:", "callback (https://example.test/reject.js:1:"} {
		if !strings.Contains(h.lines[i], want) || strings.Contains(h.lines[i], "stack getter ran") {
			t.Fatalf("log %d: %s", i, h.lines[i])
		}
	}
}

func TestPromiseRejectionFormattingBoundaries(t *testing.T) {
	s, h := eventSandbox(t, Config{})
	if err := s.Eval("unsafe\x1b[31m.js", `Promise.reject(new Error('\x1b[31m'+ 'x'.repeat(10000)));Promise.reject({toJSON(){throw Error('no')},toString(){throw Error('no')}});Promise.reject({toJSON(){Promise.reject('nested');return 'outer'}});`); err != nil {
		t.Fatal(err)
	}
	if len(h.lines) != 3 {
		t.Fatalf("recursive/missing logs: %v", h.lines)
	}
	for _, line := range h.lines {
		if len(line) > 4110 || strings.ContainsAny(line, "\x1b\n\r\t") {
			t.Fatalf("unsafe log: %q", line)
		}
	}
	if !strings.Contains(h.lines[0], "unsafe[31m.js:1:") || !strings.Contains(h.lines[1], "<unprintable rejection>") {
		t.Fatal(h.lines)
	}
}

func TestPromiseRejectionFormattingWatchdog(t *testing.T) {
	s, _ := eventSandbox(t, Config{Slice: 10 * time.Millisecond, Strikes: 2})
	if err := s.Eval("spin.js", `Promise.reject({toJSON(){while(true){}}})`); err == nil || s.strikes != 1 {
		t.Fatalf("watchdog: %v strikes=%d", err, s.strikes)
	}
	if s.rejectionReporting || len(s.pendingRejections) != 0 {
		t.Fatal("logger guard retained after interrupt")
	}
}

func TestPromiseRejectionsHandledBeforeCheckpoint(t *testing.T) {
	s, h := eventSandbox(t, Config{})
	if err := s.Eval("handled.js", `
 Promise.reject(Error('direct')).catch(()=>{});
 Promise.reject(Error('google fallback')).then(()=>true).catch(()=>false);
 const p=Promise.reject(Error('microtask'));Promise.resolve().then(()=>p.catch(()=>{}));
 Promise.resolve().then(()=>{throw Error('callback')}).catch(()=>{});
 `); err != nil {
		t.Fatal(err)
	}
	if len(h.lines) != 0 {
		t.Fatalf("handled rejection reported: %v", h.lines)
	}
	if err := s.Eval("unhandled.js", `var late=Promise.reject(Error('real failure'));Promise.reject(Error('propagated')).then(()=>{});`); err != nil {
		t.Fatal(err)
	}
	if len(h.lines) != 2 || !strings.Contains(h.lines[0], "real failure") || !strings.Contains(h.lines[1], "propagated") {
		t.Fatal(h.lines)
	}
	if err := s.Eval("later.js", `late.catch(()=>{});`); err != nil {
		t.Fatal(err)
	}
	if len(h.lines) != 2 || len(s.pendingRejections) != 0 {
		t.Fatal("repeated report or retained promises")
	}
}

func TestPromiseRejectionTrackingBoundedAndReclaimed(t *testing.T) {
	s, h := eventSandbox(t, Config{Slice: time.Second})
	if err := s.Eval("handled-many.js", `for(let i=0;i<1000;i++)Promise.reject(i).catch(()=>{});`); err != nil {
		t.Fatal(err)
	}
	if len(h.lines) != 0 {
		t.Fatal("handled slots not reclaimed", h.lines)
	}
	if err := s.Eval("many.js", `for(let i=0;i<300;i++)Promise.reject(i);`); err != nil {
		t.Fatal(err)
	}
	if len(h.lines) != maxPendingRejections+1 || !strings.Contains(h.lines[len(h.lines)-1], "limit reached") {
		t.Fatalf("log count %d", len(h.lines))
	}
	if len(s.pendingRejections) != 0 || s.rejectionsDropped {
		t.Fatal("retained checkpoint state")
	}
}
