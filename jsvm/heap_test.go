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

// Crossing HeapBudget inside one slice disables the page and says why.
func TestHeapBudgetDisablesPage(t *testing.T) {
	runtime.GC() // a previous iteration's runtime is garbage; measure after it is gone
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(sample)
	budget := sample[0].Value.Uint64() + 24<<20 // 24 MiB above where we stand
	tree, _ := dom.Parse(strings.NewReader("<body></body>"), dom.Limits{})
	host := &nopHost{}
	s := New(Config{HeapBudget: budget, Slice: 30 * time.Second}, tree, host) // the race detector makes the bomb ~10× slower; the slice is not under test
	// The heap metric is process-wide (that is the spec's design: one budget
	// per process, checked per page), so this runtime's garbage must be gone
	// before the next test measures anything: close it and collect.
	t.Cleanup(func() { s.Close(); runtime.GC() })
	// Well over 24 MiB of live objects, kept reachable so the GC cannot help.
	err := s.Eval("bomb.js", `globalThis.keep = []; for (let i = 0; i < 1<<15; i++) keep.push(new Array(64).fill(i));`)
	if err != nil {
		t.Fatalf("the slice itself must finish: %v", err)
	}
	if !s.Disabled() {
		t.Fatal("heap budget exceeded but page not disabled")
	}
	if err := s.Eval("after.js", `1`); err != ErrDisabled {
		t.Fatalf("Eval after heap disable: %v", err)
	}
	if line := strings.Join(host.lines, "\n"); !strings.Contains(line, "heap budget exceeded") || !strings.Contains(line, "process heap") || !strings.Contains(line, "after GC; budget") {
		t.Fatalf("no log line: %v", host.lines)
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
