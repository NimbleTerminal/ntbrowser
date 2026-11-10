// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import (
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"weak"

	"github.com/dop251/goja"
	"nimbleterminal.dev/ntbrowser/dom"
)

func value(o *goja.Object, key string) goja.Value {
	if o == nil {
		return goja.Undefined()
	}
	v := o.Get(key)
	if v == nil {
		return goja.Undefined()
	}
	return v
}
func flag(o *goja.Object, key string) bool { return value(o, key).ToBoolean() }
func (s *Sandbox) record() *goja.Object    { o := s.vm.NewObject(); _ = o.SetPrototype(nil); return o }
func (s *Sandbox) private(v goja.Value) *goja.Object {
	r, err := s.privateGet(s.eventPrivate, v)
	if err != nil {
		panic(err)
	}
	o, _ := r.(*goja.Object)
	return o
}
func (s *Sandbox) putPrivate(key goja.Value, state *goja.Object) {
	if _, err := s.privateSet(s.eventPrivate, key, state); err != nil {
		panic(err)
	}
}
func (s *Sandbox) brand(v goja.Value, kind string) *goja.Object {
	state := s.private(v)
	if state == nil || value(state, "kind").String() != kind {
		panic(s.vm.NewTypeError("not a " + kind))
	}
	return state
}
func (s *Sandbox) registerEventTarget(w *goja.Object, id dom.NodeID) {
	if s.private(w) != nil {
		return
	}
	if id == 0 {
		if len(s.extraTargets) >= s.cfg.MaxListeners {
			s.collectNow()
			if len(s.extraTargets) >= s.cfg.MaxListeners {
				panic(s.vm.NewTypeError("too many EventTargets"))
			}
		}
		s.extraTargetNext++
		id = -s.extraTargetNext
		s.extraTargets[id] = true
		s.weakW[id] = weak.Make(w)
		runtime.AddCleanup(w, s.collectedCleanup, id)
	}
	state := s.record()
	_ = state.Set("kind", "EventTarget")
	_ = state.Set("id", int64(id))
	_ = state.Set("listeners", s.record())
	_ = state.Set("handlers", s.record())
	s.putPrivate(w, state)
}
func (s *Sandbox) listenerList(w *goja.Object, typ string, create bool) *goja.Object {
	state := s.brand(w, "EventTarget")
	bag := state.Get("listeners").(*goja.Object)
	arr, _ := bag.Get(typ).(*goja.Object)
	if arr == nil && create {
		arr = s.vm.NewArray()
		_ = arr.SetPrototype(nil)
		_ = bag.Set(typ, arr)
	}
	return arr
}
func arrayValues(arr *goja.Object) []goja.Value {
	if arr == nil {
		return nil
	}
	n := int(arr.Get("length").ToInteger())
	out := make([]goja.Value, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, arr.Get(strconv.Itoa(i)))
	}
	return out
}
func (s *Sandbox) eventTargetThis(v goja.Value) *goja.Object {
	if goja.IsNull(v) || goja.IsUndefined(v) {
		v = s.vm.GlobalObject()
	}
	s.brand(v, "EventTarget")
	return v.(*goja.Object)
}
func (s *Sandbox) bindEventTarget(p *goja.Object) {
	s.method(p, "addEventListener", func(c goja.FunctionCall) goja.Value {
		target := s.eventTargetThis(c.This)
		if len(c.Arguments) < 2 {
			panic(s.vm.NewTypeError("missing event listener arguments"))
		}
		s.addEventListener(target, c.Argument(0).String(), c.Argument(1), c.Argument(2))
		return goja.Undefined()
	})
	s.method(p, "removeEventListener", func(c goja.FunctionCall) goja.Value {
		target := s.eventTargetThis(c.This)
		if len(c.Arguments) < 2 {
			panic(s.vm.NewTypeError("missing event listener arguments"))
		}
		capture := false
		if o, ok := c.Argument(2).(*goja.Object); ok {
			capture = flag(o, "capture")
		} else {
			capture = c.Argument(2).ToBoolean()
		}
		s.removeEventListener(target, c.Argument(0).String(), c.Argument(1), capture)
		return goja.Undefined()
	})
	s.method(p, "dispatchEvent", func(c goja.FunctionCall) goja.Value {
		target := s.eventTargetThis(c.This)
		state := s.brand(c.Argument(0), "Event")
		if flag(state, "dispatching") || !flag(state, "initialized") {
			panic(s.eventException("InvalidStateError", "event is already dispatching or uninitialized"))
		}
		_ = state.Set("trusted", false)
		canceled, err := s.dispatchEvent(target, c.Argument(0).(*goja.Object))
		if err != nil {
			panic(err)
		}
		return s.vm.ToValue(!canceled)
	})
}
func (s *Sandbox) addEventListener(w *goja.Object, typ string, callback, options goja.Value) {
	if goja.IsNull(callback) || goja.IsUndefined(callback) {
		return
	}
	if _, ok := callback.(*goja.Object); !ok {
		panic(s.vm.NewTypeError("listener must be an object or function"))
	}
	capture, once, passive := false, false, false
	signal := goja.Undefined()
	if o, ok := options.(*goja.Object); ok {
		capture = flag(o, "capture")
		once = flag(o, "once")
		if v := value(o, "passive"); !goja.IsUndefined(v) {
			passive = v.ToBoolean()
		} else {
			passive = s.defaultPassive(w, typ)
		}
		signal = value(o, "signal")
	} else {
		capture = options.ToBoolean()
		passive = s.defaultPassive(w, typ)
	}
	if !goja.IsUndefined(signal) && !goja.IsNull(signal) {
		st := s.brand(signal, "EventTarget")
		if !flag(st, "signal") {
			panic(s.vm.NewTypeError("signal must be AbortSignal"))
		}
		if flag(st, "aborted") {
			return
		}
	}
	for _, v := range arrayValues(s.listenerList(w, typ, false)) {
		r := v.(*goja.Object)
		if flag(r, "capture") == capture && value(r, "callback").StrictEquals(callback) {
			return
		}
	}
	if s.nListen >= s.cfg.MaxListeners {
		s.collectNow()
		if s.nListen >= s.cfg.MaxListeners {
			panic(s.vm.NewTypeError("too many event listeners"))
		}
	}
	r := s.record()
	_ = r.Set("callback", callback)
	_ = r.Set("capture", capture)
	_ = r.Set("once", once)
	_ = r.Set("passive", passive)
	_ = r.Set("signal", signal)
	arr := s.listenerList(w, typ, true)
	_ = arr.Set(strconv.Itoa(int(arr.Get("length").ToInteger())), r)
	id := dom.NodeID(s.brand(w, "EventTarget").Get("id").ToInteger())
	s.nListen++
	if s.listenCount[id] == nil {
		s.listenCount[id] = map[string]int{}
	}
	s.listenCount[id][typ]++
	s.typeCount[typ]++
	if typ == "abort" {
		s.refreshSignalPin(w)
	}
	if n := s.tree.Node(id); n != nil {
		s.tree.AddListener(n, typ)
		if s.tree.Attached(n) {
			s.strong[id] = w
		}
	}
}
func (s *Sandbox) removeRecord(w *goja.Object, typ string, record *goja.Object) {
	if flag(record, "removed") {
		return
	}
	_ = record.Set("removed", true)
	arr := s.listenerList(w, typ, false)
	kept := s.vm.NewArray()
	_ = kept.SetPrototype(nil)
	for _, v := range arrayValues(arr) {
		if v != record {
			_ = kept.Set(strconv.Itoa(int(kept.Get("length").ToInteger())), v)
		}
	}
	bag := s.brand(w, "EventTarget").Get("listeners").(*goja.Object)
	if kept.Get("length").ToInteger() == 0 {
		_ = bag.Delete(typ)
	} else {
		_ = bag.Set(typ, kept)
	}
	id := dom.NodeID(s.brand(w, "EventTarget").Get("id").ToInteger())
	s.nListen--
	s.typeCount[typ]--
	if s.typeCount[typ] <= 0 {
		delete(s.typeCount, typ)
	}
	if counts := s.listenCount[id]; counts != nil {
		counts[typ]--
		if counts[typ] <= 0 {
			delete(counts, typ)
			if n := s.tree.Node(id); n != nil {
				s.tree.RemoveListener(n, typ)
			}
		}
		if len(counts) == 0 {
			delete(s.listenCount, id)
			delete(s.strong, id)
		}
	}
	if typ == "abort" {
		s.refreshSignalPin(w)
	}
}
func (s *Sandbox) removeEventListener(w *goja.Object, typ string, callback goja.Value, capture bool) {
	for _, v := range arrayValues(s.listenerList(w, typ, false)) {
		r := v.(*goja.Object)
		if flag(r, "capture") == capture && value(r, "callback").StrictEquals(callback) {
			s.removeRecord(w, typ, r)
			return
		}
	}
}

