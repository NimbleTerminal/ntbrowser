// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"github.com/dop251/goja"
	weburl "github.com/nlnwa/whatwg-url/url"
)

// Window messaging stays inside this document realm. Capture its actual origin
// once; Location writes, document.domain expandos and payload fields cannot
// change event.origin or authorize another receiver. Opaque origins retain
// same-realm identity for '/', but never match another parsed opaque URL.
func (s *Sandbox) installWindowMessages() {
	vm := s.vm
	global := vm.GlobalObject()
	s.messageOrigin = "null"
	if u := s.locationURL(); u != nil {
		s.messageOrigin = anchorOrigin(u)
	}
	s.method(global, "postMessage", func(c goja.FunctionCall) goja.Value {
		if !goja.IsUndefined(c.This) && !goja.IsNull(c.This) && c.This != global {
			panic(vm.NewTypeError("not a Window"))
		}
		if len(c.Arguments) == 0 {
			panic(vm.NewTypeError("message required"))
		}
		targetValue := vm.ToValue("/")
		transfer := goja.Undefined()
		second := c.Argument(1)
		if len(c.Arguments) < 3 && (goja.IsUndefined(second) || goja.IsNull(second)) {
			// The optional dictionary defaults to an empty dictionary.
		} else if options, ok := second.(*goja.Object); ok && len(c.Arguments) < 3 {
			transfer = value(options, "transfer")
			targetValue = value(options, "targetOrigin")
			if goja.IsUndefined(targetValue) {
				targetValue = vm.ToValue("/")
			}
		} else {
			targetValue = second
			transfer = c.Argument(2)
		}
		target := s.postMessageOriginString(targetValue)
		deliver := target == "*" || target == "/"
		if !deliver {
			parsed, err := weburl.Parse(target)
			if err != nil {
				panic(s.eventException("SyntaxError", "invalid target origin"))
			}
			origin := anchorOrigin(parsed)
			deliver = origin != "null" && origin == s.messageOrigin
		}
		if !goja.IsUndefined(transfer) {
			vm.ForOf(transfer, func(goja.Value) bool {
				panic(s.eventException("DataCloneError", "message transfers are not supported"))
			})
		}
		copied, size := s.cloneMessage(c.Argument(0))
		// Getters invoked during cloning may enqueue recursively. Admission must
		// use the current shared queue totals, including origin-mismatched posts.
		if len(s.portMessages) >= s.cfg.MaxMessages || size > s.cfg.MaxMessageBytes-s.messageBytes {
			s.messageQuota()
		}
		s.ensureMessageTask()
		s.portMessages = append(s.portMessages, portMessage{data: copied, bytes: size, window: true, deliver: deliver})
		s.messageBytes += size
		return goja.Undefined()
	})
}
func (s *Sandbox) postMessageOriginString(v goja.Value) string {
	v = v.ToString()
	if _, ok := v.(*goja.Symbol); ok {
		panic(s.vm.NewTypeError("target origin cannot be a Symbol"))
	}
	if str, ok := v.(goja.String); ok && str.Length() > 2048 {
		panic(s.eventException("SyntaxError", "target origin exceeds 2048 characters"))
	}
	out := v.String()
	if len(out) > 8192 {
		panic(s.eventException("SyntaxError", "target origin too large"))
	}
	return out
}
