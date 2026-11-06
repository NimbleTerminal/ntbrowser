// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"fmt"
	"strings"

	"github.com/dop251/goja"
	weburl "github.com/nlnwa/whatwg-url/url"
	"nimbleterminal.dev/ntbrowser/dom"
)

// Anchors expose data, not authority. These bindings never call a host hook.
// In particular href/target/download/ping assignments cannot navigate, create
// a window, write a file, or issue a request. Reader activation stays in loop
// and app, where navigation and network policies are enforced.
func (s *Sandbox) anchorNode(v goja.Value) *dom.Node {
	n := s.node(v)
	if n.Tag() != "a" {
		panic(s.vm.NewTypeError("not an HTMLAnchorElement"))
	}
	return n
}

func (s *Sandbox) anchorURL(n *dom.Node) *weburl.Url {
	raw, exists := n.Attr("href")
	if !exists {
		return nil
	}
	u, err := weburl.ParseRef(s.cfg.Location, raw)
	if err != nil {
		return nil
	}
	return u
}

func (s *Sandbox) anchorHref(n *dom.Node) string {
	if u := s.anchorURL(n); u != nil {
		return u.Href(false)
	}
	raw, _ := n.Attr("href")
	return raw
}

// Origin is a serialization, never an authorization decision. Opaque and
// file origins serialize as null; blob inherits only an embedded HTTP(S)
// origin. Neither kind becomes navigable merely because it can be parsed.
func anchorOrigin(u *weburl.Url) string {
	if u.Scheme() == "blob" {
		nested, err := weburl.Parse(u.Pathname())
		if err != nil || (nested.Scheme() != "http" && nested.Scheme() != "https") {
			return "null"
		}
		u = nested
	}
	switch u.Scheme() {
	case "http", "https", "ws", "wss", "ftp":
		return u.Protocol() + "//" + u.Host()
	}
	return "null"
}

func (s *Sandbox) installAnchors() {
	p := s.vm.NewObject()
	s.anchorProto = p
	_ = p.SetPrototype(s.elemProto)
	ctor := s.vm.ToValue(func(goja.ConstructorCall) *goja.Object { panic(s.vm.NewTypeError("Illegal constructor")) }).(*goja.Object)
	_ = ctor.Set("prototype", p)
	defineFrozen(p, "constructor", ctor)
	defineFrozen(s.vm.GlobalObject(), "HTMLAnchorElement", ctor)
	s.relListSym = goja.NewSymbol("ntbrowser.relList")
	s.accessor(p, "href", func(t goja.Value) goja.Value { return s.vm.ToValue(s.anchorHref(s.anchorNode(t))) }, func(t, v goja.Value) { s.must(s.tree.SetAttr(s.anchorNode(t), "href", v.String())) })
	s.method(p, "toString", func(c goja.FunctionCall) goja.Value { return s.vm.ToValue(s.anchorHref(s.anchorNode(c.This))) })
	for _, part := range []struct {
		name string
		get  func(*weburl.Url) string
		set  func(*weburl.Url, string)
	}{
		{"protocol", (*weburl.Url).Protocol, (*weburl.Url).SetProtocol},
		{"username", (*weburl.Url).Username, (*weburl.Url).SetUsername},
		{"password", (*weburl.Url).Password, (*weburl.Url).SetPassword},
		{"host", (*weburl.Url).Host, (*weburl.Url).SetHost},
		{"hostname", (*weburl.Url).Hostname, (*weburl.Url).SetHostname},
		{"port", (*weburl.Url).Port, (*weburl.Url).SetPort},
		{"pathname", (*weburl.Url).Pathname, (*weburl.Url).SetPathname},
		{"search", (*weburl.Url).Search, (*weburl.Url).SetSearch},
		{"hash", (*weburl.Url).Hash, (*weburl.Url).SetHash},
		{"origin", anchorOrigin, nil},
	} {
		var setter func(goja.Value, goja.Value)
		if part.set != nil {
			setter = func(t, v goja.Value) {
				n := s.anchorNode(t)
				value := v.String()
				if len(value) > dom.DefaultLimits.TextBytes {
					s.throw(fmt.Errorf("%w: URL component too large", dom.ErrLimit))
				}
				u := s.anchorURL(n)
				if u == nil {
					return
				}
				part.set(u, value)
				s.must(s.tree.SetAttr(n, "href", u.Href(false)))
			}
		}
		s.accessor(p, part.name, func(t goja.Value) goja.Value {
			u := s.anchorURL(s.anchorNode(t))
			if u == nil {
				if part.name == "protocol" {
					return s.vm.ToValue(":")
				}
				return s.vm.ToValue("")
			}
			return s.vm.ToValue(part.get(u))
		}, setter)
	}
	for _, name := range []string{"target", "download", "ping", "rel", "hreflang", "type"} {
		s.accessor(p, name, func(t goja.Value) goja.Value { v, _ := s.anchorNode(t).Attr(name); return s.vm.ToValue(v) }, func(t, v goja.Value) { s.must(s.tree.SetAttr(s.anchorNode(t), name, v.String())) })
	}
	s.accessor(p, "referrerPolicy", func(t goja.Value) goja.Value {
		v, _ := s.anchorNode(t).Attr("referrerpolicy")
		v = strings.ToLower(v)
		switch v {
		case "no-referrer", "no-referrer-when-downgrade", "same-origin", "origin", "strict-origin", "origin-when-cross-origin", "strict-origin-when-cross-origin", "unsafe-url":
			return s.vm.ToValue(v)
		}
		return s.vm.ToValue("")
	}, func(t, v goja.Value) { s.must(s.tree.SetAttr(s.anchorNode(t), "referrerpolicy", v.String())) })
	s.accessor(p, "text", func(t goja.Value) goja.Value { return s.vm.ToValue(dom.TextContent(s.anchorNode(t))) }, func(t, v goja.Value) { s.anchorNode(t); s.setText(t, v) })
	s.accessor(p, "relList", func(t goja.Value) goja.Value {
		s.anchorNode(t)
		w := t.(*goja.Object)
		if list := w.GetSymbol(s.relListSym); list != nil {
			return list
		}
		list := s.anchorRelList(w)
		_ = w.DefineDataPropertySymbol(s.relListSym, list, goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
		return list
	}, func(t, v goja.Value) { s.must(s.tree.SetAttr(s.anchorNode(t), "rel", v.String())) })
}
