// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import (
	"errors"
	"github.com/dop251/goja"
	"nimbleterminal.dev/ntbrowser/dom"
	"strconv"
)

const (
	maxMutationObservers     = 64
	maxMutationRegistrations = 256
	maxMutationRecords       = 1024
	maxMutationBytes         = 1 << 20
	maxMutationTransients    = 1024
	maxMutationDeliveries    = 128
)

type mutationOptions struct {
	attributes, characters, children, subtree, attributeOld, characterOld bool
	filter                                                                map[string]bool
}
type mutationRegistration struct {
	node, source dom.NodeID
	options      mutationOptions
}
type queuedMutation struct {
	mutation dom.Mutation
	old      bool
	bytes    int
}
type mutationObserver struct {
	object                    *goja.Object
	callback                  goja.Callable
	registrations, transients []mutationRegistration
	records                   []queuedMutation
	pending                   bool
}

func (s *Sandbox) observer(v goja.Value) (uint64, *mutationObserver) {
	st := s.brand(v, "MutationObserver")
	id := uint64(st.Get("id").ToInteger())
	return id, s.mutationObservers[id]
}
func (s *Sandbox) mutationOptions(v goja.Value) mutationOptions {
	o, ok := v.(*goja.Object)
	if !ok {
		panic(s.vm.NewTypeError("observer options required"))
	}
	// Read each dictionary member once, in Web IDL dictionary order.
	filter := value(o, "attributeFilter")
	ao := value(o, "attributeOldValue")
	a := value(o, "attributes")
	c := value(o, "characterData")
	co := value(o, "characterDataOldValue")
	ch := value(o, "childList")
	sub := value(o, "subtree")
	out := mutationOptions{attributes: a.ToBoolean(), characters: c.ToBoolean(), children: ch.ToBoolean(), subtree: sub.ToBoolean(), attributeOld: ao.ToBoolean(), characterOld: co.ToBoolean()}
	if goja.IsUndefined(a) && (!goja.IsUndefined(ao) || !goja.IsUndefined(filter)) {
		out.attributes = true
	}
	if goja.IsUndefined(c) && !goja.IsUndefined(co) {
		out.characters = true
	}
	if !goja.IsUndefined(filter) {
		out.filter = map[string]bool{}
		count := 0
		s.vm.ForOf(filter, func(v goja.Value) bool {
			count++
			if count > 64 {
				panic(s.eventException("QuotaExceededError", "attribute filter limit reached"))
			}
			out.filter[s.foreignText(v, 1024)] = true
			return true
		})
	}
	if !out.attributes && !out.characters && !out.children || !out.attributes && (out.attributeOld || out.filter != nil) || !out.characters && out.characterOld {
		panic(s.vm.NewTypeError("inconsistent mutation observer options"))
	}
	return out
}
func (s *Sandbox) installMutations() {
	vm := s.vm
	s.mutationObservers = map[uint64]*mutationObserver{}
	s.mutationPins = map[dom.NodeID]int{}
	proto := vm.NewObject()
	s.mutationProto = proto
	ctor := vm.ToValue(func(c goja.ConstructorCall) *goja.Object {
		_, ok := goja.AssertFunction(c.Argument(0))
		if !ok {
			panic(vm.NewTypeError("MutationObserver callback must be callable"))
		}
		o := vm.NewObject()
		_ = o.SetPrototype(proto)
		s.mutationNext++
		st := s.record()
		_ = st.Set("kind", "MutationObserver")
		_ = st.Set("id", s.mutationNext)
		_ = st.Set("callback", c.Argument(0))
		s.putPrivate(o, st)
		return o
	}).(*goja.Object)
	_ = ctor.Set("prototype", proto)
	defineFrozen(proto, "constructor", ctor)
	defineFrozen(vm.GlobalObject(), "MutationObserver", ctor)
	s.method(proto, "observe", func(c goja.FunctionCall) goja.Value {
		id, _ := s.observer(c.This)
		node := s.node(c.Argument(0))
		options := s.mutationOptions(c.Argument(1))
		// Getters/iterators may disconnect or replace registrations.
		if s.mutationStopped {
			panic(s.eventException("QuotaExceededError", "mutation observers disabled after quota overflow"))
		}
		observer := s.mutationObservers[id]
		if observer == nil {
			if len(s.mutationObservers) >= maxMutationObservers {
				panic(s.eventException("QuotaExceededError", "too many mutation observers"))
			}
			fn, _ := goja.AssertFunction(s.brand(c.This, "MutationObserver").Get("callback"))
			observer = &mutationObserver{object: c.This.(*goja.Object), callback: fn}
		}
		found := -1
		for i, r := range observer.registrations {
			if r.node == node.ID() {
				found = i
				break
			}
		}
		if found < 0 && s.mutationRegistrations >= maxMutationRegistrations {
			panic(s.eventException("QuotaExceededError", "too many observer registrations"))
		}
		if s.mutationObservers[id] == nil {
			s.mutationObservers[id] = observer
			s.mutationOrder = append(s.mutationOrder, id)
		}
		reg := mutationRegistration{node: node.ID(), source: node.ID(), options: options}
		if found >= 0 {
			observer.registrations[found] = reg
			s.removeTransients(observer, node.ID())
		} else {
			observer.registrations = append(observer.registrations, reg)
			s.mutationRegistrations++
			s.pinMutation(node, 1)
		}
		return goja.Undefined()
	})
	s.method(proto, "disconnect", func(c goja.FunctionCall) goja.Value {
		id, _ := s.observer(c.This)
		s.disconnectObserver(id)
		return goja.Undefined()
	})
	s.method(proto, "takeRecords", func(c goja.FunctionCall) goja.Value {
		_, o := s.observer(c.This)
		if o == nil {
			return vm.NewArray()
		}
		return s.takeMutationRecords(o)
	})
	// Capture the native Promise reaction mechanism, with a private promise whose
	// constructor cannot be redirected through page-controlled species getters.
	promise, resolve, _ := vm.NewPromise()
	_ = resolve(goja.Undefined())
	s.mutationPromise = vm.ToValue(promise).(*goja.Object)
	defineFrozen(s.mutationPromise, "constructor", goja.Undefined())
	s.mutationThen, _ = goja.AssertFunction(s.promiseCtor.Get("prototype").(*goja.Object).Get("then"))
	s.mutationNotify = vm.ToValue(func(goja.FunctionCall) goja.Value { s.notifyMutations(); return goja.Undefined() })
	s.tree.SetMutationHook(s.queueMutation)
}
func (s *Sandbox) pinMutation(n *dom.Node, delta int) {
	if n == nil {
		return
	}
	s.mutationPins[n.ID()] += delta
	if s.mutationPins[n.ID()] <= 0 {
		delete(s.mutationPins, n.ID())
	}
}
func (s *Sandbox) pinMutationRecord(m dom.Mutation, delta int) {
	s.pinMutation(m.Target, delta)
	s.pinMutation(m.PreviousSibling, delta)
	s.pinMutation(m.NextSibling, delta)
	for _, n := range m.Added {
		s.pinMutation(n, delta)
	}
	for _, n := range m.Removed {
		s.pinMutation(n, delta)
	}
}
func (s *Sandbox) removeTransients(o *mutationObserver, source dom.NodeID) {
	kept := o.transients[:0]
	for _, r := range o.transients {
		if source == 0 || source == r.source {
			s.pinMutation(s.tree.Node(r.node), -1)
			s.mutationTransients--
		} else {
			kept = append(kept, r)
		}
	}
	o.transients = kept
}
func (s *Sandbox) discardRecords(o *mutationObserver) {
	for _, r := range o.records {
		s.pinMutationRecord(r.mutation, -1)
		s.mutationBytes -= r.bytes
		s.mutationRecords--
	}
	o.records = nil
}
func (s *Sandbox) disconnectObserver(id uint64) {
	o := s.mutationObservers[id]
	if o == nil {
		return
	}
	s.discardRecords(o)
	s.removeTransients(o, 0)
	for _, r := range o.registrations {
		s.pinMutation(s.tree.Node(r.node), -1)
		s.mutationRegistrations--
	}
	delete(s.mutationObservers, id)
	for i, v := range s.mutationOrder {
		if v == id {
			s.mutationOrder = append(s.mutationOrder[:i], s.mutationOrder[i+1:]...)
			break
		}
	}
	// Remove pending IDs as well, so disconnect/reobserve cannot duplicate delivery.
	for i := 0; i < len(s.mutationPending); {
		if s.mutationPending[i] == id {
			s.mutationPending = append(s.mutationPending[:i], s.mutationPending[i+1:]...)
		} else {
			i++
		}
	}
}
func (s *Sandbox) stopMutations() {
	if s.mutationStopped {
		return
	}
	s.mutationStopped = true
	for len(s.mutationOrder) > 0 {
		s.disconnectObserver(s.mutationOrder[0])
	}
	s.host.Log("error", "MutationObserver quota exceeded; observers disconnected")
}
func mutationMatches(r mutationRegistration, n *dom.Node) bool {
	if n == nil {
		return false
	}
	if n.ID() == r.node {
		return true
	}
	if !r.options.subtree {
		return false
	}
	for n = n.Parent(); n != nil; n = n.Parent() {
		if n.ID() == r.node {
			return true
		}
	}
	return false
}
func (s *Sandbox) pendingMutation(id uint64, o *mutationObserver) {
	if !o.pending {
		o.pending = true
		s.mutationPending = append(s.mutationPending, id)
	}
}
func (s *Sandbox) queueMutation(m dom.Mutation) {
	if s.disabled || s.mutationStopped || len(s.mutationObservers) == 0 {
		return
	}
	for _, id := range s.mutationOrder {
		o := s.mutationObservers[id]
		interested, old := false, false
		regs := append(append([]mutationRegistration(nil), o.registrations...), o.transients...)
		for _, r := range regs {
			if !mutationMatches(r, m.Target) {
				continue
			}
			opts := r.options
			match := m.Type == "childList" && opts.children || m.Type == "characterData" && opts.characters || m.Type == "attributes" && opts.attributes && (opts.filter == nil || opts.filter[m.AttributeName])
			if match {
				interested = true
				old = old || m.Type == "attributes" && opts.attributeOld || m.Type == "characterData" && opts.characterOld
			}
			if len(m.Removed) > 0 && opts.subtree {
				for _, removed := range m.Removed {
					duplicate := false
					for _, tr := range o.transients {
						if tr.node == removed.ID() && tr.source == r.source {
							duplicate = true
							break
						}
					}
					if duplicate {
						continue
					}
					if s.mutationTransients >= maxMutationTransients {
						s.stopMutations()
						return
					}
					o.transients = append(o.transients, mutationRegistration{node: removed.ID(), source: r.source, options: opts})
					s.mutationTransients++
					s.pinMutation(removed, 1)
					s.pendingMutation(id, o)
				}
			}
		}
		if interested {
			charge := 64 + 8*(len(m.Added)+len(m.Removed))
			if old {
				charge += len(m.OldValue)
			}
			if s.mutationRecords >= maxMutationRecords || charge > maxMutationBytes-s.mutationBytes {
				s.stopMutations()
				return
			}
			copy := m
			copy.Added = append([]*dom.Node(nil), m.Added...)
			copy.Removed = append([]*dom.Node(nil), m.Removed...)
			if !old {
				copy.OldValue = ""
				copy.OldPresent = false
			}
			o.records = append(o.records, queuedMutation{copy, old, charge})
			s.mutationRecords++
			s.mutationBytes += charge
			s.pinMutationRecord(copy, 1)
			s.pendingMutation(id, o)
		}
	}
	// A shared-DOM caller must never enter this runtime. Outside a JS stack,
	// the owner loop performs a checkpoint under the ordinary watchdog instead.
	if s.running && len(s.vm.CaptureCallStack(1, nil)) > 0 {
		s.scheduleMutations()
	}
}
func (s *Sandbox) scheduleMutations() {
	if s.mutationScheduled || s.mutationStopped || len(s.mutationPending) == 0 {
		return
	}
	s.mutationScheduled = true
	if _, err := s.mutationThen(s.mutationPromise, s.mutationNotify); err != nil {
		panic(err)
	}
}
func (s *Sandbox) mutationNodeList(nodes []*dom.Node) *goja.Object {
	out := s.vm.NewObject()
	for i, n := range nodes {
		defineFrozen(out, strconv.Itoa(i), s.wrap(n))
	}
	defineFrozen(out, "length", s.vm.ToValue(len(nodes)))
	s.method(out, "item", func(c goja.FunctionCall) goja.Value {
		i := webIDLUint32(c.Argument(0))
		if uint64(i) >= uint64(len(nodes)) {
			return goja.Null()
		}
		return out.Get(strconv.Itoa(int(i)))
	})
	_ = out.SetSymbol(goja.SymIterator, s.vm.ToValue(func(goja.FunctionCall) goja.Value {
		i := 0
		it := s.vm.NewObject()
		s.method(it, "next", func(goja.FunctionCall) goja.Value {
			r := s.vm.NewObject()
			_ = r.Set("done", i >= len(nodes))
			if i < len(nodes) {
				_ = r.Set("value", out.Get(strconv.Itoa(i)))
				i++
			}
			return r
		})
		return it
	}))
	return out
}
func (s *Sandbox) takeMutationRecords(o *mutationObserver) *goja.Object {
	records := o.records
	out := s.vm.NewArray()
	for i, r := range records {
		m := r.mutation
		record := s.vm.NewObject()
		for key, n := range map[string]*dom.Node{"target": m.Target, "previousSibling": m.PreviousSibling, "nextSibling": m.NextSibling} {
			defineFrozen(record, key, s.wrap(n))
		}
		defineFrozen(record, "type", s.vm.ToValue(m.Type))
		defineFrozen(record, "addedNodes", s.mutationNodeList(m.Added))
		defineFrozen(record, "removedNodes", s.mutationNodeList(m.Removed))
		name, old := goja.Value(goja.Null()), goja.Value(goja.Null())
		if m.Type == "attributes" {
			name = s.vm.ToValue(m.AttributeName)
		}
		if r.old && m.OldPresent {
			old = s.vm.ToValue(m.OldValue)
		}
		defineFrozen(record, "attributeName", name)
		defineFrozen(record, "attributeNamespace", goja.Null())
		defineFrozen(record, "oldValue", old)
		_ = out.Set(strconv.Itoa(i), record)
	}
	s.discardRecords(o)
	return out
}
func (s *Sandbox) notifyMutations() {
	s.mutationScheduled = false
	pending := append([]uint64(nil), s.mutationPending...)
	s.mutationPending = nil
	for _, id := range pending {
		o := s.mutationObservers[id]
		if o == nil {
			continue
		}
		o.pending = false
		s.removeTransients(o, 0)
		if len(o.records) == 0 {
			continue
		}
		s.mutationDeliveries++
		if s.mutationDeliveries > maxMutationDeliveries {
			s.stopMutations()
			return
		}
		records := s.takeMutationRecords(o)
		if _, err := o.callback(o.object, records, o.object); err != nil {
			var interrupted *goja.InterruptedError
			var overflow *goja.StackOverflowError
			if errors.As(err, &interrupted) || errors.As(err, &overflow) {
				panic(err)
			}
			if err := s.reportEventError(err, "MutationObserver"); err != nil {
				panic(err)
			}
		}
	}
}

// FlushMutations is an owner-thread checkpoint for host/shared-DOM writes.
// Promise reactions generated inside JS use the normal microtask queue instead.
func (s *Sandbox) FlushMutations() error {
	if s.disabled || len(s.mutationPending) == 0 && len(s.disclosureOrder) == 0 {
		return nil
	}
	return s.Run(func(*goja.Runtime) error { return nil })
}
