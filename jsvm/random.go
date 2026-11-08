// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	crand "crypto/rand"
	"encoding/binary"
	"math/rand/v2"
	"sync"
)

// RandomSource returns the generator behind Math.random, crypto.getRandomValues
// and crypto.randomUUID. Seed 0 keys it from the operating system, so every
// page gets its own unpredictable sequence; a nonzero seed reproduces the same
// sequence for replays and tests (the --seed flag). ChaCha8 is Go's
// cryptographically strong generator, so the default is safe for pages that
// use crypto for nonces or identifiers. One source may be shared by several
// sandboxes (frames run on their own goroutines), so draws are serialized.
func RandomSource(seed uint64) func() float64 {
	var key [32]byte
	if seed == 0 {
		_, _ = crand.Read(key[:]) // never fails since Go 1.24; it aborts instead
	} else {
		binary.LittleEndian.PutUint64(key[:8], seed)
	}
	r := rand.New(rand.NewChaCha8(key))
	var mu sync.Mutex
	return func() float64 {
		mu.Lock()
		defer mu.Unlock()
		return r.Float64()
	}
}