func (s *Sandbox) forgetCounts(id dom.NodeID) {
	s.forgetMessagePort(id)
	for typ, count := range s.listenCount[id] {
		s.nListen -= count
		s.typeCount[typ] -= count
		if s.typeCount[typ] <= 0 {
			delete(s.typeCount, typ)
		}
	}
	delete(s.listenCount, id)
	delete(s.strong, id)
	delete(s.extraTargets, id)
	if timer, ok := s.abortTimers[id]; ok {
		s.host.ClearTimer(timer)
		delete(s.abortTimers, id)
	}
}

type Event struct {
	Type   string
	Target dom.NodeID // 0 targets the focused element (keyboard) or the document
	Key    string     // keydown: DOM KeyboardEvent.key where known
	Href   string     // click: the resolved href of the target or its nearest <a>, if any
	// NoBubble suppresses the bubbling phase (focus, blur); capture still runs.
	NoBubble bool
	// AllowDetached is reserved for host-delivered image load/error events.
	AllowDetached bool
	NonCancelable bool
}

// Dispatch is host-only. Synthetic dispatch never enters default-action code.
func (s *Sandbox) Dispatch(ev Event) (prevented bool, err error) {
	if s.disabled {
		return false, ErrDisabled
	}
	target := s.tree.Root()
	if ev.Target != 0 {
		target = s.tree.Node(ev.Target)
		if target == nil || !s.tree.Attached(target) && !ev.AllowDetached {
			return false, nil
		}
	} else if ev.Type == "keydown" || ev.Type == "keyup" {
		if n := s.tree.Node(s.active); n != nil && s.tree.Attached(n) {
			target = n
		}
	}
	run := func(*goja.Runtime) error {
		proto := s.eventProto
		switch ev.Type {
		case "keydown", "keyup", "keypress":
			proto = s.specialEventProtos["KeyboardEvent"]
		case "click":
			proto = s.specialEventProtos["MouseEvent"]
		case "focus", "blur", "focusin", "focusout":
			proto = s.specialEventProtos["FocusEvent"]
		}
		event := s.newEvent(ev.Type, !ev.NoBubble, !ev.NonCancelable, false, proto)
		s.initializeSpecialEvent(event, proto, nil)
		state := s.brand(event, "Event")
		_ = state.Set("trusted", true)
		_ = state.Set("key", ev.Key)
		_ = state.Set("href", ev.Href)
		prevented, err = s.dispatchEvent(s.wrap(target).(*goja.Object), event)
		return err
	}
	if s.running {
		err = run(s.vm)
	} else {
		err = s.Run(run)
	}
	return
}
func (s *Sandbox) dispatchEvent(target, event *goja.Object) (bool, error) {
	state := s.brand(event, "Event")
	if flag(state, "dispatching") || !flag(state, "initialized") {
		panic(s.eventException("InvalidStateError", "event is already dispatching or uninitialized"))
	}
	if s.eventDepth >= 64 {
		panic(s.vm.NewTypeError("event dispatch depth limit exceeded"))
	}
	s.eventDepth++
	defer func() { s.eventDepth-- }()
	typ := value(state, "type").String()
	path := []*goja.Object{target}
	id := dom.NodeID(s.brand(target, "EventTarget").Get("id").ToInteger())
	if n := s.tree.Node(id); n != nil {
		for parent := n.Parent(); parent != nil; parent = parent.Parent() {
			path = append(path, s.wrap(parent).(*goja.Object))
		}
		if s.tree.Attached(n) && typ != "load" {
			path = append(path, s.vm.GlobalObject())
		}
	}
	vals := make([]any, len(path))
	for i, p := range path {
		vals[i] = p
	}
	_ = state.Set("path", s.vm.NewArray(vals...))
	_ = state.Set("dispatching", true)
	_ = state.Set("target", target)
	if flag(state, "documentLoad") {
		_ = state.Set("target", s.docObj)
	}
	defer func() {
		_ = state.Set("dispatching", false)
		_ = state.Set("currentTarget", goja.Null())
		_ = state.Set("phase", 0)
		_ = state.Set("stop", false)
		_ = state.Set("immediate", false)
		_ = state.Set("passive", false)
		_ = state.Set("path", s.vm.NewArray())
	}()
	invoke := func(w *goja.Object, capture bool, phase int) error {
		_ = state.Set("currentTarget", w)
		_ = state.Set("phase", phase)
		for _, v := range arrayValues(s.listenerList(w, typ, false)) {
			r := v.(*goja.Object)
			if flag(state, "immediate") {
				break
			}
			if flag(r, "removed") || flag(r, "capture") != capture {
				continue
			}
			if flag(r, "once") {
				s.removeRecord(w, typ, r)
			}
			_ = state.Set("passive", flag(r, "passive"))
			err := s.invokeEventListener(value(r, "callback"), w, event)
			_ = state.Set("passive", false)
			if err != nil {
				var interrupted *goja.InterruptedError
				var overflow *goja.StackOverflowError
				if errors.As(err, &interrupted) || errors.As(err, &overflow) {
					return err
				}
				if reportErr := s.reportEventError(err, typ); reportErr != nil {
					return reportErr
				}
			}
		}
		return nil
	}
	for i := len(path) - 1; i > 0 && !flag(state, "stop"); i-- {
		if err := invoke(path[i], true, 1); err != nil {
			return flag(state, "canceled"), err
		}
	}
	if !flag(state, "stop") {
		if err := invoke(target, true, 2); err != nil {
			return flag(state, "canceled"), err
		}
		if err := invoke(target, false, 2); err != nil {
			return flag(state, "canceled"), err
		}
	}
	if flag(state, "bubbles") {
		for i := 1; i < len(path) && !flag(state, "stop"); i++ {
			if err := invoke(path[i], false, 3); err != nil {
				return flag(state, "canceled"), err
			}
		}
	}
	return flag(state, "canceled"), nil
}
func (s *Sandbox) invokeEventListener(callback goja.Value, target, event *goja.Object) (err error) {
	exception := s.vm.Try(func() {
		fn, ok := goja.AssertFunction(callback)
		this := goja.Value(target)
		if !ok {
			object := callback.(*goja.Object)
			fn, ok = goja.AssertFunction(object.Get("handleEvent"))
			this = object
		}
		if !ok {
			panic(s.vm.NewTypeError("listener.handleEvent is not callable"))
		}
		_, err = fn(this, event)
	})
	if exception != nil {
		return exception
	}
	return err
}
func (s *Sandbox) reportEventError(err error, typ string) error {
	message := err.Error()
	filename := ""
	line, column := 0, 0
	thrown := goja.Value(goja.Null())
	var exception *goja.Exception
	if errors.As(err, &exception) {
		thrown = exception.Value()
		message = safeString(thrown)
		for _, frame := range exception.Stack() {
			p := frame.Position()
			if p.Line > 0 {
				filename, line, column = p.Filename, p.Line, p.Column
				break
			}
		}
	}
	canceled := false
	if !s.eventErrorReporting {
		s.eventErrorReporting = true
		defer func() { s.eventErrorReporting = false }()
		p := s.specialEventProtos["ErrorEvent"]
		e := s.newEvent("error", false, true, false, p)
		st := s.private(e)
		for k, v := range map[string]any{"message": message, "filename": filename, "lineno": line, "colno": column, "error": thrown, "trusted": true} {
			_ = st.Set(k, v)
		}
		var reportErr error
		canceled, reportErr = s.dispatchEvent(s.vm.GlobalObject(), e)
		if reportErr != nil {
			return reportErr
		}
	}
	if !canceled {
		if line > 0 {
			message += fmt.Sprintf(" at %s:%d:%d", filename, line, column)
		}
		s.host.Log("error", "uncaught in "+typ+" listener: "+safeString(s.vm.ToValue(message)))
	}
	return nil
}

func (s *Sandbox) defaultPassive(w *goja.Object, typ string) bool {
	if typ != "touchstart" && typ != "touchmove" && typ != "wheel" && typ != "mousewheel" {
		return false
	}
	if w == s.vm.GlobalObject() || w == s.docObj {
		return true
	}
	id := dom.NodeID(s.brand(w, "EventTarget").Get("id").ToInteger())
	n := s.tree.Node(id)
	return n != nil && (n == s.tree.Body() || n.Tag() == "html" && n.Parent() == s.tree.Root())
}
