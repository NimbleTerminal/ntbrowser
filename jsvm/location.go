// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import (
	"github.com/dop251/goja"
	weburl "github.com/nlnwa/whatwg-url/url"
)

func (s *Sandbox) locationError(name, message string) {
	e := s.vm.NewTypeError(message)
	_ = e.Set("name", name)
	panic(e)
}
func (s *Sandbox) locationURL() *weburl.Url {
	u, err := weburl.Parse(s.cfg.Location)
	if err != nil {
		return nil
	}
	return u
}
func (s *Sandbox) requestLocation(raw, mode string) {
	if len(raw) > 2048 {
		s.locationError("SyntaxError", "location URL exceeds 2048 bytes")
	}
	u, err := weburl.ParseRef(s.cfg.Location, raw)
	if err != nil {
		s.locationError("SyntaxError", "invalid location URL")
	}
	if (u.Scheme() != "https" && u.Scheme() != "http") || u.Username() != "" || u.Password() != "" {
		s.locationError("SecurityError", "location permits only HTTP(S) URLs without credentials")
	}
	if len(u.Href(false)) > 2048 {
		s.locationError("SyntaxError", "location URL exceeds 2048 bytes")
	}
	if s.cfg.Navigate == nil {
		s.locationError("SecurityError", "script navigation is off in this profile")
	}
	if err := s.cfg.Navigate(u.Href(false), mode); err != nil {
		s.locationError("SecurityError", err.Error())
	}
}

func (s *Sandbox) installLocation() {
	vm := s.vm
	o := vm.NewObject()
	p := vm.NewObject()
	_ = o.SetPrototype(p)
	ctor := vm.ToValue(func(goja.ConstructorCall) *goja.Object { panic(vm.NewTypeError("Illegal constructor")) }).(*goja.Object)
	_ = ctor.Set("prototype", p)
	defineFrozen(p, "constructor", ctor)
	defineFrozen(vm.GlobalObject(), "Location", ctor)
	_ = o.DefineDataPropertySymbol(goja.SymToStringTag, vm.ToValue("Location"), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	check := func(v goja.Value) {
		if v != o {
			panic(vm.NewTypeError("not a Location"))
		}
	}
	s.accessor(o, "href", func(v goja.Value) goja.Value { check(v); return vm.ToValue(s.cfg.Location) }, func(t, v goja.Value) { check(t); s.requestLocation(v.String(), "assign") })
	for _, part := range []struct {
		name string
		get  func(*weburl.Url) string
		set  func(*weburl.Url, string)
	}{
		{"protocol", (*weburl.Url).Protocol, (*weburl.Url).SetProtocol},
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
				check(t)
				value := v.String()
				if len(value) > 2048 {
					s.locationError("SyntaxError", "location component too large")
				}
				u := s.locationURL()
				if u == nil {
					return
				}
				oldHash := u.Hash()
				part.set(u, value)
				if part.name == "hash" && oldHash == u.Hash() {
					return
				}
				s.requestLocation(u.Href(false), "assign")
			}
		}
		s.accessor(o, part.name, func(t goja.Value) goja.Value {
			check(t)
			u := s.locationURL()
			if u == nil {
				return vm.ToValue("")
			}
			return vm.ToValue(part.get(u))
		}, setter)
	}
	for _, mode := range []string{"assign", "replace", "reload"} {
		defineFrozen(o, mode, vm.ToValue(func(c goja.FunctionCall) goja.Value {
			check(c.This)
			raw := s.cfg.Location
			if mode != "reload" {
				if len(c.Arguments) == 0 {
					panic(vm.NewTypeError("URL argument required"))
				}
				raw = c.Argument(0).String()
			}
			s.requestLocation(raw, mode)
			return goja.Undefined()
		}))
	}
	defineFrozen(o, "toString", vm.ToValue(func(c goja.FunctionCall) goja.Value { check(c.This); return vm.ToValue(s.cfg.Location) }))
	for _, obj := range []*goja.Object{vm.GlobalObject(), s.docObj} {
		s.accessor(obj, "location", func(goja.Value) goja.Value { return o }, func(_ goja.Value, v goja.Value) { s.requestLocation(v.String(), "assign") })
	}
}

// SetLocation is called only by the owning loop after a same-document host
// navigation commits. It cannot change the document's origin.
func (s *Sandbox) SetLocation(raw string) { s.cfg.Location = raw }
