// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"strings"
	"testing"
	"time"

	"nimbleterminal.dev/ntbrowser/dom"
)

// heapBomb is a sandbox 24 MiB below a budget the page can cross with retained
// objects, with a short grace so tests can wait it out.
func heapBomb(t *testing.T, grace time.Duration) (*Sandbox, *nopHost) {
	t.Helper()
	runtime.GC() // a previous iteration's runtime is garbage; measure after it is gone
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(sample)
	budget := sample[0].Value.Uint64() + 24<<20 // 24 MiB above where we stand
	tree, _ := dom.Parse(strings.NewReader("<body></body>"), dom.Limits{})
	host := &nopHost{}
	s := New(Config{HeapBudget: budget, HeapGrace: grace, Slice: 30 * time.Second}, tree, host) // the race detector makes the bomb ~10× slower; the slice is not under test
	// The heap metric is process-wide (that is the spec's design: one budget
	// per process, checked per page), so this runtime's garbage must be gone
	// before the next test measures anything: close it and collect.
	t.Cleanup(func() { s.Close(); runtime.GC() })
	// Well over 24 MiB of live objects, kept reachable so the GC cannot help.
	if err := s.Eval("bomb.js", `globalThis.keep = []; for (let i = 0; i < 1<<15; i++) keep.push(new Array(64).fill(i));`); err != nil {
		t.Fatalf("the slice itself must finish: %v", err)
	}
	return s, host
}

// Crossing HeapBudget starts a grace: the page is warned, the host is asked to
// recheck after HeapGrace, and a page still over budget then is disabled and
// says why. Nothing is disabled on the first high sample alone.
func TestHeapBudgetDisablesPageAfterGrace(t *testing.T) {
	// Long enough that no runner, however slow, leaves it by accident; the
	// test ends the grace itself by moving its start back.
	const grace = time.Minute
	s, host := heapBomb(t, grace)
	if s.Disabled() {
		t.Fatal("page disabled on the first over-budget sample")
	}
	if host.later == nil || host.laterDelay != grace {
		t.Fatalf("no host recheck scheduled for the grace: delay=%v", host.laterDelay)
	}
	if line := strings.Join(host.lines, "\n"); !strings.Contains(line, "warn: heap budget exceeded") || !strings.Contains(line, "stays above the budget for 1m0s") {
		t.Fatalf("no warning: %v", host.lines)
	}
	// Still over budget inside the grace: another entry does not disable yet.
	if err := s.Eval("during.js", `1`); err != nil || s.Disabled() {
		t.Fatalf("disabled inside the grace: err=%v disabled=%v", err, s.Disabled())
	}
	s.heapOver = s.heapOver.Add(-grace)
	host.later()
	if !s.Disabled() {
		t.Fatal("heap budget exceeded for the whole grace but page not disabled")
	}
	if err := s.Eval("after.js", `1`); err != ErrDisabled {
		t.Fatalf("Eval after heap disable: %v", err)
	}
	if line := strings.Join(host.lines, "\n"); !strings.Contains(line, "error: scripting disabled: heap budget exceeded for 1m0s") || !strings.Contains(line, "process heap") || !strings.Contains(line, "after GC; budget") {
		t.Fatalf("no log line: %v", host.lines)
	}
}

// A sample back under budget before the grace ends is forgiven: the host's own
// frame encoding inflates the process heap for a few hundred milliseconds and
// must not disable a healthy page. The recheck then finds nothing to do, and a
// later overrun starts a fresh grace instead of inheriting the old start.
func TestHeapBudgetTransientOverrunIsForgiven(t *testing.T) {
	const grace = time.Minute
	s, host := heapBomb(t, grace)
	if err := s.Eval("release.js", `globalThis.keep = null`); err != nil {
		t.Fatal(err)
	}
	if s.Disabled() || !s.heapOver.IsZero() {
		t.Fatalf("heap back under budget but grace still running: disabled=%v", s.Disabled())
	}
	host.later() // the recheck, whenever it comes, finds nothing to do
	if s.Disabled() {
		t.Fatalf("recheck disabled a page that was back under budget: %v", host.lines)
	}
	for _, line := range host.lines {
		if strings.HasPrefix(line, "error:") {
			t.Fatalf("error reported for a transient overrun: %v", host.lines)
		}
	}
	// A new overrun long after the first starts its own grace.
	host.later = nil
	if err := s.Eval("bomb2.js", `globalThis.keep = []; for (let i = 0; i < 1<<15; i++) keep.push(new Array(64).fill(i));`); err != nil {
		t.Fatal(err)
	}
	if s.Disabled() || host.later == nil {
		t.Fatalf("second overrun: disabled=%v rescheduled=%v", s.Disabled(), host.later != nil)
	}
}

// Rendering and navigation can leave large unreachable Go allocations between
// collections. Crossing the sampled threshold must not kill a healthy runtime.
func TestHeapBudgetCollectsGarbageBeforeDisabling(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader("<body></body>"), dom.Limits{})
	host := &nopHost{}
	s := New(Config{}, tree, host)
	t.Cleanup(func() { s.Close(); runtime.GC() })
	runtime.GC()
	s.cfg.HeapBudget = processHeapBytes() + (16 << 20)
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	allocateDiscardedHeap()
	if got := processHeapBytes(); got <= s.cfg.HeapBudget {
		t.Fatalf("test did not cross threshold: %d <= %d", got, s.cfg.HeapBudget)
	}
	if err := s.Eval("healthy.js", `globalThis.answer=42`); err != nil {
		t.Fatal(err)
	}
	if s.Disabled() {
		t.Fatalf("garbage killed healthy page: %v", host.lines)
	}
	if err := s.Eval("still-alive.js", `if(answer!==42) throw new Error("lost runtime")`); err != nil {
		t.Fatal(err)
	}
}

//go:noinline
func allocateDiscardedHeap() {
	b := make([]byte, 48<<20)
	for i := 0; i < len(b); i += 4096 {
		b[i] = 1
	}
	runtime.KeepAlive(b)
}
