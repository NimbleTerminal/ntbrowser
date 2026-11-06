// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"errors"
	"runtime"
	"strconv"
	"strings"
	"time"
	"weak"

	"github.com/dop251/goja"

	"nimbleterminal.dev/ntbrowser/dom"
)

// Element wrappers are ordinary JS objects whose prototype carries the
// methods. The only state on a wrapper is a symbol-keyed integer NodeID and
// a symbol-keyed bag of event listeners. Every method resolves the ID through
// the Tree on each call, so no Go pointer is ever reachable from JavaScript
// (ToValue is never called on a *dom.Node or any struct).
//
// Lifetime (browser semantics, arbitrary pages): a removed node stays alive
// while JavaScript references it and can be re-inserted. The bridge holds
// wrappers in two maps:
//
//   - weak:   every wrapper, as a weak.Pointer. JS unreachability is Go
//             unreachability in Goja, so the GC collects dropped wrappers
//             and runtime.AddCleanup reports the NodeID to the sandbox.
//   - strong: wrappers of *attached* nodes that carry listeners. A listener
//             on an attached element must keep firing even when the page
//             forgot the element, so those cannot be weak.
//
// Detaching a subtree demotes its wrappers to weak only; re-attaching
// promotes listener-bearing ones back. When a cleanup reports a node whose
// detached root has no live wrapper left, the subtree is released from the
// node budget.

