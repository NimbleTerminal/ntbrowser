// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import (
	"github.com/dop251/goja"
	"nimbleterminal.dev/ntbrowser/sessionstorage"
)

func (sb *Sandbox) storageString(v goja.Value) string {
	v = v.ToString()
	if _, ok := v.(*goja.Symbol); ok {
		panic(sb.vm.NewTypeError("cannot convert Symbol to storage string"))
	}
	s, ok := v.(goja.String)
	if !ok {
		s = sb.vm.ToValue(v.String()).(goja.String)
	}
	if s.Length() > sessionstorage.MaxBytes/2 {
		return "\x00"
	} // odd-length sentinel cannot be a stored UTF-16 string
	b := make([]byte, s.Length()*2)
	for i := 0; i < s.Length(); i++ {
		c := s.CharAt(i)
		b[2*i], b[2*i+1] = byte(c), byte(c>>8)
	}
	return string(b)
}
func storageValue(raw string) goja.Value {
	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	return goja.StringFromUTF16(units)
}
func (s *Sandbox) storageError(name, message string) {
	e := s.vm.NewTypeError(message)
	_ = e.Set("name", name)
	panic(e)
}

type storageObject struct {
	s     *Sandbox
	area  *sessionstorage.Area
	proto *goja.Object
}

func (d *storageObject) visible(key string) bool {
	for p := d.proto; p != nil; p = p.Prototype() {
		for _, name := range p.GetOwnPropertyNames() {
			if name == key {
				return false
			}
		}
	}
	return true
}
func (d *storageObject) Get(key string) goja.Value {
	if !d.visible(key) {
		return nil
	}
	v, ok := d.area.Get(d.s.storageString(d.s.vm.ToValue(key)))
	if !ok {
		return nil
	}
	return storageValue(v)
}
func (d *storageObject) Set(key string, v goja.Value) bool {
	d.set(d.s.storageString(d.s.vm.ToValue(key)), d.s.storageString(v))
	return true
}
func (d *storageObject) set(key, value string) {
	if len(key)%2 != 0 || len(value)%2 != 0 {
		d.s.storageError("QuotaExceededError", "session storage input exceeds maximum quota")
	}
	if err := d.area.Set(key, value); err != nil {
		d.s.storageError("QuotaExceededError", err.Error())
	}
}
func (d *storageObject) Has(key string) bool {
	if !d.visible(key) {
		return false
	}
	_, ok := d.area.Get(d.s.storageString(d.s.vm.ToValue(key)))
	return ok
}
func (d *storageObject) Delete(key string) bool {
	d.area.Remove(d.s.storageString(d.s.vm.ToValue(key)))
	return true
}
func (d *storageObject) Keys() []string {
	var out []string
	for _, k := range d.area.Keys() {
		key := storageValue(k).String()
		if d.visible(key) {
			out = append(out, key)
		}
	}
	return out
}

func (s *Sandbox) installStorage() {
	vm := s.vm
	p := vm.NewObject()
	ctor := vm.ToValue(func(goja.ConstructorCall) *goja.Object { panic(vm.NewTypeError("Illegal constructor")) }).(*goja.Object)
	_ = ctor.Set("prototype", p)
	defineFrozen(p, "constructor", ctor)
	defineFrozen(vm.GlobalObject(), "Storage", ctor)
	_ = p.DefineDataPropertySymbol(goja.SymToStringTag, vm.ToValue("Storage"), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	var object *goja.Object
	var data *storageObject
	if s.cfg.SessionStorage != nil {
		data = &storageObject{s: s, area: s.cfg.SessionStorage, proto: p}
		object = vm.NewDynamicObject(data)
		_ = object.SetPrototype(p)
	}
	check := func(v goja.Value) {
		if object == nil || v != object {
			panic(vm.NewTypeError("not a Storage object"))
		}
	}
	s.accessor(p, "length", func(v goja.Value) goja.Value { check(v); return vm.ToValue(data.area.Len()) }, nil)
	for _, name := range []string{"getItem", "setItem", "removeItem", "clear", "key"} {
		s.method(p, name, func(c goja.FunctionCall) goja.Value {
			check(c.This)
			need := 1
			if name == "clear" {
				need = 0
			}
			if name == "setItem" {
				need = 2
			}
			if len(c.Arguments) < need {
				panic(vm.NewTypeError("missing storage argument"))
			}
			switch name {
			case "clear":
				data.area.Clear()
			case "key":
				keys := data.area.Keys()
				i := webIDLUint32(c.Argument(0))
				if uint64(i) >= uint64(len(keys)) {
					return goja.Null()
				}
				return storageValue(keys[i])
			case "getItem":
				v, ok := data.area.Get(s.storageString(c.Argument(0)))
				if !ok {
					return goja.Null()
				}
				return storageValue(v)
			case "setItem":
				data.set(s.storageString(c.Argument(0)), s.storageString(c.Argument(1)))
			case "removeItem":
				data.area.Remove(s.storageString(c.Argument(0)))
			}
			return goja.Undefined()
		})
	}
	s.accessor(vm.GlobalObject(), "sessionStorage", func(goja.Value) goja.Value {
		if object == nil {
			s.storageError("SecurityError", "session storage is disabled or this document has no supported origin")
		}
		return object
	}, nil)
}
