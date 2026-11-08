// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import "github.com/dop251/goja"

// installHistory defines an inert window.history. The session history belongs
// to the host (design decision 16) and scripts navigate only through Location
// under the script-navigation gate, so this object carries page-local state
// and nothing else: pushState and replaceState record the state value and
// ignore the URL, length stays 1, back/forward/go do nothing and never
// navigate. It exists so routers and analytics that assume history is present
// run instead of throwing; it is not the History platform (no popstate).
func (s *Sandbox) installHistory() {
	vm := s.vm
	o := vm.NewObject()
	p := vm.NewObject()
	_ = o.SetPrototype(p)
	ctor := vm.ToValue(func(goja.ConstructorCall) *goja.Object { panic(vm.NewTypeError("Illegal constructor")) }).(*goja.Object)
	_ = ctor.Set("prototype", p)
	defineFrozen(p, "constructor", ctor)
	defineFrozen(vm.GlobalObject(), "History", ctor)
	_ = p.DefineDataPropertySymbol(goja.SymToStringTag, vm.ToValue("History"), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE)
	check := func(v goja.Value) {
		if v != o {
			panic(vm.NewTypeError("Illegal invocation"))
		}
	}
	state := goja.Value(goja.Null())
	scrollRestoration := "auto"
	s.accessor(p, "length", func(t goja.Value) goja.Value { check(t); return vm.ToValue(1) }, nil)
	s.accessor(p, "state", func(t goja.Value) goja.Value { check(t); return state }, nil)
	s.accessor(p, "scrollRestoration", func(t goja.Value) goja.Value { check(t); return vm.ToValue(scrollRestoration) }, func(t, v goja.Value) {
		check(t)
		if mode := v.String(); mode == "auto" || mode == "manual" {
			scrollRestoration = mode
		}
	})
	setState := func(c goja.FunctionCall) goja.Value {
		check(c.This)
		if len(c.Arguments) < 2 {
			panic(vm.NewTypeError("state and title required"))
		}
		// The page's own value, kept by reference: it never leaves this realm
		// and the heap guard bounds it like any other retained object.
		state = c.Argument(0)
		return goja.Undefined()
	}
	// Writable, like the platform's: beacons and routers wrap pushState by
	// assigning over it, and that must take effect rather than silently fail.
	// Nothing here reaches the host, so replacing a method costs nothing.
	_ = p.Set("pushState", vm.ToValue(setState))
	_ = p.Set("replaceState", vm.ToValue(setState))
	for _, name := range []string{"back", "forward", "go"} {
		_ = p.Set(name, vm.ToValue(func(c goja.FunctionCall) goja.Value { check(c.This); return goja.Undefined() }))
	}
	defineFrozen(vm.GlobalObject(), "history", o)
}
