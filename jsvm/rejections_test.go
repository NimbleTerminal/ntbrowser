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
	if s.rejectionReporting {
		t.Fatal("logger guard retained after interrupt")
	}
}
