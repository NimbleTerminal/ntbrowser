// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"strings"
	"testing"
	"time"

	"nimbleterminal.dev/ntbrowser/dom"
)

func TestPerformanceCoarseClockAndStableOrigin(t *testing.T) {
	current := time.Unix(1700000000, 123000000)
	tree, _ := dom.Parse(strings.NewReader(""), dom.Limits{})
	s := New(Config{Clock: func() time.Time { return current }, ClockResolution: 100 * time.Millisecond}, tree, &nopHost{})
	defer s.Close()
	if got := evalString(t, s, `[performance instanceof Performance,performance.now(),performance.timeOrigin,Date.now(),JSON.stringify(performance)].join("|")`); got != `true|0|1700000000100|1700000000100|{"timeOrigin":1700000000100}` {
		t.Fatal(got)
	}
	current = current.Add(76 * time.Millisecond)
	if got := evalString(t, s, `performance.now()`); got != "0" {
		t.Fatal(got)
	}
	current = current.Add(time.Millisecond)
	if got := evalString(t, s, `[performance.now(),Date.now()-performance.timeOrigin].join()`); got != "100,100" {
		t.Fatal(got)
	}
	current = current.Add(-time.Hour)
	if got := evalString(t, s, `performance.now()`); got != "100" {
		t.Fatalf("clock moved backwards: %s", got)
	}
	imageEval(t, s, `Date.now=()=>99;performance.timeOrigin=42;`)
	if got := evalString(t, s, `[performance.now(),performance.timeOrigin].join()`); got != "100,1700000000100" {
		t.Fatal(got)
	}
}

func TestPerformanceDefaultResolutionAndReceiver(t *testing.T) {
	current := time.Unix(1700000000, 0)
	tree, _ := dom.Parse(strings.NewReader(""), dom.Limits{})
	s := New(Config{Clock: func() time.Time { return current }}, tree, &nopHost{})
	defer s.Close()
	current = current.Add(9 * time.Millisecond)
	if evalString(t, s, `performance.now()`) != "0" {
		t.Fatal("default clock finer than 10ms")
	}
	current = current.Add(time.Millisecond)
	if evalString(t, s, `performance.now()`) != "10" {
		t.Fatal("default clock did not advance")
	}
	imageEval(t, s, `let errors=0;try{new Performance()}catch(e){errors++};try{performance.now.call({})}catch(e){errors++}`)
	if evalString(t, s, `errors`) != "2" {
		t.Fatal("illegal invocation accepted")
	}
	// Google's guarded legacy-timing access must be harmless without inventing
	// request timing values we do not collect.
	imageEval(t, s, `if(performance&&performance.timing){throw Error("unexpected legacy timing")}`)
}
