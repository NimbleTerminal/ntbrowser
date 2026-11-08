// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"encoding/hex"

	"github.com/dop251/goja"
)

// maxRandomValuesBytes is the Web Crypto quota for one getRandomValues call.
const maxRandomValuesBytes = 65536

// installCrypto defines crypto.getRandomValues and crypto.randomUUID. Both
// draw from the sandbox's random source, the same one behind Math.random
// (design decision 10): an OS-keyed ChaCha8 generator by default, or the
// host's seeded source for deterministic replays. crypto.subtle is absent so
// pages feature-detect it honestly.
func (s *Sandbox) installCrypto() {
	vm := s.vm
	typedProto := vm.Get("Uint8Array").(*goja.Object).Get("prototype").(*goja.Object).Prototype()
	descriptor, _ := goja.AssertFunction(vm.Get("Object").(*goja.Object).Get("getOwnPropertyDescriptor"))
	getter := func(key goja.Value) goja.Callable {
		d, err := descriptor(goja.Undefined(), typedProto, key)
		if err != nil {
			panic(err)
		}
		f, _ := goja.AssertFunction(d.(*goja.Object).Get("get"))
		return f
	}
	typedBuffer := getter(vm.ToValue("buffer"))
	typedOffset := getter(vm.ToValue("byteOffset"))
	typedLength := getter(vm.ToValue("byteLength"))
	typedTag := getter(goja.SymToStringTag)
	call := func(f goja.Callable, v goja.Value) goja.Value {
		r, err := f(v)
		if err != nil {
			panic(err)
		}
		return r
	}
	integerViews := map[string]bool{"Int8Array": true, "Uint8Array": true, "Uint8ClampedArray": true, "Int16Array": true, "Uint16Array": true, "Int32Array": true, "Uint32Array": true, "BigInt64Array": true, "BigUint64Array": true}

	crypto := vm.NewObject()
	s.method(crypto, "getRandomValues", func(c goja.FunctionCall) goja.Value {
		v := c.Argument(0)
		// Intrinsic getters, never page-controlled fields: a plain object or a
		// Proxy is not a view, and a spoofed buffer property is ignored.
		buffer, err := typedBuffer(v)
		if err != nil {
			panic(vm.NewTypeError("argument must be an integer typed array"))
		}
		if !integerViews[call(typedTag, v).String()] {
			panic(s.eventException("TypeMismatchError", "getRandomValues requires an integer typed array"))
		}
		offset := int(call(typedOffset, v).ToInteger())
		length := int(call(typedLength, v).ToInteger())
		if length > maxRandomValuesBytes {
			panic(s.eventException("QuotaExceededError", "getRandomValues fills at most 65536 bytes"))
		}
		ab, ok := buffer.Export().(goja.ArrayBuffer)
		if !ok || ab.Detached() {
			panic(vm.NewTypeError("detached buffer"))
		}
		s.fillRandom(ab.Bytes()[offset : offset+length])
		return v
	})
	s.method(crypto, "randomUUID", func(goja.FunctionCall) goja.Value {
		var b [16]byte
		s.fillRandom(b[:])
		b[6] = b[6]&0x0f | 0x40 // version 4
		b[8] = b[8]&0x3f | 0x80 // RFC 4122 variant
		var text [36]byte
		hex.Encode(text[0:8], b[0:4])
		hex.Encode(text[9:13], b[4:6])
		hex.Encode(text[14:18], b[6:8])
		hex.Encode(text[19:23], b[8:10])
		hex.Encode(text[24:36], b[10:16])
		text[8], text[13], text[18], text[23] = '-', '-', '-', '-'
		return vm.ToValue(string(text[:]))
	})
	defineFrozen(vm.GlobalObject(), "crypto", crypto)
}

// fillRandom writes bytes from the configured random source, six per draw
// (the low 48 of its 53 bits), so a seeded source reproduces the same bytes.
func (s *Sandbox) fillRandom(dst []byte) {
	for i := 0; i < len(dst); i += 6 {
		u := uint64(s.cfg.Rand() * (1 << 53))
		for j := 0; j < 6 && i+j < len(dst); j++ {
			dst[i+j] = byte(u >> (8 * j))
		}
	}
}
