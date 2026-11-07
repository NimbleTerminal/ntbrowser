// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import (
	"fmt"
	"github.com/dop251/goja"
	"nimbleterminal.dev/ntbrowser/dom"
	"strings"
)

// Only data and revision-bound IDs cross realms. No Goja values cross this API.
type DOMRef struct {
	Frame, Revision uint64
	Node, Document  dom.NodeID
	Kind            int
}
type DOMRequest struct {
	X, Y          float64
	Relative      bool
	Ref           DOMRef
	Op, Key, Text string
	Other, Before DOMRef
}
type DOMReply struct {
	X, Y float64
	Ref  DOMRef
	Refs []DOMRef
	Text string
	Bool bool
}
type DOMError struct{ Name, Message string }

func (e *DOMError) Error() string         { return e.Message }
func domError(name, message string) error { return &DOMError{name, message} }

const MaxForeignNodes = 256

func (s *Sandbox) foreignText(v goja.Value, limit int) string {
	str := v.ToString()
	if text, ok := str.(goja.String); ok && text.Length() > limit {
		panic(s.eventException("QuotaExceededError", "shared DOM input too large"))
	}
	text := str.String()
	if len(text) > limit {
		panic(s.eventException("QuotaExceededError", "shared DOM input too large"))
	}
	return text
}

