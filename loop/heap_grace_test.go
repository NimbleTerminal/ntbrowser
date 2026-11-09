// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package loop

import (
	"context"
	"runtime"
	"runtime/metrics"
	"testing"
	"time"

	"nimbleterminal.dev/ntbrowser/jsvm"
)

// TestHeapOverrunDropsPageAfterGrace: a page that keeps the process heap above
// the budget is warned, then dropped when the host-scheduled recheck finds it
// still over after the grace, even though the page runs nothing else itself.
func TestHeapOverrunDropsPageAfterGrace(t *testing.T) {
	runtime.GC()
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(sample)
	budget := sample[0].Value.Uint64() + 24<<20
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := make(chanSink, 1024)
	l := New(ctx, Config{Profile: testProfile, Width: 200, Script: jsvm.Config{HeapBudget: budget, HeapGrace: 50 * time.Millisecond, Slice: 30 * time.Second}}, sink)
	go l.Run()
	l.LoadPage(`<body><p>bomb</p><script>globalThis.keep = []; for (let i = 0; i < 1<<15; i++) keep.push(new Array(64).fill(i));</script></body>`, "https://example.test/", nil)
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, killed, disabled := pageState(t, l)
		if killed && disabled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("page not dropped after the grace: killed=%v disabled=%v", killed, disabled)
		}
		time.Sleep(10 * time.Millisecond)
	}
	var warned, errored bool
	timeout := time.After(2 * time.Second)
	for !(warned && errored) {
		select {
		case m := <-sink:
			if lm, ok := m.(LogMsg); ok {
				warned = warned || (lm.Level == "warn" && containsAll(lm.Line, "heap budget exceeded", "stays above"))
				errored = errored || (lm.Level == "error" && containsAll(lm.Line, "scripting disabled: heap budget exceeded for 50ms"))
			}
		case <-timeout:
			t.Fatalf("log lines missing: warned=%v errored=%v", warned, errored)
		}
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !contains(s, p) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})())
}
