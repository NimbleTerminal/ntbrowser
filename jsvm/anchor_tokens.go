// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"strconv"
	"strings"

	"github.com/dop251/goja"
)

func asciiSpace(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' }

// The token list retains its owner wrapper: keeping only a.relList alive
// must also keep a detached anchor alive. All reads reflect the live DOM.
func (s *Sandbox) anchorRelList(owner *goja.Object) goja.Value {
	vm := s.vm
	raw := func() string { v, _ := s.anchorNode(owner).Attr("rel"); return v }
	tokens := func() []string {
		var out []string
		seen := map[string]bool{}
		for _, v := range strings.FieldsFunc(raw(), asciiSpace) {
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
		return out
	}
	write := func(v string) { s.must(s.tree.SetAttr(s.anchorNode(owner), "rel", v)) }
	check := func(v goja.Value) string {
		token := v.String()
		name := ""
		if token == "" {
			name = "SyntaxError"
		} else if strings.ContainsAny(token, " \t\n\r\f") {
			name = "InvalidCharacterError"
		}
		if name != "" {
			e := vm.NewTypeError("invalid rel token")
			_ = e.Set("name", name)
			panic(e)
		}
		return token
	}
	proto := vm.NewObject()
	s.accessor(proto, "length", func(goja.Value) goja.Value { return vm.ToValue(len(tokens())) }, nil)
	s.accessor(proto, "value", func(goja.Value) goja.Value { return vm.ToValue(raw()) }, func(_, v goja.Value) { write(v.String()) })
	s.method(proto, "toString", func(goja.FunctionCall) goja.Value { return vm.ToValue(raw()) })
	s.method(proto, "item", func(c goja.FunctionCall) goja.Value {
		ts := tokens()
		i := webIDLUint32(c.Argument(0))
		if uint64(i) >= uint64(len(ts)) {
			return goja.Null()
		}
		return vm.ToValue(ts[i])
	})
	s.method(proto, "contains", func(c goja.FunctionCall) goja.Value { return vm.ToValue(contains(tokens(), c.Argument(0).String())) })
	for _, name := range []string{"add", "remove"} {
		s.method(proto, name, func(c goja.FunctionCall) goja.Value {
			args := make([]string, len(c.Arguments))
			for i, v := range c.Arguments {
				args[i] = check(v)
			}
			ts := tokens()
			for _, v := range args {
				if name == "remove" {
					ts = without(ts, v)
				} else if !contains(ts, v) {
					ts = append(ts, v)
				}
			}
			write(strings.Join(ts, " "))
			return goja.Undefined()
		})
	}
	s.method(proto, "toggle", func(c goja.FunctionCall) goja.Value {
		token := check(c.Argument(0))
		ts := tokens()
		has := contains(ts, token)
		want := !has
		if len(c.Arguments) > 1 {
			want = c.Argument(1).ToBoolean()
		}
		if want != has {
			if want {
				ts = append(ts, token)
			} else {
				ts = without(ts, token)
			}
			write(strings.Join(ts, " "))
		}
		return vm.ToValue(want)
	})
	s.method(proto, "replace", func(c goja.FunctionCall) goja.Value {
		old, newToken := check(c.Argument(0)), check(c.Argument(1))
		ts := tokens()
		if !contains(ts, old) {
			return vm.ToValue(false)
		}
		// An existing replacement retains whichever position occurs first.
		out := make([]string, 0, len(ts))
		added := false
		for _, v := range ts {
			if v == old || v == newToken {
				if !added {
					out = append(out, newToken)
					added = true
				}
			} else {
				out = append(out, v)
			}
		}
		write(strings.Join(out, " "))
		return vm.ToValue(true)
	})
	// No browsing-context/opener or per-link referrer processing is exposed.
	s.method(proto, "supports", func(c goja.FunctionCall) goja.Value { _ = c.Argument(0).String(); return vm.ToValue(false) })
	var list goja.Value
	s.method(proto, "forEach", func(c goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(c.Argument(0))
		if !ok {
			panic(vm.NewTypeError("callback must be a function"))
		}
		for i := 0; ; i++ {
			ts := tokens()
			if i >= len(ts) {
				break
			}
			if _, err := fn(c.Argument(1), vm.ToValue(ts[i]), vm.ToValue(i), list); err != nil {
				panic(err)
			}
		}
		return goja.Undefined()
	})
	var values goja.Value
	for _, kind := range []string{"values", "keys", "entries"} {
		fn := vm.ToValue(func(goja.FunctionCall) goja.Value {
			i := 0
			it := vm.NewObject()
			s.method(it, "next", func(goja.FunctionCall) goja.Value {
				ts := tokens()
				out := vm.NewObject()
				done := i >= len(ts)
				_ = out.Set("done", done)
				v := goja.Undefined()
				if !done {
					switch kind {
					case "keys":
						v = vm.ToValue(i)
					case "entries":
						v = vm.NewArray(i, ts[i])
					default:
						v = vm.ToValue(ts[i])
					}
					i++
				}
				_ = out.Set("value", v)
				return out
			})
			_ = it.DefineDataPropertySymbol(goja.SymIterator, vm.ToValue(func(goja.FunctionCall) goja.Value { return it }), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
			return it
		})
		defineFrozen(proto, kind, fn)
		if kind == "values" {
			values = fn
		}
	}
	_ = proto.DefineDataPropertySymbol(goja.SymIterator, values, goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	target := vm.NewObject()
	_ = target.SetPrototype(proto)
	index := func(key string) (int, bool) {
		i, err := strconv.Atoi(key)
		return i, err == nil && i >= 0 && strconv.Itoa(i) == key
	}
	proxy := vm.NewProxy(target, &goja.ProxyTrapConfig{
		Get: func(t *goja.Object, key string, _ goja.Value) goja.Value {
			if i, ok := index(key); ok {
				ts := tokens()
				if i < len(ts) {
					return vm.ToValue(ts[i])
				}
				return goja.Undefined()
			}
			return t.Get(key)
		},
		Has: func(t *goja.Object, key string) bool {
			if i, ok := index(key); ok {
				return i < len(tokens())
			}
			return t.Get(key) != nil
		},
		Set: func(_ *goja.Object, key string, v, receiver goja.Value) bool {
			if key == "value" {
				write(v.String())
				return true
			}
			return false
		},
		DefineProperty:    func(*goja.Object, string, goja.PropertyDescriptor) bool { return false },
		DeleteProperty:    func(_ *goja.Object, key string) bool { i, ok := index(key); return !ok || i >= len(tokens()) },
		PreventExtensions: func(*goja.Object) bool { return false },
		SetPrototypeOf:    func(*goja.Object, *goja.Object) bool { return false },
		SetSym:            func(*goja.Object, *goja.Symbol, goja.Value, goja.Value) bool { return false },
		DefinePropertySym: func(*goja.Object, *goja.Symbol, goja.PropertyDescriptor) bool { return false },
		GetOwnPropertyDescriptor: func(_ *goja.Object, key string) goja.PropertyDescriptor {
			if i, ok := index(key); ok {
				ts := tokens()
				if i < len(ts) {
					return goja.PropertyDescriptor{Value: vm.ToValue(ts[i]), Writable: goja.FLAG_FALSE, Enumerable: goja.FLAG_TRUE, Configurable: goja.FLAG_TRUE}
				}
			}
			return goja.PropertyDescriptor{}
		},
		OwnKeys: func(*goja.Object) *goja.Object {
			ts := tokens()
			keys := make([]any, len(ts))
			for i := range ts {
				keys[i] = strconv.Itoa(i)
			}
			return vm.NewArray(keys...)
		},
	})
	list = vm.ToValue(proxy)
	return list
}