func (s *Sandbox) wrap(n *dom.Node) goja.Value {
	if n == nil {
		return goja.Null()
	}
	if w := s.liveWrapper(n.ID()); w != nil {
		return w
	}
	w := s.vm.NewObject()
	_ = w.SetPrototype(s.elemProto)
	if n.Tag() == "a" {
		_ = w.SetPrototype(s.anchorProto)
	}
	if n.Tag() == "img" {
		_ = w.SetPrototype(s.imageProto)
	}
	if n.Tag() == "script" {
		_ = w.SetPrototype(s.scriptProto)
	}
	_ = w.DefineDataPropertySymbol(s.nodeSym, s.vm.ToValue(int(n.ID())), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	s.weakW[n.ID()] = weak.Make(w)
	// The cleanup closure must not reference w (it would never be collected).
	// It references the sandbox, which holds w only weakly.
	runtime.AddCleanup(w, s.collectedCleanup, n.ID())
	return w
}

// collectedCleanup runs on the GC's cleanup goroutine; it only enqueues.
func (s *Sandbox) collectedCleanup(id dom.NodeID) {
	s.collectedMu.Lock()
	s.collected = append(s.collected, id)
	s.collectedMu.Unlock()
}

func (s *Sandbox) liveWrapper(id dom.NodeID) *goja.Object {
	if w, ok := s.strong[id]; ok {
		return w
	}
	if wp, ok := s.weakW[id]; ok {
		if w := wp.Value(); w != nil {
			return w
		}
		delete(s.weakW, id)
	}
	return nil
}

// reap drains cleanup reports (on the VM goroutine) and releases detached
// subtrees that no wrapper keeps alive. Called at the start of every Run and
// after a budget-triggered GC.
func (s *Sandbox) reap() {
	s.collectedMu.Lock()
	ids := s.collected
	s.collected = nil
	s.collectedMu.Unlock()
	for _, id := range ids {
		if wp, ok := s.weakW[id]; ok && wp.Value() == nil {
			delete(s.weakW, id)
		}
		s.forgetCounts(id)
	}
	for _, root := range s.tree.DetachedRoots() {
		if !s.anyLiveWrapper(root) {
			dom.Walk(root, func(n *dom.Node) { s.forgetCounts(n.ID()); delete(s.weakW, n.ID()); delete(s.images, n.ID()) })
			s.tree.Release(root)
		}
	}
	for id := range s.scripts {
		if s.tree.Node(id) == nil {
			delete(s.scripts, id)
		}
	}
	if s.active != 0 && s.tree.Node(s.active) == nil {
		s.active = 0
	}
}

func (s *Sandbox) anyLiveWrapper(root *dom.Node) bool {
	alive := false
	dom.Walk(root, func(n *dom.Node) {
		if st := s.images[n.ID()]; st != nil && st.pin != nil {
			alive = true
		}
		if !alive && s.liveWrapper(n.ID()) != nil {
			alive = true
		}
	})
	return alive
}

// onDetach/onAttach keep the strong map consistent with attachment.
func (s *Sandbox) onDetach(root *dom.Node) {
	dom.Walk(root, func(n *dom.Node) { delete(s.strong, n.ID()) })
	if s.active != 0 && s.tree.Node(s.active) != nil && s.tree.RootOf(s.tree.Node(s.active)) == root {
		s.active = 0 // focus leaves with the subtree, as in browsers
	}
}

func (s *Sandbox) onAttach(root *dom.Node) {
	dom.Walk(root, func(n *dom.Node) {
		if len(s.listenCount[n.ID()]) > 0 {
			if w := s.liveWrapper(n.ID()); w != nil {
				s.strong[n.ID()] = w
			}
		}
	})
}

// node resolves `this` to a live node or throws a TypeError into JS.
func (s *Sandbox) node(this goja.Value) *dom.Node {
	obj, ok := this.(*goja.Object)
	if !ok {
		panic(s.vm.NewTypeError("Illegal invocation"))
	}
	idv := obj.GetSymbol(s.nodeSym)
	if idv == nil {
		panic(s.vm.NewTypeError("Illegal invocation"))
	}
	n := s.tree.Node(dom.NodeID(idv.ToInteger()))
	if n == nil {
		panic(s.vm.NewTypeError("node has been released"))
	}
	return n
}

func (s *Sandbox) nodeArg(v goja.Value) *dom.Node {
	if goja.IsUndefined(v) || goja.IsNull(v) {
		panic(s.vm.NewTypeError("parameter is not a Node"))
	}
	return s.node(v)
}

func (s *Sandbox) throw(err error) {
	panic(jsError(s.vm, err)) // catchable by the page; a budget failure reads like a RangeError
}

// collectNow is the budget-pressure path: force a GC, give the cleanup
// goroutine a bounded window to report collected wrappers, and reap. It runs
// only when a budget is exceeded, so steady-state pages pay nothing, and the
// window (10 ms) fits inside the default 50 ms slice.
func (s *Sandbox) collectNow() {
	if s.running {
		defer s.wd.pause()() // Go-side GC time is not billed to the script
	}
	runtime.GC()
	deadline := time.Now().Add(10 * time.Millisecond)
	for time.Now().Before(deadline) {
		s.reap()
		time.Sleep(500 * time.Microsecond)
	}
	s.reap()
}

// underPressure retries a budget-limited tree operation once after a forced
// collection, so a page that dropped its references is not punished for the
// GC not having run yet.
func (s *Sandbox) underPressure(op func() error) {
	err := op()
	if errors.Is(err, dom.ErrLimit) {
		s.collectNow()
		err = op()
	}
	s.must(err)
}

func (s *Sandbox) must(err error) {
	if err != nil {
		s.throw(err)
	}
}

func (s *Sandbox) method(obj *goja.Object, name string, fn func(call goja.FunctionCall) goja.Value) {
	defineFrozen(obj, name, s.vm.ToValue(fn))
}

func (s *Sandbox) accessor(obj *goja.Object, name string, get func(goja.Value) goja.Value, set func(goja.Value, goja.Value)) {
	var setter goja.Value
	if set != nil {
		setter = s.vm.ToValue(func(call goja.FunctionCall) goja.Value { set(call.This, call.Argument(0)); return goja.Undefined() })
	}
	getter := s.vm.ToValue(func(call goja.FunctionCall) goja.Value { return get(call.This) })
	_ = obj.DefineAccessorProperty(name, getter, setter, goja.FLAG_FALSE, goja.FLAG_TRUE)
}

func (s *Sandbox) buildElementProto() *goja.Object {
	vm := s.vm
	p := vm.NewObject()
	s.accessor(p, "tagName", func(t goja.Value) goja.Value { return vm.ToValue(strings.ToUpper(s.node(t).Tag())) }, nil)
	s.accessor(p, "nodeType", func(t goja.Value) goja.Value {
		if s.node(t).Kind() == dom.Text {
			return vm.ToValue(3)
		}
		return vm.ToValue(1)
	}, nil)
	s.accessor(p, "isConnected", func(t goja.Value) goja.Value { return vm.ToValue(s.tree.Attached(s.node(t))) }, nil)
	s.accessor(p, "id", func(t goja.Value) goja.Value { v, _ := s.node(t).Attr("id"); return vm.ToValue(v) },
		func(t, v goja.Value) { s.must(s.tree.SetAttr(s.node(t), "id", v.String())) })
	s.accessor(p, "className", func(t goja.Value) goja.Value { v, _ := s.node(t).Attr("class"); return vm.ToValue(v) },
		func(t, v goja.Value) { s.must(s.tree.SetAttr(s.node(t), "class", v.String())) })
	s.accessor(p, "textContent", func(t goja.Value) goja.Value { return vm.ToValue(dom.TextContent(s.node(t))) }, s.setText)
	s.accessor(p, "innerText", func(t goja.Value) goja.Value { return vm.ToValue(dom.TextContent(s.node(t))) }, s.setText)
	// innerHTML is deliberately absent: the setter would require parsing
	// untrusted markup inside the VM slice. Pages build nodes instead.
	s.accessor(p, "parentElement", func(t goja.Value) goja.Value {
		p := s.node(t).Parent()
		if p == nil || p == s.tree.Root() {
			return goja.Null()
		}
		return s.wrap(p)
	}, nil)
	s.accessor(p, "parentNode", func(t goja.Value) goja.Value { return s.wrap(s.node(t).Parent()) }, nil)
	s.accessor(p, "firstChild", func(t goja.Value) goja.Value {
		kids := s.node(t).Children()
		if len(kids) == 0 {
			return goja.Null()
		}
		return s.wrap(kids[0])
	}, nil)
	s.accessor(p, "childNodes", func(t goja.Value) goja.Value { return s.wrapAll(s.node(t).Children(), false) }, nil)
	s.accessor(p, "children", func(t goja.Value) goja.Value { return s.wrapAll(s.node(t).Children(), true) }, nil)
	s.accessor(p, "classList", func(t goja.Value) goja.Value { return s.classList(s.node(t)) }, nil)

	s.method(p, "getAttribute", func(c goja.FunctionCall) goja.Value {
		v, ok := s.node(c.This).Attr(c.Argument(0).String())
		if !ok {
			return goja.Null()
		}
		return vm.ToValue(v)
	})
	s.method(p, "hasAttribute", func(c goja.FunctionCall) goja.Value {
		_, ok := s.node(c.This).Attr(c.Argument(0).String())
		return vm.ToValue(ok)
	})
	s.method(p, "setAttribute", func(c goja.FunctionCall) goja.Value {
		n := s.node(c.This)
		name := c.Argument(0).String()
		s.must(s.tree.SetAttr(n, name, c.Argument(1).String()))
		if strings.EqualFold(name, "src") {
			s.imageChanged(n)
		}
		if n.Tag() == "script" && strings.EqualFold(name, "async") {
			s.scriptState(n).ordered = true
		}
		if strings.EqualFold(name, "src") {
			s.prepareScript(n)
		}
		return goja.Undefined()
	})
	s.method(p, "removeAttribute", func(c goja.FunctionCall) goja.Value {
		n := s.node(c.This)
		name := c.Argument(0).String()
		s.tree.RemoveAttr(n, name)
		if strings.EqualFold(name, "src") {
			s.imageChanged(n)
		}
		return goja.Undefined()
	})
	s.method(p, "appendChild", func(c goja.FunctionCall) goja.Value {
		child := s.nodeArg(c.Argument(0))
		s.insert(s.node(c.This), child, nil)
		return s.wrap(child)
	})
	s.method(p, "insertBefore", func(c goja.FunctionCall) goja.Value {
		child := s.nodeArg(c.Argument(0))
		var ref *dom.Node
		if r := c.Argument(1); !goja.IsNull(r) && !goja.IsUndefined(r) {
			ref = s.node(r)
		}
		s.insert(s.node(c.This), child, ref)
		return s.wrap(child)
	})
	s.method(p, "removeChild", func(c goja.FunctionCall) goja.Value {
		child := s.nodeArg(c.Argument(0))
		if child.Parent() != s.node(c.This) {
			panic(vm.NewTypeError("node is not a child of this element"))
		}
		s.detach(child)
		return s.wrap(child)
	})
	s.method(p, "remove", func(c goja.FunctionCall) goja.Value { s.detach(s.node(c.This)); return goja.Undefined() })
	s.method(p, "querySelector", func(c goja.FunctionCall) goja.Value { return s.query(s.node(c.This), c.Argument(0).String(), false) })
	s.method(p, "querySelectorAll", func(c goja.FunctionCall) goja.Value { return s.query(s.node(c.This), c.Argument(0).String(), true) })
	s.method(p, "addEventListener", func(c goja.FunctionCall) goja.Value {
		s.addListener(c.This.(*goja.Object), s.node(c.This), c.Argument(0).String(), c.Argument(1))
		return goja.Undefined()
	})
	s.method(p, "removeEventListener", func(c goja.FunctionCall) goja.Value {
		s.removeListener(c.This.(*goja.Object), s.node(c.This), c.Argument(0).String(), c.Argument(1))
		return goja.Undefined()
	})
	s.method(p, "focus", func(c goja.FunctionCall) goja.Value {
		if n := s.node(c.This); dom.Focusable(n) && s.tree.Attached(n) {
			_ = s.SetFocus(n.ID()) // listener errors are logged inside
		}
		return goja.Undefined()
	})
	s.method(p, "blur", func(c goja.FunctionCall) goja.Value {
		if s.active == s.node(c.This).ID() {
			_ = s.SetFocus(0)
		}
		return goja.Undefined()
	})
	s.installFormBindings(p)
	return p
}

// insert/detach wrap Tree mutations with the wrapper bookkeeping.
func (s *Sandbox) insert(parent, child, ref *dom.Node) {
	wasAttached := s.tree.Attached(child)
	s.must(s.tree.InsertBefore(parent, child, ref))
	if !wasAttached && s.tree.Attached(child) {
		s.onAttach(child)
	} else if wasAttached && !s.tree.Attached(child) {
		s.onDetach(child)
	}
	dom.Walk(child, s.prepareScript)
	s.prepareScript(parent)
}

func (s *Sandbox) detach(n *dom.Node) {
	if !s.tree.Attached(n) || n.Parent() == nil {
		return
	}
	s.onDetach(n)
	s.tree.Detach(n)
	if !s.anyLiveWrapper(n) {
		s.tree.Release(n) // nothing can ever reach it again
	}
}

// setText implements the textContent/innerText setter. Replaced children
// become detached roots; those without live wrappers are released at once.
func (s *Sandbox) setText(t, v goja.Value) {
	n := s.node(t)
	prev := append([]*dom.Node(nil), n.Children()...)
	// SetText is transactional in dom: on failure the children stay attached,
	// so their strong pins must only be dropped after it succeeds.
	s.underPressure(func() error { return s.tree.SetText(n, v.String()) })
	for _, c := range prev {
		if !s.tree.Attached(c) {
			s.onDetach(c)
		}
	}
	for _, root := range s.tree.DetachedRoots() {
		if !s.anyLiveWrapper(root) {
			dom.Walk(root, func(n *dom.Node) { s.forgetCounts(n.ID()) })
			s.tree.Release(root)
		}
	}
	s.prepareScript(n)
	s.prepareScript(n.Parent())
}

func (s *Sandbox) wrapAll(nodes []*dom.Node, elementsOnly bool) goja.Value {
	out := make([]any, 0, len(nodes))
	for _, n := range nodes {
		if elementsOnly && n.Kind() != dom.Element {
			continue
		}
		out = append(out, s.wrap(n))
	}
	return s.vm.NewArray(out...)
}

func (s *Sandbox) query(from *dom.Node, sel string, all bool) goja.Value {
	nodes, err := s.tree.Query(from, sel, all)
	if err != nil {
		panic(jsError(s.vm, err)) // SyntaxError in browsers; TypeError here
	}
	if !all {
		if len(nodes) == 0 {
			return goja.Null()
		}
		return s.wrap(nodes[0])
	}
	return s.wrapAll(nodes, false)
}

func (s *Sandbox) classList(n *dom.Node) goja.Value {
	vm := s.vm
	o := vm.NewObject()
	classes := func() []string { v, _ := n.Attr("class"); return strings.Fields(v) }
	write := func(cs []string) { s.must(s.tree.SetAttr(n, "class", strings.Join(cs, " "))) }
	s.method(o, "contains", func(c goja.FunctionCall) goja.Value { return vm.ToValue(contains(classes(), c.Argument(0).String())) })
	s.method(o, "add", func(c goja.FunctionCall) goja.Value {
		cs := classes()
		for _, a := range c.Arguments {
			if k := a.String(); !contains(cs, k) && k != "" {
				cs = append(cs, k)
			}
		}
		write(cs)
		return goja.Undefined()
	})
	s.method(o, "remove", func(c goja.FunctionCall) goja.Value {
		cs := classes()
		for _, a := range c.Arguments {
			cs = without(cs, a.String())
		}
		write(cs)
		return goja.Undefined()
	})
	s.method(o, "toggle", func(c goja.FunctionCall) goja.Value {
		k := c.Argument(0).String()
		cs := classes()
		if contains(cs, k) {
			write(without(cs, k))
			return vm.ToValue(false)
		}
		write(append(cs, k))
		return vm.ToValue(true)
	})
	return o
}

func contains(cs []string, k string) bool {
	for _, c := range cs {
		if c == k {
			return true
		}
	}
	return false
}

func without(cs []string, k string) []string {
	out := cs[:0]
	for _, c := range cs {
		if c != k {
			out = append(out, c)
		}
	}
	return out
}

// ---- document ----

func (s *Sandbox) documentObject() *goja.Object {
	vm := s.vm
	d := vm.NewObject()
	s.docObj = d
	// The document object participates in event dispatch like a wrapper for
	// the root node, so it gets the same node symbol.
	_ = d.DefineDataPropertySymbol(s.nodeSym, vm.ToValue(int(s.tree.Root().ID())), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	s.strong[s.tree.Root().ID()] = d
	s.method(d, "getElementById", func(c goja.FunctionCall) goja.Value { return s.wrap(s.tree.ByID(c.Argument(0).String())) })
	s.method(d, "querySelector", func(c goja.FunctionCall) goja.Value { return s.query(s.tree.Root(), c.Argument(0).String(), false) })
	s.method(d, "querySelectorAll", func(c goja.FunctionCall) goja.Value { return s.query(s.tree.Root(), c.Argument(0).String(), true) })
	s.method(d, "createElement", func(c goja.FunctionCall) goja.Value {
		var n *dom.Node
		s.underPressure(func() (err error) { n, err = s.tree.NewElement(c.Argument(0).String()); return err })
		return s.wrap(n)
	})
	s.method(d, "createTextNode", func(c goja.FunctionCall) goja.Value {
		var n *dom.Node
		s.underPressure(func() (err error) { n, err = s.tree.NewText(c.Argument(0).String()); return err })
		return s.wrap(n)
	})
	s.method(d, "addEventListener", func(c goja.FunctionCall) goja.Value {
		s.addListener(d, s.tree.Root(), c.Argument(0).String(), c.Argument(1))
		return goja.Undefined()
	})
	s.method(d, "removeEventListener", func(c goja.FunctionCall) goja.Value {
		s.removeListener(d, s.tree.Root(), c.Argument(0).String(), c.Argument(1))
		return goja.Undefined()
	})
	s.accessor(d, "currentScript", func(goja.Value) goja.Value { return s.wrap(s.tree.Node(s.currentScript)) }, nil)
	s.accessor(d, "head", func(goja.Value) goja.Value { return s.query(s.tree.Root(), "head", false) }, nil)
	s.accessor(d, "body", func(goja.Value) goja.Value { return s.wrap(s.tree.Body()) }, nil)
	s.accessor(d, "documentElement", func(goja.Value) goja.Value {
		for _, c := range s.tree.Root().Children() {
			if c.Tag() == "html" {
				return s.wrap(c)
			}
		}
		return goja.Null()
	}, nil)
	s.accessor(d, "activeElement", func(goja.Value) goja.Value {
		if n := s.tree.Node(s.active); n != nil && s.tree.Attached(n) {
			return s.wrap(n)
		}
		return s.wrap(s.tree.Body())
	}, nil)
	s.accessor(d, "cookie", func(goja.Value) goja.Value {
		v := ""
		if s.cfg.CookieRead != nil {
			v = s.cfg.CookieRead()
		}
		return vm.ToValue(v)
	}, func(_ goja.Value, v goja.Value) {
		if s.cfg.CookieWrite != nil {
			s.cfg.CookieWrite(v.String())
		}
	})
	s.accessor(d, "title", func(goja.Value) goja.Value { return vm.ToValue(s.tree.Title()) }, nil)
	return d
}

// ---- events ----
//
// Listeners live on the wrapper under a hidden symbol: { type: [fn, ...] }.
// Only integer counts live in Go (listenCount), so a dropped wrapper takes
// its callbacks with it and the counts are reconciled by reap.

func (s *Sandbox) listenerList(w *goja.Object, typ string, create bool) *goja.Object {
	bagV := w.GetSymbol(s.listSym)
	bag, _ := bagV.(*goja.Object)
	if bag == nil {
		if !create {
			return nil
		}
		bag = s.vm.NewObject()
		_ = w.DefineDataPropertySymbol(s.listSym, bag, goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	}
	arr, _ := bag.Get(typ).(*goja.Object)
	if arr == nil {
		if !create {
			return nil
		}
		arr = s.vm.NewArray()
		_ = bag.Set(typ, arr)
	}
	return arr
}

func arrayValues(arr *goja.Object) []goja.Value {
	if arr == nil {
		return nil
	}
	n := int(arr.Get("length").ToInteger())
	out := make([]goja.Value, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, arr.Get(strconv.Itoa(i)))
	}
	return out
}

func (s *Sandbox) addListener(w *goja.Object, n *dom.Node, typ string, fn goja.Value) {
	if _, ok := goja.AssertFunction(fn); !ok {
		return // browsers ignore non-callable listeners
	}
	if s.nListen >= s.cfg.MaxListeners {
		s.collectNow()
		if s.nListen >= s.cfg.MaxListeners {
			panic(jsError(s.vm, errors.New("too many event listeners")))
		}
	}
	arr := s.listenerList(w, typ, true)
	vals := arrayValues(arr)
	for _, existing := range vals {
		if existing.StrictEquals(fn) {
			return
		}
	}
	_ = arr.Set(strconv.Itoa(len(vals)), fn)
	s.nListen++
	if s.listenCount[n.ID()] == nil {
		s.listenCount[n.ID()] = map[string]int{}
	}
	s.listenCount[n.ID()][typ]++
	s.typeCount[typ]++
	s.tree.AddListener(n, typ)
	if s.tree.Attached(n) {
		s.strong[n.ID()] = w
	}
}

func (s *Sandbox) removeListener(w *goja.Object, n *dom.Node, typ string, fn goja.Value) {
	arr := s.listenerList(w, typ, false)
	if arr == nil {
		return
	}
	vals := arrayValues(arr)
	kept := s.vm.NewArray()
	removed := false
	for _, v := range vals {
		if !removed && v.StrictEquals(fn) {
			removed = true
			continue
		}
		_ = kept.Set(strconv.Itoa(int(kept.Get("length").ToInteger())), v)
	}
	if !removed {
		return
	}
	_ = w.GetSymbol(s.listSym).(*goja.Object).Set(typ, kept)
	s.nListen--
	s.typeCount[typ]--
	if m := s.listenCount[n.ID()]; m != nil {
		m[typ]--
		if m[typ] <= 0 {
			delete(m, typ)
			s.tree.RemoveListener(n, typ)
		}
		if len(m) == 0 {
			delete(s.listenCount, n.ID())
			delete(s.strong, n.ID())
		}
	}
}

// forgetCounts reconciles the Go-side counts for a node whose wrapper (and
// with it, its callbacks) is gone or whose subtree is being released.
func (s *Sandbox) forgetCounts(id dom.NodeID) {
	m := s.listenCount[id]
	for typ, c := range m {
		s.nListen -= c
		s.typeCount[typ] -= c
	}
	delete(s.listenCount, id)
	delete(s.strong, id)
}

// Event is what the loop delivers; it is Go data until Dispatch builds the
// JS event object.
type Event struct {
	Type   string
	Target dom.NodeID // 0 targets the focused element (keyboard) or the document
	Key    string     // keydown: DOM KeyboardEvent.key where known
	Href   string     // click: the resolved href of the target or its nearest <a>, if any
	// NoBubble limits dispatch to the target (focus, blur); the zero value
	// bubbles to the document, as every other event here does.
	NoBubble bool
	// AllowDetached is reserved for host-delivered image load/error events.
	AllowDetached bool
	NonCancelable bool
}

// Dispatch runs listeners from the target up to the document (bubbling
// only). It returns whether the page called preventDefault. All listener
// calls happen inside one watchdog slice. Called from inside running script
// (element.focus()), it runs the listeners in the current slice; their
// exceptions are logged rather than thrown into the caller, as in browsers.
func (s *Sandbox) Dispatch(ev Event) (prevented bool, err error) {
	if s.disabled {
		return false, ErrDisabled // nothing can prevent anything any more
	}
	target := s.tree.Root()
	switch {
	case ev.Target != 0:
		if target = s.tree.Node(ev.Target); target == nil || (!s.tree.Attached(target) && !ev.AllowDetached) {
			return false, nil
		}
	case ev.Type == "keydown" || ev.Type == "keyup":
		if n := s.tree.Node(s.active); n != nil && s.tree.Attached(n) {
			target = n
		}
	}
	// Collect the path first; listeners may mutate the tree while running.
	type hop struct {
		this *goja.Object
		fns  []goja.Value
	}
	var path []hop
	for n := target; n != nil; n = n.Parent() {
		if w := s.liveWrapper(n.ID()); w != nil {
			if fns := arrayValues(s.listenerList(w, ev.Type, false)); len(fns) > 0 {
				path = append(path, hop{w, fns})
			}
		}
		if ev.NoBubble {
			break
		}
	}
	if len(path) == 0 {
		return false, nil
	}
	run := func(vm *goja.Runtime) error {
		stopped := false
		e := vm.NewObject()
		defineFrozen(e, "type", vm.ToValue(ev.Type))
		defineFrozen(e, "bubbles", vm.ToValue(!ev.NoBubble))
		defineFrozen(e, "cancelable", vm.ToValue(!ev.NonCancelable))
		defineFrozen(e, "target", s.wrap(target))
		defineFrozen(e, "key", vm.ToValue(ev.Key))
		defineFrozen(e, "href", vm.ToValue(ev.Href))
		_ = e.Set("defaultPrevented", false)
		defineFrozen(e, "preventDefault", vm.ToValue(func(goja.FunctionCall) goja.Value {
			if !ev.NonCancelable {
				prevented = true
				_ = e.Set("defaultPrevented", true)
			}
			return goja.Undefined()
		}))
		defineFrozen(e, "stopPropagation", vm.ToValue(func(goja.FunctionCall) goja.Value { stopped = true; return goja.Undefined() }))
		for _, h := range path {
			if stopped {
				break
			}
			_ = e.Set("currentTarget", h.this)
			for _, f := range h.fns {
				call, _ := goja.AssertFunction(f)
				if _, err := call(h.this, e); err != nil {
					return err // first throwing listener aborts; browsers report and continue
				}
			}
		}
		return nil
	}
	if s.running {
		if err := run(s.vm); err != nil {
			s.host.Log("error", "uncaught in "+ev.Type+" listener: "+safeString(s.vm.ToValue(err.Error())))
		}
		return prevented, nil
	}
	err = s.Run(run)
	return prevented, err
}

// HasListeners lets the loop skip the VM entirely for events nobody wants.
func (s *Sandbox) HasListeners(typ string) bool { return s.typeCount[typ] > 0 }

// Active reports the focused element (0 = none).
func (s *Sandbox) Active() dom.NodeID { return s.active }

// Focus sets the focused element from the host (e.g. after a click hit a
// focusable element). Non-focusable targets clear focus to the body.
func (s *Sandbox) Focus(id dom.NodeID) { _ = s.SetFocus(id) }

// SetFocus moves focus to id (0 or a non-focusable or detached element
// clears it) and dispatches blur/focusout on the old element and
// focus/focusin on the new, each only when the page listens for it, so a
// listener-free page never enters the VM.
func (s *Sandbox) SetFocus(id dom.NodeID) error {
	if n := s.tree.Node(id); n == nil || !s.tree.Attached(n) || !focusable(n) {
		id = 0
	}
	old := s.active
	if id == old {
		return nil
	}
	s.active = id
	var errs []error
	fire := func(target dom.NodeID, typ string, noBubble bool) {
		if target == 0 || !s.HasListeners(typ) {
			return
		}
		if n := s.tree.Node(target); n == nil || !s.tree.Attached(n) {
			return
		}
		if _, err := s.Dispatch(Event{Type: typ, Target: target, NoBubble: noBubble}); err != nil {
			errs = append(errs, err)
		}
	}
	fire(old, "blur", true)
	fire(old, "focusout", false)
	fire(id, "focus", true)
	fire(id, "focusin", false)
	return errors.Join(errs...)
}

func focusable(n *dom.Node) bool { return dom.Focusable(n) }
