// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import "testing"

// TestDefaultRandomSourceIsFreshPerSandbox: without a host-injected source each
// sandbox draws from its own OS-keyed generator, so two pages never share a
// sequence and a run never repeats the last one.
func TestDefaultRandomSourceIsFreshPerSandbox(t *testing.T) {
	draw := func() string {
		s, _ := eventSandbox(t, Config{})
		eventEval(t, s, `globalThis.out=[Math.random(),Math.random(),Math.random(),crypto.randomUUID()].join()`)
		return s.vm.Get("out").String()
	}
	if a, b := draw(), draw(); a == b {
		t.Fatalf("two sandboxes produced the same sequence: %s", a)
	}
}

// TestSeededRandomSourceReplays: the same nonzero seed reproduces the same
// values, in [0, 1), and different seeds diverge.
func TestSeededRandomSourceReplays(t *testing.T) {
	a, b := RandomSource(42), RandomSource(42)
	for i := 0; i < 64; i++ {
		x, y := a(), b()
		if x != y {
			t.Fatalf("same seed diverged at %d: %v vs %v", i, x, y)
		}
		if x < 0 || x >= 1 {
			t.Fatalf("out of range: %v", x)
		}
	}
	if RandomSource(1)() == RandomSource(2)() {
		t.Fatal("different seeds gave the same first value")
	}
	run := func(seed uint64) string {
		s, _ := eventSandbox(t, Config{Rand: RandomSource(seed)})
		eventEval(t, s, `globalThis.out=[Math.random(),crypto.randomUUID(),Array.from(crypto.getRandomValues(new Uint8Array(8))).join('.')].join()`)
		return s.vm.Get("out").String()
	}
	first, again, other := run(7), run(7), run(8) // three separate sandboxes
	if first != again || first == other {
		t.Fatal("seeded sandboxes are not reproducible")
	}
}
