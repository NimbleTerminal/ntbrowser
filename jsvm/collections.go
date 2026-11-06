// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import (
	"strconv"
	"strings"

	"github.com/dop251/goja"
	"nimbleterminal.dev/ntbrowser/dom"
)

// The root wrapper keeps a detached subtree alive while its collection exists.
// No matches or wrappers are cached: each lookup sees the current bounded DOM.
type tagCollection struct {
	sandbox         *Sandbox
	root            *dom.Node
	rootPin         goja.Value
	tag             string
	item, namedItem goja.Value
}

func (c *tagCollection) nodes() []*dom.Node {
	var nodes []*dom.Node
	dom.Walk(c.root, func(n *dom.Node) {
		if n != c.root && n.Kind() == dom.Element && (c.tag == "*" || n.Tag() == c.tag) {
			nodes = append(nodes, n)
		}
	})
	return nodes
}
func (c *tagCollection) named(name string) *dom.Node {
	if name == "" {
		return nil
	}
	for _, n := range c.nodes() {
		id, _ := n.Attr("id")
		key, _ := n.Attr("name")
		if id == name || key == name {
			return n
		}
	}
	return nil
}
func (c *tagCollection) Get(key string) goja.Value {
	switch key {
	case "length":
		return c.sandbox.vm.ToValue(len(c.nodes()))
	case "item":
		return c.item
	case "namedItem":
		return c.namedItem
	}
	if i, err := strconv.ParseUint(key, 10, 32); err == nil && strconv.FormatUint(i, 10) == key {
		nodes := c.nodes()
		if i < uint64(len(nodes)) {
			return c.sandbox.wrap(nodes[i])
		}
		return nil
	}
	// Prototype members take precedence over named properties.
	for p := c.sandbox.collectionProto; p != nil; p = p.Prototype() {
		for _, name := range p.GetOwnPropertyNames() {
			if key == name {
				return nil
			}
		}
	}
	if n := c.named(key); n != nil {
		return c.sandbox.wrap(n)
	}
	return nil
}
func (c *tagCollection) Has(key string) bool         { return c.Get(key) != nil }
func (c *tagCollection) Set(string, goja.Value) bool { return false }
func (c *tagCollection) Delete(string) bool          { return false }
func (c *tagCollection) Keys() []string {
	nodes := c.nodes()
	keys := make([]string, len(nodes))
	for i := range keys {
		keys[i] = strconv.Itoa(i)
	}
	return keys
}
func (s *Sandbox) installCollections() {
	p := s.vm.NewObject()
	s.collectionProto = p
	ctor := s.vm.ToValue(func(goja.ConstructorCall) *goja.Object { panic(s.vm.NewTypeError("Illegal constructor")) }).(*goja.Object)
	_ = ctor.Set("prototype", p)
	defineFrozen(p, "constructor", ctor)
	defineFrozen(s.vm.GlobalObject(), "HTMLCollection", ctor)
	_ = p.DefineDataPropertySymbol(goja.SymToStringTag, s.vm.ToValue("HTMLCollection"), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	// Capture the pristine generic Array iterator before page code can replace it.
	iterator := s.vm.Get("Array").ToObject(s.vm).Get("prototype").ToObject(s.vm).Get("values")
	_ = p.DefineDataPropertySymbol(goja.SymIterator, iterator, goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
}
func (s *Sandbox) tagCollection(pin goja.Value, root *dom.Node, tag string) goja.Value {
	c := &tagCollection{sandbox: s, root: root, rootPin: pin, tag: strings.ToLower(tag)}
	object := s.vm.NewDynamicObject(c)
	_ = object.SetPrototype(s.collectionProto)
	check := func(call goja.FunctionCall) {
		if call.This != object {
			panic(s.vm.NewTypeError("not this HTMLCollection"))
		}
		if len(call.Arguments) == 0 {
			panic(s.vm.NewTypeError("missing collection argument"))
		}
	}
	c.item = s.vm.ToValue(func(call goja.FunctionCall) goja.Value {
		check(call)
		i := webIDLUint32(call.Argument(0))
		nodes := c.nodes()
		if uint64(i) >= uint64(len(nodes)) {
			return goja.Null()
		}
		return s.wrap(nodes[i])
	})
	c.namedItem = s.vm.ToValue(func(call goja.FunctionCall) goja.Value {
		check(call)
		return s.wrap(c.named(call.Argument(0).String()))
	})
	return object
}
