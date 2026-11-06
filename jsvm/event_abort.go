// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import (
	"math"
	"sort"
	"strconv"
	"time"
	"weak"

	"github.com/dop251/goja"
	"nimbleterminal.dev/ntbrowser/dom"
)

func (s *Sandbox) signalState(v goja.Value) *goja.Object {
	st := s.brand(v, "EventTarget")
	if !flag(st, "signal") {
		panic(s.vm.NewTypeError("not an AbortSignal"))
	}
	return st
}
func (s *Sandbox) newSignal() *goja.Object {
	o := s.vm.NewObject()
	_ = o.SetPrototype(s.signalProto)
	s.registerEventTarget(o, 0)
	st := s.private(o)
	_ = st.Set("signal", true)
	_ = st.Set("aborted", false)
	_ = st.Set("reason", goja.Undefined())
	return o
}
func (s *Sandbox) installAbortEvents() {
	vm := s.vm
	s.signalProto = vm.NewObject()
	_ = s.signalProto.SetPrototype(s.eventTargetProto)
	ctor := vm.ToValue(func(goja.ConstructorCall) *goja.Object { panic(vm.NewTypeError("Illegal constructor")) }).(*goja.Object)
	_ = ctor.Set("prototype", s.signalProto)
	_ = ctor.SetPrototype(vm.Get("EventTarget").(*goja.Object))
	defineFrozen(s.signalProto, "constructor", ctor)
	defineFrozen(vm.GlobalObject(), "AbortSignal", ctor)
	s.accessor(s.signalProto, "aborted", func(t goja.Value) goja.Value { return value(s.signalState(t), "aborted") }, nil)
	s.accessor(s.signalProto, "reason", func(t goja.Value) goja.Value { return value(s.signalState(t), "reason") }, nil)
	s.bindEventHandlers(s.signalProto)
	s.method(s.signalProto, "throwIfAborted", func(c goja.FunctionCall) goja.Value {
		st := s.signalState(c.This)
		if flag(st, "aborted") {
			panic(value(st, "reason"))
		}
		return goja.Undefined()
	})
	s.method(ctor, "abort", func(c goja.FunctionCall) goja.Value { o := s.newSignal(); s.abortSignal(o, c.Argument(0)); return o })
	s.method(ctor, "timeout", func(c goja.FunctionCall) goja.Value {
		n := c.Argument(0).ToFloat()
		if len(c.Arguments) == 0 || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > float64(math.MaxInt64/int64(time.Millisecond)) {
			panic(vm.NewTypeError("timeout outside supported duration range"))
		}
		o := s.newSignal()
		wp := weak.Make(o)
		id := dom.NodeID(s.private(o).Get("id").ToInteger())
		timer, ok := s.host.SetTimer(time.Duration(n)*time.Millisecond, false, func(goja.Value, ...goja.Value) (goja.Value, error) {
			delete(s.abortTimers, id)
			if target := wp.Value(); target != nil {
				s.abortSignal(target, s.eventException("TimeoutError", "The operation timed out"))
			}
			return goja.Undefined(), nil
		}, nil)
		if !ok {
			panic(vm.NewTypeError("too many timers"))
		}
		s.abortTimers[id] = timer
		return o
	})
	s.method(ctor, "any", func(c goja.FunctionCall) goja.Value {
		if len(c.Arguments) == 0 {
			panic(vm.NewTypeError("missing signals"))
		}
		sources := vm.NewArray()
		count := 0
		vm.ForOf(c.Argument(0), func(v goja.Value) bool {
			s.signalState(v)
			if count >= s.cfg.MaxListeners {
				panic(vm.NewTypeError("too many abort sources"))
			}
			_ = sources.Set(strconv.Itoa(count), v)
			count++
			return true
		})
		o := s.newSignal()
		for _, v := range arrayValues(sources) {
			if st := s.signalState(v); flag(st, "aborted") {
				s.abortSignal(o, value(st, "reason"))
				return o
			}
		}
		flat := s.vm.NewArray()
		seen := map[*goja.Object]bool{}
		add := func(v goja.Value) {
			key := v.(*goja.Object)
			if seen[key] {
				return
			}
			if len(seen) >= s.cfg.MaxListeners {
				panic(vm.NewTypeError("too many abort sources"))
			}
			_ = flat.Set(strconv.Itoa(len(seen)), v)
			seen[key] = true
		}
		for _, v := range arrayValues(sources) {
			st := s.signalState(v)
			if nested, ok := value(st, "sources").(*goja.Object); ok {
				for _, source := range arrayValues(nested) {
					add(source)
				}
			} else {
				add(v)
			}
		}
		_ = s.private(o).Set("sources", flat)
		return o
	})
	cp := vm.NewObject()
	cc := vm.ToValue(func(c goja.ConstructorCall) *goja.Object {
		st := s.record()
		_ = st.Set("kind", "AbortController")
		_ = st.Set("signal", s.newSignal())
		s.putPrivate(c.This, st)
		return c.This
	}).(*goja.Object)
	_ = cc.Set("prototype", cp)
	defineFrozen(cp, "constructor", cc)
	defineFrozen(vm.GlobalObject(), "AbortController", cc)
	s.accessor(cp, "signal", func(t goja.Value) goja.Value { return value(s.brand(t, "AbortController"), "signal") }, nil)
	s.method(cp, "abort", func(c goja.FunctionCall) goja.Value {
		s.abortSignal(value(s.brand(c.This, "AbortController"), "signal").(*goja.Object), c.Argument(0))
		return goja.Undefined()
	})
}
func (s *Sandbox) abortSignal(signal *goja.Object, reason goja.Value) {
	if flag(s.signalState(signal), "aborted") {
		return
	}
	if goja.IsUndefined(reason) {
		reason = s.eventException("AbortError", "The operation was aborted")
	}
	// Mark all dependents before firing any callback. A nested abort observes the
	// final state; the bounded weak target registry avoids source/child pinning.
	pending := []*goja.Object{signal}
	mark := func(o *goja.Object) {
		st := s.signalState(o)
		_ = st.Set("aborted", true)
		_ = st.Set("reason", reason)
	}
	mark(signal)
	ids := make([]dom.NodeID, 0, len(s.extraTargets))
	for id := range s.extraTargets {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] > ids[j] })
	for _, id := range ids {
		if o := s.weakW[id].Value(); o != nil {
			st := s.private(o)
			if !flag(st, "signal") || flag(st, "aborted") {
				continue
			}
			sources, _ := value(st, "sources").(*goja.Object)
			for _, source := range arrayValues(sources) {
				if source == signal {
					mark(o)
					pending = append(pending, o)
					break
				}
			}
		}
	}
	for _, aborting := range pending {
		for id := range s.listenCount {
			if o := s.weakW[id].Value(); o != nil {
				st := s.private(o)
				bag, _ := value(st, "listeners").(*goja.Object)
				if bag == nil {
					continue
				}
				for _, typ := range bag.Keys() {
					for _, v := range arrayValues(s.listenerList(o, typ, false)) {
						r := v.(*goja.Object)
						sig := value(r, "signal")
						if sig == aborting {
							s.removeRecord(o, typ, r)
						}
					}
				}
			}
		}
		o := aborting
		st := s.signalState(o)
		_ = st.Delete("sources")
		s.refreshSignalPin(o)
		e := s.newEvent("abort", false, false, false, s.eventProto)
		_ = s.private(e).Set("trusted", true)
		if _, err := s.dispatchEvent(o, e); err != nil {
			panic(err)
		}
	}
}

// A pending timeout or dependent signal with abort listeners must survive even
// after its JS wrapper is dropped. Pins are bounded by the listener/target caps.
func (s *Sandbox) refreshSignalPin(o *goja.Object) {
	st := s.private(o)
	if !flag(st, "signal") {
		return
	}
	id := dom.NodeID(value(st, "id").ToInteger())
	sources, _ := value(st, "sources").(*goja.Object)
	_, timed := s.abortTimers[id]
	needed := !flag(st, "aborted") && s.listenCount[id]["abort"] > 0 && (timed || sources != nil && len(arrayValues(sources)) > 0)
	if needed {
		s.signalPins[id] = o
	} else {
		delete(s.signalPins, id)
	}
}
