// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import (
	"encoding/json"
	"math"
	"reflect"
	"strconv"

	"github.com/dop251/goja"
	weburl "github.com/nlnwa/whatwg-url/url"
	"nimbleterminal.dev/ntbrowser/dom"
)

// FrameSpec and FramePost carry plain data, never VM objects, across realms.
type FrameSpec struct {
	Node                  dom.NodeID
	Src, Srcdoc, Sandbox  string
	HasSrcdoc, HasSandbox bool
	Width, Height         int
}
type FramePost struct {
	Target       uint64
	TargetOrigin string
	Data         []byte
}

func (s *Sandbox) frameSpec(n *dom.Node) FrameSpec {
	src, _ := n.Attr("src")
	html, hasHTML := n.Attr("srcdoc")
	sandbox, hasSandbox := n.Attr("sandbox")
	size := func(name string, def, max int) int {
		v, _ := n.Attr(name)
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 {
			return def
		}
		return min(n, max)
	}
	return FrameSpec{n.ID(), src, html, sandbox, hasHTML, hasSandbox, size("width", 300, 1024), size("height", 150, 768)}
}
func (s *Sandbox) frameID(n *dom.Node) uint64 {
	if n.Tag() != "iframe" || !s.tree.Attached(n) || s.cfg.EnsureFrame == nil {
		return 0
	}
	id, err := s.cfg.EnsureFrame(s.frameSpec(n))
	if err != nil {
		return 0
	}
	return id
}
func (s *Sandbox) installFrames() {
	s.frameWindows = map[uint64]*goja.Object{}
	p := s.vm.NewObject()
	s.iframeProto = p
	_ = p.SetPrototype(s.elemProto)
	for _, key := range []string{"src", "srcdoc", "name", "width", "height", "allow", "referrerPolicy", "loading"} {
		attr := key
		if key == "referrerPolicy" {
			attr = "referrerpolicy"
		}
		s.accessor(p, key, func(v goja.Value) goja.Value {
			n := s.node(v)
			x, _ := n.Attr(attr)
			if key == "src" && x != "" {
				x = s.imageURL(x)
			}
			return s.vm.ToValue(x)
		}, func(t, v goja.Value) { s.must(s.tree.SetAttr(s.node(t), attr, v.String())) })
	}
	sandboxKey := goja.NewSymbol("ntbrowser.iframeSandbox")
	s.accessor(p, "sandbox", func(v goja.Value) goja.Value {
		w := v.ToObject(s.vm)
		if cached := w.GetSymbol(sandboxKey); cached != nil {
			return cached
		}
		list := s.attributeTokens(w, "sandbox")
		_ = w.DefineDataPropertySymbol(sandboxKey, list, goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
		return list
	}, func(t, v goja.Value) { s.must(s.tree.SetAttr(s.node(t), "sandbox", v.String())) })
	s.accessor(p, "contentWindow", func(v goja.Value) goja.Value {
		id := s.frameID(s.node(v))
		if id == 0 {
			return goja.Null()
		}
		return s.frameWindow(id)
	}, nil)
	s.accessor(p, "contentDocument", func(goja.Value) goja.Value { return goja.Null() }, nil)
	ctor := s.vm.ToValue(func(goja.ConstructorCall) *goja.Object { panic(s.vm.NewTypeError("Illegal constructor")) }).(*goja.Object)
	_ = ctor.Set("prototype", p)
	defineFrozen(p, "constructor", ctor)
	defineFrozen(s.vm.GlobalObject(), "HTMLIFrameElement", ctor)
}
func (s *Sandbox) frameWindow(id uint64) *goja.Object {
	if id == 0 || id == s.cfg.FrameSelf {
		return s.vm.GlobalObject()
	}
	if w := s.frameWindows[id]; w != nil {
		return w
	}
	w := s.vm.NewObject()
	plainProto := w.Prototype()
	s.frameWindows[id] = w
	// Same-origin frame DOM access is intentionally not part of isolated mode.
	for _, key := range []string{"document", "location", "localStorage", "sessionStorage"} {
		s.accessor(w, key, func(goja.Value) goja.Value { panic(s.eventException("SecurityError", "isolated frame access denied")) }, nil)
	}
	for _, key := range []string{"window", "self"} {
		defineFrozen(w, key, w)
	}
	s.accessor(w, "closed", func(goja.Value) goja.Value { return s.vm.ToValue(s.cfg.FrameAlive == nil || !s.cfg.FrameAlive(id)) }, nil)
	s.method(w, "postMessage", func(c goja.FunctionCall) goja.Value {
		if c.This != w {
			panic(s.vm.NewTypeError("not a frame Window"))
		}
		if len(c.Arguments) == 0 {
			panic(s.vm.NewTypeError("message required"))
		}
		target := s.vm.ToValue("/")
		transfer := goja.Undefined()
		second := c.Argument(1)
		if opt, ok := second.(*goja.Object); ok && len(c.Arguments) < 3 {
			target = value(opt, "targetOrigin")
			transfer = value(opt, "transfer")
			if goja.IsUndefined(target) {
				target = s.vm.ToValue("/")
			}
		} else if !goja.IsUndefined(second) && !goja.IsNull(second) || len(c.Arguments) >= 3 {
			target = second
			transfer = c.Argument(2)
		}
		to := s.postMessageOriginString(target)
		if to != "/" && to != "*" {
			u, err := weburl.Parse(to)
			if err != nil {
				panic(s.eventException("SyntaxError", "invalid target origin"))
			}
			to = anchorOrigin(u)
		}
		if !goja.IsUndefined(transfer) {
			s.vm.ForOf(transfer, func(goja.Value) bool {
				panic(s.eventException("DataCloneError", "frame message transfers are not supported"))
			})
		}
		clone, _ := s.cloneMessage(c.Argument(0))
		// The first wire format supports JSON-compatible data only. Reject rather
		// than silently coercing cycles, buffers, undefined, or collection types.
		seen := map[*goja.Object]bool{}
		wireNodes, wireBytes := 0, 0
		var wire func(goja.Value) any
		fail := func() { panic(s.eventException("DataCloneError", "frame messages require JSON-compatible data")) }
		wire = func(v goja.Value) any {
			wireNodes++
			wireBytes += 16
			if str, ok := v.(goja.String); ok {
				wireBytes += str.Length() * 2
			}
			if wireNodes > maxMessageCloneNodes || wireBytes > maxMessageCloneBytes {
				s.messageQuota()
			}
			if goja.IsUndefined(v) {
				fail()
			}
			if goja.IsNull(v) {
				return nil
			}
			if o, ok := v.(*goja.Object); ok {
				if seen[o] || o.ClassName() != "Object" && o.ClassName() != "Array" || o.ClassName() == "Object" && o.Prototype() != plainProto {
					fail()
				}
				seen[o] = true
				defer delete(seen, o)
				if o.ClassName() == "Array" {
					n := o.Get("length").ToInteger()
					if n > maxMessageCloneNodes {
						fail()
					}
					a := make([]any, int(n))
					for i := range a {
						a[i] = wire(o.Get(strconv.Itoa(i)))
					}
					return a
				}
				m := map[string]any{}
				for _, k := range o.Keys() {
					wireBytes += len(k)
					m[k] = wire(o.Get(k))
				}
				return m
			}
			typ := v.ExportType()
			if typ == nil {
				fail()
			}
			switch typ.Kind() {
			case reflect.String, reflect.Bool, reflect.Int64, reflect.Int32:
				return v.Export()
			case reflect.Float64, reflect.Float32:
				f := v.ToFloat()
				if math.IsInf(f, 0) || math.IsNaN(f) {
					fail()
				}
				return f
			default:
				fail()
			}
			return nil
		}
		// Encode plain Go values: page-defined Object.prototype.toJSON must
		// never transform an already cloned wire message.
		data, err := json.Marshal(wire(clone))
		if err != nil || len(data) > maxMessageCloneBytes {
			fail()
		}

		if s.cfg.PostFrame == nil {
			panic(s.eventException("SecurityError", "frame messaging disabled"))
		}
		if err := s.cfg.PostFrame(FramePost{id, to, data}); err != nil {
			panic(s.eventException("QuotaExceededError", err.Error()))
		}
		return goja.Undefined()
	})
	return w
}

// ReceiveFrameMessage is called on the receiving VM owner. Its queue and
// delivery tasks share the existing message/timer budgets.
func (s *Sandbox) ReceiveFrameMessage(source uint64, origin string, data []byte) error {
	return s.Run(func(*goja.Runtime) error {
		if len(data) > maxMessageCloneBytes || len(s.portMessages) >= s.cfg.MaxMessages || len(data) > s.cfg.MaxMessageBytes-s.messageBytes {
			s.messageQuota()
		}
		if !json.Valid(data) {
			return nil
		}
		v, err := s.jsonParse(goja.Undefined(), s.vm.ToValue(string(data)))
		if err != nil {
			return err
		}
		s.ensureMessageTask()
		s.portMessages = append(s.portMessages, portMessage{window: true, deliver: true, data: v, bytes: len(data), source: s.frameWindow(source), origin: origin})
		s.messageBytes += len(data)
		return nil
	})
}
func (s *Sandbox) SetFrameChildren(ids []uint64) {
	if s.disabled {
		return
	}
	global := s.vm.GlobalObject()
	for i := 0; i < s.frameChildCount; i++ {
		_ = global.Delete(strconv.Itoa(i))
	}
	for i, id := range ids {
		_ = global.DefineDataProperty(strconv.Itoa(i), s.frameWindow(id), goja.FLAG_FALSE, goja.FLAG_TRUE, goja.FLAG_TRUE)
	}
	s.frameChildCount = len(ids)
	_ = global.DefineDataProperty("length", s.vm.ToValue(len(ids)), goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_TRUE)
}

// FrameSpec reads a host-selected iframe node on the owning goroutine.
func (s *Sandbox) FrameSpec(id dom.NodeID) FrameSpec {
	n := s.tree.Node(id)
	if n == nil || n.Tag() != "iframe" {
		return FrameSpec{}
	}
	return s.frameSpec(n)
}
