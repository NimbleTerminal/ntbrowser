// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import (
	"strings"

	"github.com/dop251/goja"
	"nimbleterminal.dev/ntbrowser/dom"
)

func (s *Sandbox) eventException(name, message string) *goja.Object {
	e := s.vm.NewObject()
	_ = e.SetPrototype(s.domExceptionProto)
	st := s.record()
	_ = st.Set("kind", "DOMException")
	_ = st.Set("name", name)
	_ = st.Set("message", message)
	codes := map[string]int{"InvalidStateError": 11, "NotSupportedError": 9, "AbortError": 20, "TimeoutError": 23, "DataCloneError": 25, "QuotaExceededError": 22}
	_ = st.Set("code", codes[name])
	s.putPrivate(e, st)
	return e
}
func (s *Sandbox) installDOMException() {
	p := s.vm.NewObject()
	_ = p.SetPrototype(s.vm.Get("Error").(*goja.Object).Get("prototype").(*goja.Object))
	s.domExceptionProto = p
	for _, key := range []string{"name", "message", "code"} {
		s.accessor(p, key, func(t goja.Value) goja.Value { return value(s.brand(t, "DOMException"), key) }, nil)
	}
	ctor := s.vm.ToValue(func(c goja.ConstructorCall) *goja.Object {
		message, name := "", "Error"
		if !goja.IsUndefined(c.Argument(0)) {
			message = c.Argument(0).String()
		}
		if !goja.IsUndefined(c.Argument(1)) {
			name = c.Argument(1).String()
		}
		e := s.eventException(name, message)
		_ = e.SetPrototype(c.This.Prototype())
		return e
	}).(*goja.Object)
	_ = ctor.Set("prototype", p)
	defineFrozen(p, "constructor", ctor)
	defineFrozen(s.vm.GlobalObject(), "DOMException", ctor)
}
func (s *Sandbox) eventDictionary(v goja.Value) *goja.Object {
	if goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}
	if o, ok := v.(*goja.Object); ok {
		return o
	}
	panic(s.vm.NewTypeError("event options must be an object"))
}
func (s *Sandbox) installEventCore() {
	vm := s.vm
	s.extraTargets = map[dom.NodeID]bool{}
	s.abortTimers = map[dom.NodeID]int{}
	s.signalPins = map[dom.NodeID]*goja.Object{}
	var err error
	s.eventPrivate, err = vm.New(vm.Get("WeakMap"))
	if err != nil {
		panic(err)
	}
	s.privateGet, _ = goja.AssertFunction(s.eventPrivate.Get("get"))
	s.privateSet, _ = goja.AssertFunction(s.eventPrivate.Get("set"))
	s.eventTargetProto = vm.NewObject()
	s.bindEventTarget(s.eventTargetProto)
	targetCtor := vm.ToValue(func(c goja.ConstructorCall) *goja.Object { s.registerEventTarget(c.This, 0); return c.This }).(*goja.Object)
	_ = targetCtor.Set("prototype", s.eventTargetProto)
	defineFrozen(s.eventTargetProto, "constructor", targetCtor)
	defineFrozen(vm.GlobalObject(), "EventTarget", targetCtor)
	s.eventProto = vm.NewObject()
	for name, key := range map[string]string{"type": "type", "bubbles": "bubbles", "cancelable": "cancelable", "composed": "composed", "target": "target", "srcElement": "target", "currentTarget": "currentTarget", "eventPhase": "phase", "defaultPrevented": "canceled", "timeStamp": "timestamp", "key": "key", "href": "href"} {
		s.accessor(s.eventProto, name, func(t goja.Value) goja.Value { return value(s.brand(t, "Event"), key) }, nil)
	}
	s.method(s.eventProto, "preventDefault", func(c goja.FunctionCall) goja.Value {
		s.preventEventDefault(s.brand(c.This, "Event"))
		return goja.Undefined()
	})
	s.method(s.eventProto, "stopPropagation", func(c goja.FunctionCall) goja.Value {
		_ = s.brand(c.This, "Event").Set("stop", true)
		return goja.Undefined()
	})
	s.method(s.eventProto, "stopImmediatePropagation", func(c goja.FunctionCall) goja.Value {
		st := s.brand(c.This, "Event")
		_ = st.Set("stop", true)
		_ = st.Set("immediate", true)
		return goja.Undefined()
	})
	s.accessor(s.eventProto, "cancelBubble", func(t goja.Value) goja.Value { return vm.ToValue(flag(s.brand(t, "Event"), "stop")) }, func(t, v goja.Value) {
		if v.ToBoolean() {
			_ = s.brand(t, "Event").Set("stop", true)
		}
	})
	s.accessor(s.eventProto, "returnValue", func(t goja.Value) goja.Value { return vm.ToValue(!flag(s.brand(t, "Event"), "canceled")) }, func(t, v goja.Value) {
		if !v.ToBoolean() {
			s.preventEventDefault(s.brand(t, "Event"))
		}
	})
	s.method(s.eventProto, "composedPath", func(c goja.FunctionCall) goja.Value {
		arr := value(s.brand(c.This, "Event"), "path").(*goja.Object)
		values := arrayValues(arr)
		out := make([]any, len(values))
		for i, v := range values {
			out[i] = v
		}
		return vm.NewArray(out...)
	})
	s.method(s.eventProto, "initEvent", func(c goja.FunctionCall) goja.Value {
		if len(c.Arguments) == 0 {
			panic(vm.NewTypeError("missing event type"))
		}
		s.initEvent(s.brand(c.This, "Event"), c.Argument(0).String(), c.Argument(1).ToBoolean(), c.Argument(2).ToBoolean())
		return goja.Undefined()
	})
	s.customEventProto = vm.NewObject()
	_ = s.customEventProto.SetPrototype(s.eventProto)
	s.accessor(s.customEventProto, "detail", func(t goja.Value) goja.Value { return value(s.brand(t, "Event"), "detail") }, nil)
	s.method(s.customEventProto, "initCustomEvent", func(c goja.FunctionCall) goja.Value {
		if len(c.Arguments) == 0 {
			panic(s.vm.NewTypeError("missing event type"))
		}
		st := s.brand(c.This, "Event")
		if !flag(st, "dispatching") {
			s.initEvent(st, c.Argument(0).String(), c.Argument(1).ToBoolean(), c.Argument(2).ToBoolean())
			detail := c.Argument(3)
			if goja.IsUndefined(detail) {
				detail = goja.Null()
			}
			_ = st.Set("detail", detail)
		}
		return goja.Undefined()
	})
	for _, definition := range []struct {
		name  string
		proto *goja.Object
	}{{"Event", s.eventProto}, {"CustomEvent", s.customEventProto}} {
		ctor := vm.ToValue(func(c goja.ConstructorCall) *goja.Object {
			if len(c.Arguments) == 0 {
				panic(vm.NewTypeError("missing event type"))
			}
			options := s.eventDictionary(c.Argument(1))
			event := s.newEvent(c.Argument(0).String(), flag(options, "bubbles"), flag(options, "cancelable"), flag(options, "composed"), c.This.Prototype())
			if definition.name == "CustomEvent" {
				detail := value(options, "detail")
				if goja.IsUndefined(detail) {
					detail = goja.Null()
				}
				_ = s.brand(event, "Event").Set("detail", detail)
			}
			return event
		}).(*goja.Object)
		_ = ctor.Set("prototype", definition.proto)
		if definition.name == "CustomEvent" {
			_ = ctor.SetPrototype(vm.Get("Event").(*goja.Object))
		}
		defineFrozen(definition.proto, "constructor", ctor)
		defineFrozen(vm.GlobalObject(), definition.name, ctor)
		_ = definition.proto.DefineDataPropertySymbol(goja.SymToStringTag, vm.ToValue(definition.name), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
		if definition.name == "Event" {
			for name, n := range map[string]int{"NONE": 0, "CAPTURING_PHASE": 1, "AT_TARGET": 2, "BUBBLING_PHASE": 3} {
				defineFrozen(ctor, name, vm.ToValue(n))
				defineFrozen(definition.proto, name, vm.ToValue(n))
			}
		}
	}
	s.installDOMException()
	s.installSpecialEvents()
	s.installAbortEvents()
}
func (s *Sandbox) newEvent(typ string, bubbles, cancelable, composed bool, proto *goja.Object) *goja.Object {
	e := s.vm.NewObject()
	_ = e.SetPrototype(proto)
	st := s.record()
	_ = st.Set("kind", "Event")
	s.initEvent(st, typ, bubbles, cancelable)
	_ = st.Set("composed", composed)
	_ = st.Set("timestamp", s.PerformanceNow())
	_ = st.Set("detail", goja.Null())
	_ = st.Set("key", "")
	_ = st.Set("href", "")
	s.putPrivate(e, st)
	s.accessor(e, "isTrusted", func(goja.Value) goja.Value { return s.vm.ToValue(flag(st, "trusted")) }, nil)
	return e
}
func (s *Sandbox) initEvent(st *goja.Object, typ string, bubbles, cancelable bool) {
	if flag(st, "dispatching") {
		return
	}
	for name, v := range map[string]any{"type": typ, "bubbles": bubbles, "cancelable": cancelable, "initialized": true, "trusted": false, "canceled": false, "stop": false, "immediate": false, "phase": 0, "target": goja.Null(), "currentTarget": goja.Null(), "path": s.vm.NewArray()} {
		_ = st.Set(name, v)
	}
}
func (s *Sandbox) preventEventDefault(st *goja.Object) {
	if flag(st, "cancelable") && !flag(st, "passive") {
		_ = st.Set("canceled", true)
	}
}
func (s *Sandbox) createLegacyEvent(name string) goja.Value {
	aliases := map[string]string{"event": "Event", "events": "Event", "htmlevents": "Event", "customevent": "CustomEvent", "mouseevent": "MouseEvent", "mouseevents": "MouseEvent", "uievent": "UIEvent", "uievents": "UIEvent", "keyboardevent": "KeyboardEvent"}
	kind, ok := aliases[strings.ToLower(name)]
	if !ok {
		panic(s.eventException("NotSupportedError", "unsupported event interface"))
	}
	proto := s.specialEventProtos[kind]
	event := s.newEvent("", false, false, false, proto)
	s.initializeSpecialEvent(event, proto, nil)
	_ = s.brand(event, "Event").Set("initialized", false)
	return event
}
func (s *Sandbox) bindEventHandlers(p *goja.Object) {
	for _, typ := range strings.Fields("abort click dblclick auxclick contextmenu keydown keyup keypress mousedown mouseup mousemove mouseover mouseout mouseenter mouseleave pointerdown pointerup pointermove pointercancel touchstart touchmove touchend wheel input beforeinput change submit reset focus blur focusin focusout load error resize scroll message messageerror online offline hashchange popstate visibilitychange readystatechange DOMContentLoaded pageshow pagehide beforeunload unload animationstart animationend transitionend unhandledrejection rejectionhandled") {
		s.accessor(p, "on"+strings.ToLower(typ), func(t goja.Value) goja.Value {
			st := s.brand(t, "EventTarget")
			bag := st.Get("handlers").(*goja.Object)
			record, _ := bag.Get(typ).(*goja.Object)
			if record == nil {
				return goja.Null()
			}
			return value(record, "value")
		}, func(t, v goja.Value) {
			w := t.(*goja.Object)
			if typ == "message" && flag(s.brand(t, "EventTarget"), "messagePort") {
				s.startMessagePort(w)
			}
			bag := s.brand(t, "EventTarget").Get("handlers").(*goja.Object)
			r, _ := bag.Get(typ).(*goja.Object)
			_, callable := goja.AssertFunction(v)
			if r != nil {
				if callable {
					_ = r.Set("value", v)
					return
				}
				s.removeEventListener(w, typ, r.Get("listener"), false)
				_ = bag.Delete(typ)
				return
			}
			if !callable {
				return
			}
			r = s.record()
			_ = r.Set("value", v)
			listener := s.vm.ToValue(func(c goja.FunctionCall) goja.Value {
				fn, ok := goja.AssertFunction(value(r, "value"))
				if !ok {
					return goja.Undefined()
				}
				event := c.Argument(0)
				st := s.brand(event, "Event")
				args := []goja.Value{event}
				windowError := w == s.vm.GlobalObject() && typ == "error" && value(st, "message") != goja.Undefined()
				if windowError {
					args = []goja.Value{value(st, "message"), value(st, "filename"), value(st, "lineno"), value(st, "colno"), value(st, "error")}
				}
				result, err := fn(w, args...)
				if err != nil {
					panic(err)
				}
				if windowError && result.StrictEquals(s.vm.ToValue(true)) || !windowError && result.StrictEquals(s.vm.ToValue(false)) {
					s.preventEventDefault(st)
				}
				return goja.Undefined()
			})
			// Add before publishing the handler slot, so a listener-budget failure is atomic.
			s.addEventListener(w, typ, listener, goja.Undefined())
			_ = r.Set("listener", listener)
			_ = bag.Set(typ, r)
		})
	}
}