func (s *Sandbox) domRequest(r DOMRequest) DOMReply {
	if s.cfg.FrameDOM == nil {
		panic(s.eventException("SecurityError", "shared frame DOM disabled"))
	}
	reply, err := s.cfg.FrameDOM(r)
	if err != nil {
		if e, ok := err.(*DOMError); ok {
			panic(s.eventException(e.Name, e.Message))
		}
		panic(s.eventException("InvalidStateError", err.Error()))
	}
	return reply
}
func (s *Sandbox) frameDocument(id uint64, nullable bool) goja.Value {
	if s.cfg.FrameDOM == nil {
		if nullable {
			return goja.Null()
		}
		panic(s.eventException("SecurityError", "shared frame DOM disabled"))
	}
	reply, err := s.cfg.FrameDOM(DOMRequest{Ref: DOMRef{Frame: id}, Op: "document"})
	if err != nil {
		if e, ok := err.(*DOMError); ok && e.Name == "SecurityError" && nullable {
			return goja.Null()
		}
		if e, ok := err.(*DOMError); ok {
			panic(s.eventException(e.Name, e.Message))
		}
		panic(s.eventException("InvalidStateError", err.Error()))
	}
	return s.wrapForeign(reply.Ref)
}
func (s *Sandbox) wrapForeign(ref DOMRef) goja.Value {
	if ref.Node == 0 {
		return goja.Null()
	}
	if w := s.foreignNodes[ref]; w != nil {
		return w
	}
	if len(s.foreignNodes) >= MaxForeignNodes {
		panic(s.eventException("QuotaExceededError", "shared DOM handle limit reached"))
	}
	s.domRequest(DOMRequest{Ref: ref, Op: "retain"})
	w := s.vm.NewObject()
	if s.foreignNodes == nil {
		s.foreignNodes = map[DOMRef]*goja.Object{}
		s.foreignRefs = map[*goja.Object]DOMRef{}
	}
	s.foreignNodes[ref] = w
	s.foreignRefs[w] = ref
	call := func(op, key, text string) DOMReply {
		return s.domRequest(DOMRequest{Ref: ref, Op: op, Key: key, Text: text})
	}
	valid := func(c goja.FunctionCall) {
		if c.This != w {
			panic(s.vm.NewTypeError("illegal shared DOM receiver"))
		}
	}
	other := func(v goja.Value) DOMRef {
		o, ok := v.(*goja.Object)
		if !ok {
			panic(s.vm.NewTypeError("shared DOM node required"))
		}
		r, ok := s.foreignRefs[o]
		if !ok || r.Frame != ref.Frame || r.Revision != ref.Revision {
			panic(s.eventException("WrongDocumentError", "cross-document node adoption is unsupported"))
		}
		return r
	}
	for _, key := range []string{"nodeName", "tagName", "textContent", "innerHTML", "id", "className", "value"} {
		s.accessor(w, key, func(goja.Value) goja.Value { return s.vm.ToValue(call("get", key, "").Text) }, func(_, v goja.Value) {
			if key == "nodeName" || key == "tagName" {
				panic(s.vm.NewTypeError("read-only property"))
			}
			text := ""
			if !goja.IsNull(v) {
				text = s.foreignText(v, dom.MaxFragmentBytes)
			}
			call("set", key, text)
		})
	}
	s.accessor(w, "nodeType", func(goja.Value) goja.Value { call("check", "", ""); return s.vm.ToValue(ref.Kind) }, nil)
	s.accessor(w, "isConnected", func(goja.Value) goja.Value { return s.vm.ToValue(call("connected", "", "").Bool) }, nil)
	for _, key := range []string{"parentNode", "parentElement", "firstChild", "lastChild", "firstElementChild", "lastElementChild", "body", "head", "documentElement", "ownerDocument"} {
		s.accessor(w, key, func(goja.Value) goja.Value { return s.wrapForeign(call("node", key, "").Ref) }, nil)
	}
	for _, key := range []string{"children", "childNodes"} {
		s.accessor(w, key, func(goja.Value) goja.Value {
			refs := call("children", key, "").Refs
			out := s.vm.NewArray()
			for i, r := range refs {
				_ = out.Set(fmt.Sprint(i), s.wrapForeign(r))
			}
			s.method(out, "item", func(c goja.FunctionCall) goja.Value {
				i := c.Argument(0).ToInteger()
				if i < 0 || i >= int64(len(refs)) {
					return goja.Null()
				}
				return s.wrapForeign(refs[i])
			})
			return out
		}, nil)
	}
	if ref.Kind == 9 {
		s.accessor(w, "defaultView", func(goja.Value) goja.Value { call("check", "", ""); return s.frameWindow(ref.Frame) }, nil)
		for _, method := range []string{"createElement", "createTextNode", "getElementById"} {
			s.method(w, method, func(c goja.FunctionCall) goja.Value {
				valid(c)
				return s.wrapForeign(call(method, "", s.foreignText(c.Argument(0), dom.MaxFragmentBytes)).Ref)
			})
		}
	}
	s.method(w, "querySelector", func(c goja.FunctionCall) goja.Value {
		valid(c)
		return s.wrapForeign(call("query", "", s.foreignText(c.Argument(0), dom.MaxFragmentBytes)).Ref)
	})
	s.method(w, "querySelectorAll", func(c goja.FunctionCall) goja.Value {
		valid(c)
		refs := call("queryAll", "", s.foreignText(c.Argument(0), dom.MaxFragmentBytes)).Refs
		out := s.vm.NewArray()
		for i, r := range refs {
			_ = out.Set(fmt.Sprint(i), s.wrapForeign(r))
		}
		return out
	})
	for _, method := range []string{"getAttribute", "hasAttribute", "setAttribute", "removeAttribute"} {
		s.method(w, method, func(c goja.FunctionCall) goja.Value {
			valid(c)
			key := s.foreignText(c.Argument(0), dom.MaxFragmentBytes)
			text := ""
			if method == "setAttribute" {
				text = s.foreignText(c.Argument(1), dom.MaxFragmentBytes)
			}
			r := call(method, key, text)
			if method == "getAttribute" {
				if !r.Bool {
					return goja.Null()
				}
				return s.vm.ToValue(r.Text)
			}
			if method == "hasAttribute" {
				return s.vm.ToValue(r.Bool)
			}
			return goja.Undefined()
		})
	}
	for _, method := range []string{"appendChild", "insertBefore", "removeChild"} {
		s.method(w, method, func(c goja.FunctionCall) goja.Value {
			valid(c)
			r := DOMRequest{Ref: ref, Op: method, Other: other(c.Argument(0))}
			if method == "insertBefore" && !goja.IsNull(c.Argument(1)) && !goja.IsUndefined(c.Argument(1)) {
				r.Before = other(c.Argument(1))
			}
			s.domRequest(r)
			return c.Argument(0)
		})
	}
	s.method(w, "remove", func(c goja.FunctionCall) goja.Value { valid(c); call("remove", "", ""); return goja.Undefined() })
	// Fail explicitly instead of borrowing local node/event machinery with IDs
	// from another tree. Event callbacks and executable objects never cross.
	for _, method := range []string{"addEventListener", "removeEventListener", "dispatchEvent", "click", "focus", "blur", "cloneNode"} {
		s.method(w, method, func(goja.FunctionCall) goja.Value {
			panic(s.eventException("NotSupportedError", "shared DOM callbacks and activation are unsupported"))
		})
	}
	return w
}

