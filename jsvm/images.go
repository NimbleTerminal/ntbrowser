// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/dop251/goja"
	"nimbleterminal.dev/ntbrowser/dom"
)

type imageDecode struct {
	revision        uint64
	resolve, reject func(any) error
}

type imageState struct {
	sequence                 uint64
	revision                 uint64
	src                      string
	present, pending, queued bool
	width, height            int
	current                  string
	// Pending requests retain detached images, including their event handlers.
	// Settled state contains no wrapper; normal weak DOM lifetime resumes.
	pin     *goja.Object
	decodes []imageDecode
}

func (s *Sandbox) imageNode(v goja.Value) *dom.Node {
	n := s.node(v)
	if n.Tag() != "img" {
		panic(s.vm.NewTypeError("not an HTMLImageElement"))
	}
	return n
}

func (s *Sandbox) imageState(n *dom.Node) *imageState {
	st := s.images[n.ID()]
	if st == nil {
		st = &imageState{}
		s.images[n.ID()] = st
	}
	return st
}

func (s *Sandbox) imageChanged(n *dom.Node) {
	if n.Tag() != "img" {
		return
	}
	st := s.imageState(n)
	st.src, st.present = n.Attr("src")
	st.revision++
	s.imageSequence++
	st.sequence = s.imageSequence
	if !st.present || st.src == "" {
		st.width, st.height, st.current = 0, 0, ""
	}
	st.pending, st.queued = st.present, true
	st.pin = s.wrap(n).(*goja.Object)
}

func (s *Sandbox) imageURL(src string) string {
	u, err := url.Parse(strings.TrimSpace(src))
	if err != nil {
		return src
	}
	if base, err := url.Parse(s.cfg.Location); err == nil && s.cfg.Location != "" {
		u = base.ResolveReference(u)
	}
	return u.String()
}

func (s *Sandbox) installImages() {
	s.images = map[dom.NodeID]*imageState{}
	s.imageHandlers = goja.NewSymbol("ntbrowser.imageHandlers")
	p := s.vm.NewObject()
	s.imageProto = p
	_ = p.SetPrototype(s.elemProto)
	illegal := s.vm.ToValue(func(goja.ConstructorCall) *goja.Object { panic(s.vm.NewTypeError("Illegal constructor")) }).(*goja.Object)
	_ = illegal.Set("prototype", p)
	defineFrozen(p, "constructor", illegal)
	defineFrozen(s.vm.GlobalObject(), "HTMLImageElement", illegal)
	ctor := s.vm.ToValue(func(c goja.ConstructorCall) *goja.Object {
		var n *dom.Node
		s.underPressure(func() (err error) { n, err = s.tree.NewElement("img"); return err })
		for i, name := range []string{"width", "height"} {
			if i < len(c.Arguments) {
				s.must(s.tree.SetAttr(n, name, strconv.FormatUint(uint64(imageUint32(c.Arguments[i])), 10)))
			}
		}
		return s.wrap(n).(*goja.Object)
	}).(*goja.Object)
	_ = ctor.Set("prototype", p)
	defineFrozen(s.vm.GlobalObject(), "Image", ctor)
	for _, name := range []string{"src", "alt"} {
		s.accessor(p, name, func(t goja.Value) goja.Value {
			v, present := s.imageNode(t).Attr(name)
			if name == "src" && present {
				v = s.imageURL(v)
			}
			return s.vm.ToValue(v)
		}, func(t, v goja.Value) {
			n := s.imageNode(t)
			s.must(s.tree.SetAttr(n, name, v.String()))
			if name == "src" {
				s.imageChanged(n)
			}
		})
	}
	for _, name := range []string{"width", "height"} {
		s.accessor(p, name, func(t goja.Value) goja.Value {
			n := s.imageNode(t)
			if v, ok := n.Attr(name); ok {
				if number, err := strconv.ParseUint(strings.TrimSpace(v), 10, 32); err == nil {
					return s.vm.ToValue(number)
				}
			}
			st := s.imageState(n)
			if name == "width" {
				return s.vm.ToValue(st.width)
			}
			return s.vm.ToValue(st.height)
		}, func(t, v goja.Value) {
			s.must(s.tree.SetAttr(s.imageNode(t), name, strconv.FormatUint(uint64(imageUint32(v)), 10)))
		})
	}
	s.accessor(p, "complete", func(t goja.Value) goja.Value {
		st := s.imageState(s.imageNode(t))
		return s.vm.ToValue(!st.pending || st.src == "")
	}, nil)
	s.accessor(p, "naturalWidth", func(t goja.Value) goja.Value { return s.vm.ToValue(s.imageState(s.imageNode(t)).width) }, nil)
	s.accessor(p, "naturalHeight", func(t goja.Value) goja.Value { return s.vm.ToValue(s.imageState(s.imageNode(t)).height) }, nil)
	s.accessor(p, "currentSrc", func(t goja.Value) goja.Value { return s.vm.ToValue(s.imageState(s.imageNode(t)).current) }, nil)
	for _, typ := range []string{"load", "error"} {
		s.accessor(p, "on"+typ, func(t goja.Value) goja.Value {
			s.imageNode(t)
			bag, _ := t.(*goja.Object).GetSymbol(s.imageHandlers).(*goja.Object)
			if bag != nil {
				if v := bag.Get(typ); v != nil {
					return v
				}
			}
			return goja.Null()
		}, func(t, v goja.Value) {
			n := s.imageNode(t)
			w := t.(*goja.Object)
			bag, _ := w.GetSymbol(s.imageHandlers).(*goja.Object)
			if bag == nil {
				bag = s.vm.NewObject()
				_ = w.SetSymbol(s.imageHandlers, bag)
			}
			if old := bag.Get(typ + "Listener"); old != nil && !goja.IsNull(old) {
				if _, ok := goja.AssertFunction(v); ok {
					_ = bag.Set(typ, v)
					return
				}
				s.removeListener(w, n, typ, old)
			}
			_ = bag.Set(typ, goja.Null())
			_ = bag.Set(typ+"Listener", goja.Null())
			if _, ok := goja.AssertFunction(v); ok {
				listener := s.vm.ToValue(func(c goja.FunctionCall) goja.Value {
					fn, ok := goja.AssertFunction(bag.Get(typ))
					if !ok {
						return goja.Undefined()
					}
					result, err := fn(c.This, c.Arguments...)
					if err != nil {
						panic(err)
					}
					return result
				})
				s.addListener(w, n, typ, listener)
				_ = bag.Set(typ, v)
				_ = bag.Set(typ+"Listener", listener)
			}
		})
	}
	s.method(p, "decode", func(c goja.FunctionCall) goja.Value {
		n := s.imageNode(c.This)
		if s.imageDecodes >= s.cfg.MaxListeners {
			panic(s.vm.NewTypeError("too many pending image decodes"))
		}
		st := s.imageState(n)
		promise, resolve, reject := s.vm.NewPromise()
		st.decodes = append(st.decodes, imageDecode{st.revision, resolve, reject})
		st.queued = true
		st.pin = c.This.(*goja.Object)
		s.imageDecodes++
		return s.vm.ToValue(promise)
	})
}

