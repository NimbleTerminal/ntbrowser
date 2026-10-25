package jsvm

import (
	"runtime"
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
	if !strings.Contains(strings.Join(host.lines, "\n"), "heap budget exceeded") {
		t.Fatalf("no log line: %v", host.lines)
	}
}
