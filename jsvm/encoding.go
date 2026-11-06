// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/dop251/goja"
)

// Native conversion loops cannot be interrupted by Goja. Bound input before
// walking it and output before allocation; streaming state is private to the VM.
const defaultCodecLimit = 2 << 20

func (s *Sandbox) installEncoding() {
	vm := s.vm
	uint8Ctor := vm.Get("Uint8Array")
	typedProto := uint8Ctor.(*goja.Object).Get("prototype").(*goja.Object).Prototype()
	dataProto := vm.Get("DataView").(*goja.Object).Get("prototype").(*goja.Object)
	bufferProto := vm.Get("ArrayBuffer").(*goja.Object).Get("prototype").(*goja.Object)
	descriptor, _ := goja.AssertFunction(vm.Get("Object").(*goja.Object).Get("getOwnPropertyDescriptor"))
	getter := func(proto *goja.Object, key goja.Value) goja.Callable {
		d, err := descriptor(goja.Undefined(), proto, key)
		if err != nil {
			panic(err)
		}
		f, _ := goja.AssertFunction(d.(*goja.Object).Get("get"))
		return f
	}
	typedBuffer := getter(typedProto, vm.ToValue("buffer"))
	typedOffset := getter(typedProto, vm.ToValue("byteOffset"))
	typedLength := getter(typedProto, vm.ToValue("byteLength"))
	typedTag := getter(typedProto, goja.SymToStringTag)
	dataBuffer := getter(dataProto, vm.ToValue("buffer"))
	dataOffset := getter(dataProto, vm.ToValue("byteOffset"))
	dataLength := getter(dataProto, vm.ToValue("byteLength"))
	bufferLength := getter(bufferProto, vm.ToValue("byteLength"))
	rangeCtor := vm.Get("RangeError")
	failLimit := func() {
		e, err := vm.New(rangeCtor, vm.ToValue("text conversion limit exceeded"))
		if err != nil {
			panic(err)
		}
		panic(e)
	}
	call := func(f goja.Callable, v goja.Value) goja.Value {
		r, err := f(v)
		if err != nil {
			panic(err)
		}
		return r
	}
	// Call captured intrinsic getters, never page-controlled buffer/offset fields
	// or Export on an unvalidated object. Subviews expose precisely their bytes.
	bytesOf := func(v goja.Value, uint8Only bool) []byte {
		var buffer goja.Value
		offset, length := 0, 0
		if uint8Only {
			if call(typedTag, v).String() != "Uint8Array" {
				panic(vm.NewTypeError("destination must be Uint8Array"))
			}
		}
		if n, err := bufferLength(v); err == nil && !uint8Only {
			buffer = v
			length = int(n.ToInteger())
		} else if b, err := typedBuffer(v); err == nil {
			buffer = b
			offset = int(call(typedOffset, v).ToInteger())
			length = int(call(typedLength, v).ToInteger())
		} else if !uint8Only {
			b, err := dataBuffer(v)
			if err != nil {
				panic(vm.NewTypeError("input must be ArrayBuffer or ArrayBufferView"))
			}
			buffer = b
			offset = int(call(dataOffset, v).ToInteger())
			length = int(call(dataLength, v).ToInteger())
		} else {
			panic(vm.NewTypeError("destination must be Uint8Array"))
		}
		ab, ok := buffer.Export().(goja.ArrayBuffer)
		if !ok || ab.Detached() {
			panic(vm.NewTypeError("detached buffer"))
		}
		return ab.Bytes()[offset : offset+length]
	}
	stringOf := func(v goja.Value, optional bool) goja.String {
		if optional && goja.IsUndefined(v) {
			v = vm.ToValue("")
		}
		converted := v.ToString()
		if _, ok := converted.(*goja.Symbol); ok {
			panic(vm.NewTypeError("cannot convert Symbol to string"))
		}
		str, ok := converted.(goja.String)
		if !ok {
			str = vm.ToValue(converted.String()).(goja.String)
		}
		if str.Length() > s.cfg.MaxCodecBytes {
			failLimit()
		}
		return str
	}
	ep := vm.NewObject()
	ec := vm.ToValue(func(c goja.ConstructorCall) *goja.Object {
		st := s.record()
		_ = st.Set("kind", "TextEncoder")
		s.putPrivate(c.This, st)
		return c.This
	}).(*goja.Object)
	_ = ec.Set("prototype", ep)
	defineFrozen(ep, "constructor", ec)
	defineFrozen(vm.GlobalObject(), "TextEncoder", ec)
	_ = ep.DefineDataPropertySymbol(goja.SymToStringTag, vm.ToValue("TextEncoder"), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	s.accessor(ep, "encoding", func(t goja.Value) goja.Value { s.brand(t, "TextEncoder"); return vm.ToValue("utf-8") }, nil)
	s.method(ep, "encode", func(c goja.FunctionCall) goja.Value {
		s.brand(c.This, "TextEncoder")
		str := stringOf(c.Argument(0), true)
		size := 0
		for i := 0; i < str.Length(); {
			r, n := scalarAt(str, i)
			size += utf8.RuneLen(r)
			if size > s.cfg.MaxCodecBytes {
				failLimit()
			}
			i += n
		}
		out := make([]byte, 0, size)
		for i := 0; i < str.Length(); {
			r, n := scalarAt(str, i)
			out = utf8.AppendRune(out, r)
			i += n
		}
		obj, err := vm.New(uint8Ctor, vm.ToValue(vm.NewArrayBuffer(out)))
		if err != nil {
			panic(err)
		}
		return obj
	})
	s.method(ep, "encodeInto", func(c goja.FunctionCall) goja.Value {
		s.brand(c.This, "TextEncoder")
		if len(c.Arguments) < 2 {
			panic(vm.NewTypeError("source and destination required"))
		}
		str := stringOf(c.Argument(0), false)
		dst := bytesOf(c.Argument(1), true)
		read, written := 0, 0
		// Preflight so exceeding the native-work cap never partly changes dst.
		for read < str.Length() {
			r, n := scalarAt(str, read)
			size := utf8.RuneLen(r)
			if size > len(dst)-written {
				break
			}
			if written+size > s.cfg.MaxCodecBytes {
				failLimit()
			}
			written += size
			read += n
		}
		for i, pos := 0, 0; i < read; {
			r, n := scalarAt(str, i)
			pos += utf8.EncodeRune(dst[pos:], r)
			i += n
		}

		out := vm.NewObject()
		_ = out.Set("read", read)
		_ = out.Set("written", written)
		return out
	})
	dp := vm.NewObject()
	dc := vm.ToValue(func(c goja.ConstructorCall) *goja.Object {
		label := "utf-8"
		if !goja.IsUndefined(c.Argument(0)) {
			str := stringOf(c.Argument(0), false)
			label = str.String()
		}
		label = strings.ToLower(strings.Trim(label, " \t\n\r\f"))
		switch label {
		case "unicode-1-1-utf-8", "unicode11utf8", "unicode20utf8", "utf-8", "utf8", "x-unicode20utf8":
			label = "utf-8"
		case "utf-16", "utf-16le", "unicode", "ucs-2", "csunicode", "iso-10646-ucs-2", "unicodefeff":
			label = "utf-16le"
		case "utf-16be", "unicodefffe":
			label = "utf-16be"
		default:
			e, err := vm.New(rangeCtor, vm.ToValue("unsupported text encoding"))
			if err != nil {
				panic(err)
			}
			panic(e)
		}
		options := s.eventDictionary(c.Argument(1))
		st := s.record()
		_ = st.Set("kind", "TextDecoder")
		_ = st.Set("encoding", label)
		_ = st.Set("fatal", flag(options, "fatal"))
		_ = st.Set("ignoreBOM", flag(options, "ignoreBOM"))
		s.putPrivate(c.This, st)
		return c.This
	}).(*goja.Object)
	_ = dc.Set("prototype", dp)
	defineFrozen(dp, "constructor", dc)
	defineFrozen(vm.GlobalObject(), "TextDecoder", dc)
	_ = dp.DefineDataPropertySymbol(goja.SymToStringTag, vm.ToValue("TextDecoder"), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	for _, key := range []string{"encoding", "fatal", "ignoreBOM"} {
		s.accessor(dp, key, func(t goja.Value) goja.Value { return value(s.brand(t, "TextDecoder"), key) }, nil)
	}
	s.method(dp, "decode", func(c goja.FunctionCall) goja.Value {
		st := s.brand(c.This, "TextDecoder")
		var input []byte
		if !goja.IsUndefined(c.Argument(0)) {
			input = bytesOf(c.Argument(0), false)
		}
		options := s.eventDictionary(c.Argument(1))
		stream := flag(options, "stream")
		// Options can run JS getters (including recursive decode). Read state and
		// revalidate buffers after conversion before touching their backing storage.
		if !goja.IsUndefined(c.Argument(0)) {
			input = bytesOf(c.Argument(0), false)
		}
		var pending []byte
		seen := false
		if flag(st, "stream") {
			seen = flag(st, "bomSeen")
			if p := value(st, "pending"); !goja.IsUndefined(p) {
				pending = p.Export().(goja.ArrayBuffer).Bytes()
			}
		}
		if len(input) > s.cfg.MaxCodecBytes-len(pending) {
			failLimit()
		}
		data := make([]byte, 0, len(pending)+len(input))
		data = append(data, pending...)
		data = append(data, input...)
		out := make([]byte, 0, len(data))
		pos := 0
		fatal := false
		for pos < len(data) {
			r, n, bad := decodeScalar(data[pos:], value(st, "encoding").String(), !stream)
			if n == 0 {
				break
			}
			pos += n
			if bad && flag(st, "fatal") {
				fatal = true
				break
			}
			if !seen {
				seen = true
				if r == '\uFEFF' && !flag(st, "ignoreBOM") {
					continue
				}
			}
			if len(out)+utf8.RuneLen(r) > s.cfg.MaxCodecBytes {
				failLimit()
			}
			out = utf8.AppendRune(out, r)
		}
		_ = st.Set("stream", stream)
		_ = st.Set("bomSeen", seen)
		_ = st.Set("pending", vm.NewArrayBuffer(append([]byte(nil), data[pos:]...)))
		if fatal {
			panic(vm.NewTypeError("invalid encoded data"))
		}
		return vm.ToValue(string(out))
	})
}

// scalarAt implements USVString conversion without allocating a full copy of
// the input: isolated UTF-16 surrogates become one replacement character.
func scalarAt(s goja.String, i int) (rune, int) {
	a := s.CharAt(i)
	if a >= 0xD800 && a <= 0xDBFF && i+1 < s.Length() {
		b := s.CharAt(i + 1)
		if b >= 0xDC00 && b <= 0xDFFF {
			return utf16.DecodeRune(rune(a), rune(b)), 2
		}
	}
	if a >= 0xD800 && a <= 0xDFFF {
		return utf8.RuneError, 1
	}
	return rune(a), 1
}

// decodeScalar consumes a maximal valid prefix on error (not one replacement
// per byte), and retains incomplete sequences across streaming calls.
func decodeScalar(b []byte, encoding string, final bool) (r rune, n int, bad bool) {
	if encoding != "utf-8" {
		unit := func(p []byte) uint16 {
			if encoding == "utf-16le" {
				return uint16(p[0]) | uint16(p[1])<<8
			}
			return uint16(p[0])<<8 | uint16(p[1])
		}
		if len(b) < 2 {
			if !final {
				return 0, 0, false
			}
			return utf8.RuneError, 1, true
		}
		a := unit(b)
		if a >= 0xD800 && a <= 0xDBFF {
			if len(b) < 4 {
				if !final {
					return 0, 0, false
				}
				return utf8.RuneError, len(b), true
			}
			z := unit(b[2:])
			if z >= 0xDC00 && z <= 0xDFFF {
				return utf16.DecodeRune(rune(a), rune(z)), 4, false
			}
			return utf8.RuneError, 2, true
		}
		if a >= 0xDC00 && a <= 0xDFFF {
			return utf8.RuneError, 2, true
		}
		return rune(a), 2, false
	}
	a := b[0]
	if a < 0x80 {
		return rune(a), 1, false
	}
	need := 0
	lo, hi := byte(0x80), byte(0xBF)
	switch {
	case a >= 0xC2 && a <= 0xDF:
		need = 2
		r = rune(a & 0x1F)
	case a >= 0xE0 && a <= 0xEF:
		need = 3
		r = rune(a & 0x0F)
		if a == 0xE0 {
			lo = 0xA0
		}
		if a == 0xED {
			hi = 0x9F
		}
	case a >= 0xF0 && a <= 0xF4:
		need = 4
		r = rune(a & 7)
		if a == 0xF0 {
			lo = 0x90
		}
		if a == 0xF4 {
			hi = 0x8F
		}
	default:
		return utf8.RuneError, 1, true
	}
	for i := 1; i < need; i++ {
		if i == len(b) {
			if !final {
				return 0, 0, false
			}
			return utf8.RuneError, i, true
		}
		if b[i] < lo || b[i] > hi {
			return utf8.RuneError, i, true
		}
		r = r<<6 | rune(b[i]&0x3F)
		lo, hi = 0x80, 0xBF
	}
	return r, need, false
}
