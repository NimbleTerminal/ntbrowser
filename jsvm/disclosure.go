// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import (
	"github.com/dop251/goja"
	"nimbleterminal.dev/ntbrowser/dom"
	"strings"
)

type disclosureToggle struct {
	node      *dom.Node
	old, next bool
}

const maxDisclosureToggles = 256

func (s *Sandbox) installDisclosureBindings(p *goja.Object) {
	s.disclosurePending = map[dom.NodeID]*disclosureToggle{}
	s.popoverChanging = map[dom.NodeID]bool{}
	s.tree.SetToggleHook(func(n *dom.Node, old, next bool) {
		if pending := s.disclosurePending[n.ID()]; pending != nil {
			pending.next = next
			return
		}
		if len(s.disclosurePending) >= maxDisclosureToggles {
			s.disclosureOverflow = true
			return
		}
		s.pinMutation(n, 1)
		s.disclosurePending[n.ID()] = &disclosureToggle{n, old, next}
		s.disclosureOrder = append(s.disclosureOrder, n.ID())
	})
	s.accessor(p, "open", func(v goja.Value) goja.Value {
		n := s.node(v)
		if n.Tag() != "details" && n.Tag() != "dialog" {
			return goja.Undefined()
		}
		_, open := n.Attr("open")
		return s.vm.ToValue(open)
	}, func(v, value goja.Value) {
		n := s.node(v)
		if n.Tag() != "details" && n.Tag() != "dialog" {
			return
		}
		if value.ToBoolean() {
			s.must(s.tree.SetAttr(n, "open", ""))
		} else {
			s.tree.RemoveAttr(n, "open")
		}
	})
	s.accessor(p, "popover", func(v goja.Value) goja.Value {
		n := s.node(v)
		if n.PopoverMode() == "" {
			return goja.Null()
		}
		return s.vm.ToValue(n.PopoverMode())
	}, func(v, value goja.Value) {
		n := s.node(v)
		if goja.IsNull(value) {
			s.tree.RemoveAttr(n, "popover")
		} else {
			s.must(s.tree.SetAttr(n, "popover", value.String()))
		}
	})
	for _, name := range []string{"showPopover", "hidePopover", "togglePopover"} {
		s.method(p, name, func(c goja.FunctionCall) goja.Value {
			n := s.node(c.This)
			open := name == "showPopover"
			if name == "togglePopover" {
				open = !n.PopoverOpen()
				arg := c.Argument(0)
				if !goja.IsUndefined(arg) {
					if obj, ok := arg.(*goja.Object); ok {
						arg = value(obj, "force")
					}
					if !goja.IsUndefined(arg) {
						open = arg.ToBoolean()
					}
				}
			}
			if err := s.changePopover(n, open); err != nil {
				panic(err)
			}
			if name == "togglePopover" {
				return s.vm.ToValue(n.PopoverOpen())
			}
			return goja.Undefined()
		})
	}
}

func toggleState(open bool) string {
	if open {
		return "open"
	}
	return "closed"
}
func (s *Sandbox) toggleEvent(n *dom.Node, typ string, old, next, cancelable bool) (bool, error) {
	event := s.newEvent(typ, false, cancelable, false, s.eventProto)
	defineFrozen(event, "oldState", s.vm.ToValue(toggleState(old)))
	defineFrozen(event, "newState", s.vm.ToValue(toggleState(next)))
	return s.dispatchEvent(s.wrap(n).(*goja.Object), event)
}
func (s *Sandbox) changePopover(n *dom.Node, open bool) error {
	if n.PopoverMode() == "" || !s.tree.Attached(n) {
		panic(s.eventException("InvalidStateError", "popover must be connected and have a popover attribute"))
	}
	if n.PopoverOpen() == open || s.popoverChanging[n.ID()] {
		return nil
	}
	s.popoverChanging[n.ID()] = true
	defer delete(s.popoverChanging, n.ID())
	mode := n.PopoverMode()
	canceled, err := s.toggleEvent(n, "beforetoggle", n.PopoverOpen(), open, open)
	if err != nil || canceled {
		return err
	}
	if !s.tree.Attached(n) || n.PopoverMode() != mode {
		return nil
	}
	if open && mode != "manual" {
		close := s.tree.PopoverPeers(n)
		for _, other := range close {
			if err := s.changePopover(other, false); err != nil {
				return err
			}
		}
	}
	if s.tree.Attached(n) && n.PopoverMode() == mode {
		s.tree.SetPopoverOpen(n, open)
	}
	return nil
}

// TogglePopover is a local reader default action, entered under the same
// watchdog as JS methods. It conveys no navigation or trusted activation.
func (s *Sandbox) TogglePopover(id dom.NodeID, action string) error {
	return s.Run(func(*goja.Runtime) error {
		n := s.tree.Node(id)
		if n == nil || !s.tree.Attached(n) || n.PopoverMode() == "" {
			return nil
		}
		open := !n.PopoverOpen()
		switch strings.ToLower(action) {
		case "show":
			open = true
		case "hide":
			open = false
		}
		return s.changePopover(n, open)
	})
}
func (s *Sandbox) scheduleDisclosureToggles() {
	if s.disabled || s.disclosureTask != 0 || len(s.disclosureOrder) == 0 {
		return
	}
	id, ok := s.host.QueueTask(func(goja.Value, ...goja.Value) (goja.Value, error) {
		s.disclosureTask = 0
		if s.disabled {
			return goja.Undefined(), ErrDisabled
		}
		id := s.disclosureOrder[0]
		s.disclosureOrder = s.disclosureOrder[1:]
		pending := s.disclosurePending[id]
		delete(s.disclosurePending, id)
		s.scheduleDisclosureToggles()
		if pending == nil {
			return goja.Undefined(), nil
		}
		defer s.pinMutation(pending.node, -1)
		_, err := s.toggleEvent(pending.node, "toggle", pending.old, pending.next, false)
		return goja.Undefined(), err
	})
	if ok {
		s.disclosureTask = id
	} // retry at the next owner checkpoint if timer cap is full
	if s.disclosureOverflow {
		s.host.Log("warn", "disclosure toggle event queue limit exceeded")
		s.disclosureOverflow = false
	}
}
