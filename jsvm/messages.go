// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"github.com/dop251/goja"
	"nimbleterminal.dev/ntbrowser/dom"
)

type messagePort struct {
	id, peer dom.NodeID
	started  bool
	pin      *goja.Object
}
type portMessage struct {
	window, deliver bool
	target          dom.NodeID
	data            goja.Value
	bytes           int
}

func (s *Sandbox) port(v goja.Value) *messagePort {
	st := s.brand(v, "EventTarget")
	id := dom.NodeID(value(st, "id").ToInteger())
	p := s.messagePorts[id]
	if p == nil {
		panic(s.vm.NewTypeError("not a MessagePort"))
	}
	return p
}
func (s *Sandbox) messageQuota() {
	panic(s.eventException("QuotaExceededError", "message channel limit exceeded"))
}
func (s *Sandbox) installMessages() {
	s.messagePorts = map[dom.NodeID]*messagePort{}
	s.cloneMessage = s.makeMessageCloner()
	vm := s.vm
	s.emptyMessagePorts = vm.NewArray()
	freeze, _ := goja.AssertFunction(vm.Get("Object").(*goja.Object).Get("freeze"))
	if _, err := freeze(goja.Undefined(), s.emptyMessagePorts); err != nil {
		panic(err)
	}
	p := vm.NewObject()
	_ = p.SetPrototype(s.eventTargetProto)
	portCtor := vm.ToValue(func(goja.ConstructorCall) *goja.Object { panic(vm.NewTypeError("Illegal constructor")) }).(*goja.Object)
	_ = portCtor.Set("prototype", p)
	_ = portCtor.SetPrototype(vm.Get("EventTarget").(*goja.Object))
	defineFrozen(p, "constructor", portCtor)
	defineFrozen(vm.GlobalObject(), "MessagePort", portCtor)
	_ = p.DefineDataPropertySymbol(goja.SymToStringTag, vm.ToValue("MessagePort"), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	s.bindEventHandlers(p)
	s.method(p, "start", func(c goja.FunctionCall) goja.Value {
		s.port(c.This)
		s.startMessagePort(c.This.(*goja.Object))
		return goja.Undefined()
	})
	s.method(p, "close", func(c goja.FunctionCall) goja.Value { s.disentanglePort(s.port(c.This)); return goja.Undefined() })
	s.method(p, "postMessage", func(c goja.FunctionCall) goja.Value {
		source := s.port(c.This)
		if len(c.Arguments) == 0 {
			panic(vm.NewTypeError("message required"))
		}
		transfer := c.Argument(1)
		if !goja.IsUndefined(transfer) && !goja.IsNull(transfer) {
			o, ok := transfer.(*goja.Object)
			if !ok {
				panic(vm.NewTypeError("postMessage options must be an object"))
			}
			iterator := o.GetSymbol(goja.SymIterator)
			if iterator == nil || goja.IsUndefined(iterator) || goja.IsNull(iterator) {
				transfer = value(o, "transfer")
			}
			if !goja.IsUndefined(transfer) {
				vm.ForOf(transfer, func(goja.Value) bool {
					panic(s.eventException("DataCloneError", "message transfers are not supported"))
				})
			}
		}
		target := source.peer
		wrapper := s.weakW[target].Value()
		cloned, size := s.cloneMessage(c.Argument(0))
		// Serialization can invoke getters which post recursively or close a port.
		// Check the shared queue limits after that code, before accepting this copy.
		if target == 0 {
			return goja.Undefined()
		}
		dst := s.messagePorts[target]
		if dst == nil {
			return goja.Undefined()
		}
		if wrapper == nil {
			return goja.Undefined()
		}
		if len(s.portMessages) >= s.cfg.MaxMessages || size > s.cfg.MaxMessageBytes-s.messageBytes {
			s.messageQuota()
		}
		if dst.started {
			s.ensureMessageTask()
			dst.pin = wrapper
		}
		s.portMessages = append(s.portMessages, portMessage{target: target, data: cloned, bytes: size})
		s.messageBytes += size
		return goja.Undefined()
	})
	cp := vm.NewObject()
	ctor := vm.ToValue(func(c goja.ConstructorCall) *goja.Object {
		if len(s.messagePorts)+2 > s.cfg.MaxMessagePorts || len(s.extraTargets)+2 > s.cfg.MaxListeners {
			s.collectNow()
		}
		if len(s.messagePorts)+2 > s.cfg.MaxMessagePorts || len(s.extraTargets)+2 > s.cfg.MaxListeners {
			s.messageQuota()
		}
		a, b := vm.NewObject(), vm.NewObject()
		_ = a.SetPrototype(p)
		_ = b.SetPrototype(p)
		s.registerEventTarget(a, 0)
		s.registerEventTarget(b, 0)
		sa, sb := s.private(a), s.private(b)
		_ = sa.Set("messagePort", true)
		_ = sb.Set("messagePort", true)
		aid, bid := dom.NodeID(value(sa, "id").ToInteger()), dom.NodeID(value(sb, "id").ToInteger())
		s.messagePorts[aid] = &messagePort{id: aid, peer: bid}
		s.messagePorts[bid] = &messagePort{id: bid, peer: aid}
		// Entangled wrappers keep each other alive, but the host registry is weak.
		// Dropping both sides therefore collects callback cycles and queued data.
		_ = sa.Set("peer", b)
		_ = sb.Set("peer", a)
		st := s.record()
		_ = st.Set("kind", "MessageChannel")
		_ = st.Set("port1", a)
		_ = st.Set("port2", b)
		s.putPrivate(c.This, st)
		return c.This
	}).(*goja.Object)
	_ = ctor.Set("prototype", cp)
	defineFrozen(cp, "constructor", ctor)
	defineFrozen(vm.GlobalObject(), "MessageChannel", ctor)
	for _, key := range []string{"port1", "port2"} {
		s.accessor(cp, key, func(t goja.Value) goja.Value { return value(s.brand(t, "MessageChannel"), key) }, nil)
	}
	_ = cp.DefineDataPropertySymbol(goja.SymToStringTag, vm.ToValue("MessageChannel"), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
}
func (s *Sandbox) startMessagePort(w *goja.Object) {
	p := s.port(w)
	for _, m := range s.portMessages {
		if m.target == p.id {
			s.ensureMessageTask()
			p.pin = w
			break
		}
	}
	p.started = true
}
func (s *Sandbox) disentanglePort(p *messagePort) {
	if peer := s.messagePorts[p.peer]; peer != nil {
		peer.peer = 0
		if w := s.weakW[peer.id].Value(); w != nil {
			_ = s.private(w).Delete("peer")
		}
	}
	p.peer = 0
	if w := s.weakW[p.id].Value(); w != nil {
		_ = s.private(w).Delete("peer")
	}
	// Already queued messages remain deliverable; close prevents future posts.
}
func (s *Sandbox) forgetMessagePort(id dom.NodeID) {
	p := s.messagePorts[id]
	if p == nil {
		return
	}
	s.disentanglePort(p)
	delete(s.messagePorts, id)
	kept := s.portMessages[:0]
	for _, m := range s.portMessages {
		if m.target == id {
			s.messageBytes -= m.bytes
		} else {
			kept = append(kept, m)
		}
	}
	clear(s.portMessages[len(kept):])
	s.portMessages = kept
}
func (s *Sandbox) ensureMessageTask() {
	if s.messageTaskPending {
		return
	}
	id, ok := s.host.QueueTask(func(goja.Value, ...goja.Value) (goja.Value, error) {
		if s.disabled {
			return goja.Undefined(), ErrDisabled
		}
		s.messageTaskPending = false
		index := -1
		for i, m := range s.portMessages {
			if s.messageReady(m) {
				index = i
				break
			}
		}
		if index < 0 {
			return goja.Undefined(), nil
		}
		m := s.portMessages[index]
		copy(s.portMessages[index:], s.portMessages[index+1:])
		s.portMessages[len(s.portMessages)-1] = portMessage{}
		s.portMessages = s.portMessages[:len(s.portMessages)-1]
		s.messageBytes -= m.bytes
		p := s.messagePorts[m.target]
		var w *goja.Object
		if m.window {
			w = s.vm.GlobalObject()
		} else if p != nil {
			w = p.pin
			p.pin = nil
		}
		// Queue one task per message, preserving promise checkpoints and yielding
		// to the host between messages. A callback cannot extend this delivery batch.
		for _, queued := range s.portMessages {
			if s.messageReady(queued) {
				s.ensureMessageTask()
				break
			}
		}
		for _, queued := range s.portMessages {
			if p != nil && queued.target == p.id {
				p.pin = w
				break
			}
		}
		if w == nil || m.window && !m.deliver {
			return goja.Undefined(), nil
		}
		e := s.newEvent("message", false, false, false, s.specialEventProtos["MessageEvent"])
		s.initializeSpecialEvent(e, s.specialEventProtos["MessageEvent"], nil)
		st := s.private(e)
		_ = st.Set("data", m.data)
		if m.window {
			_ = st.Set("origin", s.messageOrigin)
			_ = st.Set("source", s.vm.GlobalObject())
		}
		_ = st.Set("ports", s.emptyMessagePorts)
		_ = st.Set("trusted", true)
		_, err := s.dispatchEvent(w, e)
		return goja.Undefined(), err
	})
	if !ok {
		s.messageQuota()
	}
	s.messageTask = id
	s.messageTaskPending = true
}

func (s *Sandbox) messageReady(m portMessage) bool {
	if m.window {
		return true
	}
	p := s.messagePorts[m.target]
	return p != nil && p.started
}
