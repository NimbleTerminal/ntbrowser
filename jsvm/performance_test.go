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
	if got := evalString(t, s, `[performance instanceof Performance,performance.now(),performance.timeOrigin,Date.now(),JSON.stringify(performance).startsWith('{"timeOrigin":1700000000100,"timing":{"navigationStart":1700000000100,')].join("|")`); got != `true|0|1700000000100|1700000000100|true` {
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
	// Guarded legacy-timing access (Google, Cloudflare beacons) finds the
	// coarse origin and nothing finer; request phases we never measure share it.
	imageEval(t, s, `if(!(performance&&performance.timing&&performance.timing.navigationStart===performance.timeOrigin&&performance.timing.requestStart===performance.timeOrigin)){throw Error("legacy timing missing")}`)
}

// TestPerformanceLegacyTimingAndEmptyTimeline: analytics scripts read
// performance.timing.navigationStart and getEntriesByType("navigation")[0]
// unguarded; both resolve without exposing anything finer than the origin.
func TestPerformanceLegacyTimingAndEmptyTimeline(t *testing.T) {
	s, _ := eventSandbox(t, Config{})
	eventEval(t, s, `
 const tm=performance.timing;assert(tm===performance.timing);
 assert(tm.navigationStart===performance.timeOrigin&&tm.fetchStart===tm.navigationStart&&tm.responseEnd===tm.navigationStart);
 assert(tm.loadEventEnd===0&&tm.domComplete===0&&tm.redirectStart===0);
 assert(performance.navigation.type===0&&performance.navigation.redirectCount===0&&performance.navigation.TYPE_BACK_FORWARD===2);
 for(const k of ['navigation','paint','resource','visibility-state']){const e=performance.getEntriesByType(k);assert(Array.isArray(e)&&e.length===0&&e[0]===undefined)}
 assert(performance.getEntries().length===0&&performance.getEntriesByName('x').length===0);
 const j=JSON.parse(JSON.stringify(performance));assert(j.timing.navigationStart===performance.timeOrigin&&j.navigation.type===0);
 let n='';try{Object.getOwnPropertyDescriptor(Performance.prototype,'timing').get.call({})}catch(e){n=e.name}assert(n==='TypeError');
 tm.navigationStart=1;assert(tm.navigationStart===performance.timeOrigin);
 `)
}
