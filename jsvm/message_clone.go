// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"reflect"

	"github.com/dop251/goja"
)

const maxMessageCloneBytes = 1 << 20
const maxMessageCloneNodes = 4096
const maxMessageCloneDepth = 64

// makeMessageCloner captures intrinsics before page code runs. It never exports
// an arbitrary object, preserves graph identity, and rejects host capabilities.
// Transfers and less common structured-clone types are deliberately unsupported.
func (s *Sandbox) makeMessageCloner() func(goja.Value) (goja.Value, int) {
	vm := s.vm
	ctors := map[string]*goja.Object{}
	for _, name := range []string{"ArrayBuffer", "DataView", "Date", "Map", "Set", "Uint8Array", "Uint8ClampedArray", "Int8Array", "Uint16Array", "Int16Array", "Uint32Array", "Int32Array", "Float16Array", "Float32Array", "Float64Array", "BigInt64Array", "BigUint64Array"} {
		if o, ok := vm.Get(name).(*goja.Object); ok {
			ctors[name] = o
		}
	}
	getDescriptor, _ := goja.AssertFunction(vm.Get("Object").(*goja.Object).Get("getOwnPropertyDescriptor"))
	getter := func(proto *goja.Object, key goja.Value) goja.Callable {
		d, err := getDescriptor(goja.Undefined(), proto, key)
		if err != nil {
			panic(err)
		}
		f, _ := goja.AssertFunction(d.(*goja.Object).Get("get"))
		return f
	}
	method := func(name, key string) goja.Callable {
		f, _ := goja.AssertFunction(ctors[name].Get("prototype").(*goja.Object).Get(key))
		return f
	}
	typedProto := ctors["Uint8Array"].Get("prototype").(*goja.Object).Prototype()
	typedTag := getter(typedProto, goja.SymToStringTag)
	typedBuffer := getter(typedProto, vm.ToValue("buffer"))
	typedOffset := getter(typedProto, vm.ToValue("byteOffset"))
	typedLength := getter(typedProto, vm.ToValue("length"))
	dataProto := ctors["DataView"].Get("prototype").(*goja.Object)
	dataBuffer := getter(dataProto, vm.ToValue("buffer"))
	dataOffset := getter(dataProto, vm.ToValue("byteOffset"))
	dataLength := getter(dataProto, vm.ToValue("byteLength"))
	dateTime, mapEach, mapSet, setEach, setAdd := method("Date", "getTime"), method("Map", "forEach"), method("Map", "set"), method("Set", "forEach"), method("Set", "add")
	call := func(f goja.Callable, this goja.Value, args ...goja.Value) goja.Value {
		v, err := f(this, args...)
		if err != nil {
			panic(err)
		}
		return v
	}
	construct := func(name string, args ...goja.Value) *goja.Object {
		o, err := vm.New(ctors[name], args...)
		if err != nil {
			panic(err)
		}
		return o
	}
	proxyType := reflect.TypeOf(goja.Proxy{})
	bufferType := reflect.TypeOf(goja.ArrayBuffer{})
	plainType := reflect.TypeOf(map[string]interface{}{})
	objectProto := vm.Get("Object").(*goja.Object).Get("prototype").(*goja.Object)
	mapSize := getter(ctors["Map"].Get("prototype").(*goja.Object), vm.ToValue("size"))
	setSize := getter(ctors["Set"].Get("prototype").(*goja.Object), vm.ToValue("size"))
	weakHas := []goja.Callable{}
	for _, name := range []string{"WeakMap", "WeakSet"} {
		f, _ := goja.AssertFunction(vm.Get(name).(*goja.Object).Get("prototype").(*goja.Object).Get("has"))
		weakHas = append(weakHas, f)
	}
	return func(input goja.Value) (goja.Value, int) {
		seen := map[*goja.Object]goja.Value{}
		bytes, nodes := 0, 0
		fail := func() { panic(s.eventException("DataCloneError", "unsupported message data")) }
		charge := func(n int) {
			if n > min(maxMessageCloneBytes, s.cfg.MaxMessageBytes)-bytes {
				s.messageQuota()
			}
			bytes += n
		}
		var clone func(goja.Value, int) goja.Value
		clone = func(v goja.Value, depth int) goja.Value {
			nodes++
			if nodes > maxMessageCloneNodes || depth > maxMessageCloneDepth {
				s.messageQuota()
			}
			charge(16)
			if _, ok := v.(*goja.Symbol); ok {
				fail()
			}
			if str, ok := v.(goja.String); ok {
				if str.Length() > maxMessageCloneBytes/2 {
					s.messageQuota()
				}
				charge(str.Length() * 2)
				return str
			}
			o, object := v.(*goja.Object)
			if !object {
				if v.ExportType() != nil && v.ExportType().Kind() == reflect.Pointer {
					fail()
				}
				return v
			}
			if copied, ok := seen[o]; ok {
				return copied
			}
			if o.ExportType() == proxyType {
				fail()
			}
			if _, ok := goja.AssertFunction(o); ok {
				fail()
			}
			if s.private(o) != nil {
				fail()
			}
			if o.ExportType() == bufferType {
				ab := o.Export().(goja.ArrayBuffer)
				if ab.Detached() {
					fail()
				}
				charge(len(ab.Bytes()))
				out := vm.ToValue(vm.NewArrayBuffer(append([]byte(nil), ab.Bytes()...)))
				seen[o] = out
				return out
			}
			// Native getters validate view brands; own shadow properties are ignored.
			tag := call(typedTag, o)
			var buffer, offset, length goja.Value
			name := ""
			if !goja.IsUndefined(tag) {
				name = tag.String()
				buffer = call(typedBuffer, o)
				offset = call(typedOffset, o)
				length = call(typedLength, o)
			} else if b, err := dataBuffer(o); err == nil {
				name = "DataView"
				buffer = b
				offset = call(dataOffset, o)
				length = call(dataLength, o)
			}
			if name != "" {
				copied := clone(buffer, depth+1)
				out := construct(name, copied, offset, length)
				seen[o] = out
				return out
			}
			class := o.ClassName()
			if _, err := mapSize(o); err == nil {
				class = "Map"
			} else if _, err := setSize(o); err == nil {
				class = "Set"
			}
			for _, has := range weakHas {
				if _, err := has(o, o); err == nil {
					fail()
				}
			}
			switch class {
			case "Date":
				out := construct("Date", call(dateTime, o))
				seen[o] = out
				return out
			case "Map", "Set":
				name := class
				out := construct(name)
				seen[o] = out
				// Snapshot before recursively cloning values, whose getters may mutate
				// the source collection. Bound even an adversarially growing collection.
				var entries [][2]goja.Value
				each := mapEach
				if name == "Set" {
					each = setEach
				}
				call(each, o, vm.ToValue(func(c goja.FunctionCall) goja.Value {
					if len(entries) >= maxMessageCloneNodes {
						s.messageQuota()
					}
					charge(16)
					entries = append(entries, [2]goja.Value{c.Argument(1), c.Argument(0)})
					return goja.Undefined()
				}))
				for _, entry := range entries {
					if name == "Map" {
						call(mapSet, out, clone(entry[0], depth+1), clone(entry[1], depth+1))
					} else {
						call(setAdd, out, clone(entry[1], depth+1))
					}
				}
				return out
			case "Array", "Object":
				var out *goja.Object
				if o.ClassName() == "Array" {
					length := value(o, "length").ToInteger()
					if length > maxMessageCloneNodes {
						s.messageQuota()
					}
					out = vm.NewArray()
					_ = out.Set("length", length)
				} else {
					if o.ExportType() != plainType || o.Prototype() != nil && o.Prototype() != objectProto {
						fail()
					}
					out = vm.NewObject()
				}
				seen[o] = out
				keys := o.Keys()
				if len(keys) > maxMessageCloneNodes {
					s.messageQuota()
				}
				for _, key := range keys {
					charge(len(key) * 2)
					v := clone(value(o, key), depth+1)
					_ = out.DefineDataProperty(key, v, goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_TRUE)
				}
				return out
			default:
				fail()
			}
			return goja.Undefined()
		}
		out := clone(input, 0)
		return out, bytes
	}
}