// ApplySharedDOM operates on plain DOM only. The host must serialize the frame
// tree and validate both endpoint revisions and origins before every call.
// It never invokes JS, prepares scripts, dispatches events, or performs I/O.
func (s *Sandbox) ApplySharedDOM(r DOMRequest) (reply DOMReply, err error) {
	tree := s.tree
	n := tree.Node(r.Ref.Node)
	if r.Op == "document" {
		n = tree.Root()
	}
	if n == nil {
		return reply, domError("InvalidStateError", "stale DOM node")
	}
	ref := func(n *dom.Node) DOMRef {
		if n == nil {
			return DOMRef{}
		}
		kind := 1
		if n == tree.Root() {
			kind = 9
		} else if n.Kind() == dom.Text {
			kind = 3
		}
		return DOMRef{r.Ref.Frame, r.Ref.Revision, n.ID(), tree.Root().ID(), kind}
	}
	node := func(n *dom.Node) (DOMReply, error) { return DOMReply{Ref: ref(n)}, nil }
	if r.Op == "document" {
		return node(n)
	}
	if r.Op == "check" {
		return reply, nil
	}
	if r.Op == "retain" {
		if s.foreignPins == nil {
			s.foreignPins = map[dom.NodeID]int{}
		}
		s.foreignPins[n.ID()]++
		return reply, nil
	}
	if len(r.Text) > dom.MaxFragmentBytes || len(r.Key) > 1024 {
		return reply, domError("QuotaExceededError", "shared DOM input too large")
	}
	mutable := func() error {
		for a := n; a != nil; a = a.Parent() {
			if a.Tag() == "script" {
				return domError("NotSupportedError", "shared DOM cannot edit executable scripts")
			}
		}
		return nil
	}
	switch r.Op {
	case "connected":
		reply.Bool = tree.Attached(n)
	case "get":
		switch r.Key {
		case "nodeName":
			reply.Text = strings.ToUpper(n.Tag())
			if n == tree.Root() {
				reply.Text = "#document"
			} else if n.Kind() == dom.Text {
				reply.Text = "#text"
			}
		case "tagName":
			if n.Kind() == dom.Element && n != tree.Root() {
				reply.Text = strings.ToUpper(n.Tag())
			}
		case "textContent":
			reply.Text = dom.TextContent(n)
		case "innerHTML":
			reply.Text = tree.InnerHTML(n)
		case "id":
			reply.Text, _ = n.Attr("id")
		case "className":
			reply.Text, _ = n.Attr("class")
		case "value":
			reply.Text = n.Value()
		}
	case "node":
		switch r.Key {
		case "ownerDocument":
			if n != tree.Root() {
				return node(tree.Root())
			}
		case "body", "head", "documentElement":
			if n != tree.Root() {
				return reply, nil
			}
			tag := r.Key
			if tag == "documentElement" {
				tag = "html"
			}
			nodes, e := tree.Query(tree.Root(), tag, false)
			if e != nil {
				return reply, e
			}
			if len(nodes) > 0 {
				return node(nodes[0])
			}
		case "parentNode":
			return node(n.Parent())
		case "parentElement":
			if n.Parent() != tree.Root() {
				return node(n.Parent())
			}
		case "firstChild", "lastChild", "firstElementChild", "lastElementChild":
			var nodes []*dom.Node
			for _, c := range n.Children() {
				if !strings.Contains(r.Key, "Element") || c.Kind() == dom.Element {
					nodes = append(nodes, c)
				}
			}
			if len(nodes) > 0 {
				if strings.HasPrefix(r.Key, "last") {
					return node(nodes[len(nodes)-1])
				}
				return node(nodes[0])
			}
		}
	case "children":
		for _, c := range n.Children() {
			if r.Key == "children" && c.Kind() != dom.Element {
				continue
			}
			if len(reply.Refs) >= MaxForeignNodes {
				return DOMReply{}, domError("QuotaExceededError", "shared DOM result too large")
			}
			reply.Refs = append(reply.Refs, ref(c))
		}
	case "query", "queryAll":
		nodes, e := tree.Query(n, r.Text, r.Op == "queryAll")
		if e != nil {
			return reply, domError("SyntaxError", e.Error())
		}
		if r.Op == "query" {
			if len(nodes) > 0 {
				return node(nodes[0])
			}
		} else {
			if len(nodes) > MaxForeignNodes {
				return reply, domError("QuotaExceededError", "shared DOM result too large")
			}
			for _, c := range nodes {
				reply.Refs = append(reply.Refs, ref(c))
			}
		}
	case "getElementById":
		if n != tree.Root() {
			return reply, domError("InvalidStateError", "document required")
		}
		return node(tree.ByID(r.Text))
	case "getAttribute", "hasAttribute":
		reply.Text, reply.Bool = n.Attr(strings.ToLower(r.Key))
	case "setAttribute", "removeAttribute":
		if e := mutable(); e != nil {
			return reply, e
		}
		if r.Op == "setAttribute" {
			err = tree.SetAttr(n, r.Key, r.Text)
		} else {
			tree.RemoveAttr(n, r.Key)
		}
	case "set":
		if e := mutable(); e != nil {
			return reply, e
		}
		previous := n.Children()
		switch r.Key {
		case "textContent":
			if n == tree.Root() {
				break
			}
			err = tree.SetText(n, r.Text)
		case "innerHTML":
			if n == tree.Root() {
				return reply, domError("NotSupportedError", "document innerHTML is unsupported")
			}
			err = tree.SetInnerHTML(n, r.Text)
		case "id":
			err = tree.SetAttr(n, "id", r.Text)
		case "className":
			err = tree.SetAttr(n, "class", r.Text)
		case "value":
			before := n.Value()
			switch n.Tag() {
			case "input", "textarea":
				err = tree.SetValue(n, r.Text)
			case "select":
				var match *dom.Node
				for _, o := range dom.Options(n) {
					if dom.OptionValue(o) == r.Text {
						match = o
						break
					}
				}
				for _, o := range dom.Options(n) {
					tree.SetSelected(o, o == match)
				}
			case "option", "button":
				err = tree.SetAttr(n, "value", r.Text)
			default:
				return reply, domError("NotSupportedError", "value requires a form control")
			}
			if err == nil && n.Value() != before {
				s.valueChanged(n)
			}
		}
		if err == nil {
			for _, c := range previous {
				if !tree.Attached(c) {
					s.onDetach(c)
				}
			}
		}
	case "createElement", "createTextNode":
		if n != tree.Root() {
			return reply, domError("InvalidStateError", "document required")
		}
		var created *dom.Node
		if r.Op == "createElement" {
			if strings.EqualFold(r.Text, "script") {
				return reply, domError("NotSupportedError", "shared DOM cannot create executable scripts")
			}
			created, err = tree.NewElement(r.Text)
		} else {
			created, err = tree.NewText(r.Text)
		}
		if err == nil {
			return node(created)
		}
	case "appendChild", "insertBefore", "removeChild":
		if e := mutable(); e != nil {
			return reply, e
		}
		child := tree.Node(r.Other.Node)
		if child == nil || r.Other.Frame != r.Ref.Frame || r.Other.Revision != r.Ref.Revision {
			return reply, domError("WrongDocumentError", "foreign node")
		}
		executable := false
		dom.Walk(child, func(c *dom.Node) {
			if c.Tag() == "script" && !c.ParserScript() {
				executable = true
			}
		})
		if executable {
			return reply, domError("NotSupportedError", "shared DOM cannot move executable scripts")
		}
		if r.Op == "removeChild" {
			if child.Parent() != n {
				return reply, domError("NotFoundError", "node is not a child")
			}
			tree.Detach(child)
			s.onDetach(child)
		} else {
			var before *dom.Node
			if r.Before.Node != 0 {
				before = tree.Node(r.Before.Node)
				if before == nil || before.Parent() != n {
					return reply, domError("NotFoundError", "reference is not a child")
				}
			}
			err = tree.InsertBefore(n, child, before)
			if err == nil {
				if tree.Attached(child) {
					s.onAttach(child)
				} else {
					s.onDetach(child)
				}
			}
		}
	case "remove":
		if n == tree.Root() {
			return reply, domError("NotSupportedError", "cannot remove document")
		}
		tree.Detach(n)
		s.onDetach(n)
	default:
		return reply, domError("NotSupportedError", "unsupported shared DOM operation")
	}
	if err != nil {
		return reply, domError("InvalidStateError", err.Error())
	}
	if len(reply.Text) > 1<<20 {
		return DOMReply{}, domError("QuotaExceededError", "shared DOM result too large")
	}
	return reply, nil
}
func (s *Sandbox) ReleaseSharedDOM(id dom.NodeID) {
	if s.foreignPins[id] > 1 {
		s.foreignPins[id]--
	} else {
		delete(s.foreignPins, id)
	}
}