// UpdateImages is an event-loop checkpoint, outside any JS entry. load joins
// the host's bounded image cache and returns decoded dimensions or (0,0,true)
// on failure. No JS or Goja value ever reaches a worker. The return value asks
// for another task when completion handlers queued more image work.
func (s *Sandbox) UpdateImages(load func(string) (width, height int, done bool)) bool {
	if s.disabled {
		return false
	}
	type item struct {
		id       dom.NodeID
		revision uint64
	}
	items := make([]item, 0, len(s.images))
	for id, st := range s.images {
		items = append(items, item{id, st.revision})
	}
	sort.Slice(items, func(i, j int) bool { return s.images[items[i].id].sequence < s.images[items[j].id].sequence })
	for _, it := range items {
		st := s.images[it.id]
		if st == nil || st.revision != it.revision {
			continue
		}
		st.queued = false
		event := ""
		if st.pending {
			w, h, done := 0, 0, true
			if strings.TrimSpace(st.src) != "" && load != nil {
				w, h, done = load(st.src)
			}
			if done {
				st.pending = false
				st.width, st.height = w, h
				if st.src != "" {
					st.current = s.imageURL(st.src)
				}
				event = "error"
				if w > 0 && h > 0 {
					event = "load"
				}
			}
		}
		// Settle only this checkpoint's promises. A promise reaction can change
		// src or call decode again; those belong to the next task.
		decodes := st.decodes
		st.decodes = nil
		for _, d := range decodes {
			if d.revision == st.revision && st.pending {
				st.decodes = append(st.decodes, d)
				continue
			}
			s.imageDecodes--
			err := s.Run(func(vm *goja.Runtime) error {
				if d.revision == st.revision && st.width > 0 && st.height > 0 {
					return d.resolve(goja.Undefined())
				}
				e := vm.NewTypeError("Image could not be decoded")
				_ = e.Set("name", "EncodingError")
				return d.reject(e)
			})
			if err != nil {
				s.host.Log("error", err.Error())
			}
			if s.disabled {
				return false
			}
		}
		if event != "" && st.revision == it.revision {
			_, err := s.Dispatch(Event{Type: event, Target: it.id, NoBubble: true, AllowDetached: true, NonCancelable: true})
			if err != nil {
				s.host.Log("error", err.Error())
			}
		}
		if s.disabled {
			return false
		}
		if !st.pending && !st.queued && len(st.decodes) == 0 {
			st.pin = nil
		}
	}
	for _, st := range s.images {
		if st.queued {
			return true
		}
	}
	return false
}

func imageUint32(v goja.Value) uint32 {
	f := v.ToFloat()
	if math.IsNaN(f) || math.IsInf(f, 0) || f == 0 {
		return 0
	}
	f = math.Mod(math.Trunc(f), 4294967296)
	if f < 0 {
		f += 4294967296
	}
	return uint32(f)
}
