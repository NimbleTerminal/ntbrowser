// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package browsercmd

import "testing"

// -seed makes Math.random reproducible across runs; the zero seed is
// replaced by a fixed non-zero state so xorshift never sticks at 0.
func TestSeededRandIsDeterministicAndInRange(t *testing.T) {
	a, b := seededRand(42), seededRand(42)
	for i := 0; i < 1000; i++ {
		x, y := a(), b()
		if x != y {
			t.Fatalf("same seed diverged at %d: %v vs %v", i, x, y)
		}
		if x < 0 || x >= 1 {
			t.Fatalf("out of [0,1): %v", x)
		}
	}
	if seededRand(1)() == seededRand(2)() {
		t.Fatal("different seeds gave the same first value")
	}
	if z := seededRand(0); z() == 0 && z() == 0 {
		t.Fatal("zero seed is stuck")
	}
}
