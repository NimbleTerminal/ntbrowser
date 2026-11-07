// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import (
	"github.com/dop251/goja"
	"nimbleterminal.dev/ntbrowser/dom"
	"strconv"
	"strings"
)

func (s *Sandbox) styleOwner(v goja.Value) *goja.Object {
	return s.brand(v, "CSSStyleDeclaration").Get("owner").(*goja.Object)
}
func (s *Sandbox) styleRead(v goja.Value) string {
	owner := s.styleOwner(v)
	if ref, ok := s.foreignRefs[owner]; ok {
		return s.domRequest(DOMRequest{Ref: ref, Op: "getAttribute", Key: "style"}).Text
	}
	text, _ := s.node(owner).Attr("style")
	return text
}
func (s *Sandbox) styleWrite(v goja.Value, text string) {
	owner := s.styleOwner(v)
	if ref, ok := s.foreignRefs[owner]; ok {
		s.domRequest(DOMRequest{Ref: ref, Op: "setAttribute", Key: "style", Text: text})
		return
	}
	s.must(s.tree.SetAttr(s.node(owner), "style", text))
}
func (s *Sandbox) styleString(v goja.Value) string {
	if goja.IsNull(v) {
		return ""
	}
	return s.foreignText(v, dom.MaxFragmentBytes)
}
func (s *Sandbox) styleGet(v goja.Value, name string, priority bool) string {
	for _, d := range dom.ParseInlineStyle(s.styleRead(v)) {
		if d.Name == dom.StyleName(name) {
			if priority {
				return d.Priority
			}
			return d.Value
		}
	}
	return ""
}
func (s *Sandbox) installStyle() {
	vm := s.vm
	p := vm.NewObject()
	s.styleProto = p
	s.styleSym = goja.NewSymbol("ntbrowser.style")
	ctor := vm.ToValue(func(goja.ConstructorCall) *goja.Object { panic(vm.NewTypeError("Illegal constructor")) }).(*goja.Object)
	_ = ctor.Set("prototype", p)
	defineFrozen(p, "constructor", ctor)
	defineFrozen(vm.GlobalObject(), "CSSStyleDeclaration", ctor)
	s.accessor(p, "cssText", func(v goja.Value) goja.Value {
		return vm.ToValue(dom.SerializeInlineStyle(dom.ParseInlineStyle(s.styleRead(v))))
	}, func(t, v goja.Value) {
		s.styleWrite(t, dom.SerializeInlineStyle(dom.ParseInlineStyle(s.styleString(v))))
	})
	s.accessor(p, "length", func(v goja.Value) goja.Value { return vm.ToValue(len(dom.ParseInlineStyle(s.styleRead(v)))) }, nil)
	s.accessor(p, "parentRule", func(v goja.Value) goja.Value { s.styleOwner(v); return goja.Null() }, nil)
	s.method(p, "item", func(c goja.FunctionCall) goja.Value {
		ds := dom.ParseInlineStyle(s.styleRead(c.This))
		i := webIDLUint32(c.Argument(0))
		if uint64(i) >= uint64(len(ds)) {
			return vm.ToValue("")
		}
		return vm.ToValue(ds[i].Name)
	})
	for _, method := range []string{"getPropertyValue", "getPropertyPriority"} {
		s.method(p, method, func(c goja.FunctionCall) goja.Value {
			return vm.ToValue(s.styleGet(c.This, c.Argument(0).String(), method == "getPropertyPriority"))
		})
	}
	s.method(p, "setProperty", func(c goja.FunctionCall) goja.Value {
		s.styleOwner(c.This)
		name := s.foreignText(c.Argument(0), 1024)
		val := s.styleString(c.Argument(1))
		priority := ""
		if !goja.IsUndefined(c.Argument(2)) {
			priority = s.styleString(c.Argument(2))
		}
		if text, ok := dom.SetInlineProperty(s.styleRead(c.This), name, val, priority); ok {
			s.styleWrite(c.This, text)
		}
		return goja.Undefined()
	})
	s.method(p, "removeProperty", func(c goja.FunctionCall) goja.Value {
		name := s.foreignText(c.Argument(0), 1024)
		old := s.styleGet(c.This, name, false)
		if text, ok := dom.SetInlineProperty(s.styleRead(c.This), name, "", ""); ok {
			s.styleWrite(c.This, text)
		}
		return vm.ToValue(old)
	})
	s.accessor(s.elemProto, "style", func(v goja.Value) goja.Value { s.node(v); return s.elementStyle(v.(*goja.Object)) }, func(t, v goja.Value) {
		style := s.elementStyle(t.(*goja.Object))
		s.styleWrite(style, dom.SerializeInlineStyle(dom.ParseInlineStyle(s.styleString(v))))
	})
}
func (s *Sandbox) elementStyle(owner *goja.Object) goja.Value {
	if cached := owner.GetSymbol(s.styleSym); cached != nil {
		return cached
	}
	vm := s.vm
	target := vm.NewObject()
	_ = target.SetPrototype(s.styleProto)
	state := s.record()
	_ = state.Set("kind", "CSSStyleDeclaration")
	_ = state.Set("owner", owner)
	s.putPrivate(target, state)
	aliases := map[string]string{"cssFloat": "float"}
	for _, name := range dom.SupportedStyleProperties {
		aliases[name] = name
		var b strings.Builder
		upper := false
		for _, c := range name {
			if c == '-' {
				upper = true
				continue
			}
			if upper {
				b.WriteString(strings.ToUpper(string(c)))
				upper = false
			} else {
				b.WriteRune(c)
			}
		}
		aliases[b.String()] = name
	}
	index := func(key string) (int, bool) {
		i, e := strconv.Atoi(key)
		return i, e == nil && i >= 0 && strconv.Itoa(i) == key
	}
	proxy := vm.NewProxy(target, &goja.ProxyTrapConfig{
		Get: func(t *goja.Object, key string, _ goja.Value) goja.Value {
			if name, ok := aliases[key]; ok {
				return vm.ToValue(s.styleGet(target, name, false))
			}
			if i, ok := index(key); ok {
				ds := dom.ParseInlineStyle(s.styleRead(target))
				if i < len(ds) {
					return vm.ToValue(ds[i].Name)
				}
				return goja.Undefined()
			}
			v := t.Get(key)
			if v == nil {
				return goja.Undefined()
			}
			return v
		},
		Set: func(t *goja.Object, key string, v, _ goja.Value) bool {
			if name, ok := aliases[key]; ok {
				val := s.styleString(v)
				if text, ok := dom.SetInlineProperty(s.styleRead(target), name, val, ""); ok {
					s.styleWrite(target, text)
				}
				return true
			}
			if _, ok := index(key); ok {
				return false
			}
			if key == "length" || key == "parentRule" {
				return false
			}
			s.must(t.Set(key, v))
			return true
		},
		Has: func(t *goja.Object, key string) bool {
			if _, ok := aliases[key]; ok {
				return true
			}
			if i, ok := index(key); ok {
				return i < len(dom.ParseInlineStyle(s.styleRead(target)))
			}
			return t.Get(key) != nil
		},
		DefineProperty:    func(*goja.Object, string, goja.PropertyDescriptor) bool { return false },
		PreventExtensions: func(*goja.Object) bool { return false },
		GetOwnPropertyDescriptor: func(_ *goja.Object, key string) goja.PropertyDescriptor {
			if i, ok := index(key); ok {
				ds := dom.ParseInlineStyle(s.styleRead(target))
				if i < len(ds) {
					return goja.PropertyDescriptor{Value: vm.ToValue(ds[i].Name), Writable: goja.FLAG_FALSE, Enumerable: goja.FLAG_TRUE, Configurable: goja.FLAG_TRUE}
				}
			}
			return goja.PropertyDescriptor{}
		},
		OwnKeys: func(*goja.Object) *goja.Object {
			ds := dom.ParseInlineStyle(s.styleRead(target))
			keys := make([]any, len(ds))
			for i := range ds {
				keys[i] = strconv.Itoa(i)
			}
			return vm.NewArray(keys...)
		},
	})
	style := vm.ToValue(proxy)
	s.putPrivate(style, state)
	_ = owner.DefineDataPropertySymbol(s.styleSym, style, goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	return style
}
