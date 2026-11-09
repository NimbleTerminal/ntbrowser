// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"errors"
	"sort"
	"strings"

	"github.com/dop251/goja"
	weburl "github.com/nlnwa/whatwg-url/url"
)

// URL and URLSearchParams are pure parsing: the published WHATWG parser
// behind HTMLAnchorElement, no network, no host authority. A parsed URL
// grants nothing; navigation and fetch keep their own gates.

const (
	maxSearchParams     = 4096
	maxSearchParamBytes = 1 << 20
)

type urlState struct {
	u      *weburl.Url
	params *goja.Object // lazily created, linked URLSearchParams
}

type searchParamsState struct {
	pairs []weburl.NameValuePair
	bytes int       // names and values, kept current so the cap check is O(1)
	url   *urlState // when the list belongs to a URL's searchParams
}

func (st *searchParamsState) recount() {
	st.bytes = 0
	for _, p := range st.pairs {
		st.bytes += len(p.Name) + len(p.Value) + 2
	}
}

func (s *Sandbox) installURL() {
	vm := s.vm
	s.urlSym = goja.NewSymbol("ntbrowser.url")
	s.paramsSym = goja.NewSymbol("ntbrowser.searchParams")

	// ---- URLSearchParams
	pp := vm.NewObject()
	paramsState := func(v goja.Value) *searchParamsState {
		if o, ok := v.(*goja.Object); ok {
			if st, ok := o.GetSymbol(s.paramsSym).Export().(*searchParamsState); ok {
				return st
			}
		}
		panic(vm.NewTypeError("not a URLSearchParams"))
	}
	newParams := func(st *searchParamsState) *goja.Object {
		o := vm.NewObject()
		_ = o.SetPrototype(pp)
		_ = o.DefineDataPropertySymbol(s.paramsSym, vm.ToValue(st), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
		return o
	}
	update := func(st *searchParamsState) {
		if st.url != nil {
			st.url.u.SetSearch(serializeSearchParams(st.pairs))
		}
	}
	checkSize := func(st *searchParamsState) {
		if len(st.pairs) > maxSearchParams {
			panic(vm.NewTypeError("too many search parameters"))
		}
		if st.bytes > maxSearchParamBytes {
			panic(vm.NewTypeError("search parameters too large"))
		}
	}
	paramsCtor := func(call goja.ConstructorCall) *goja.Object {
		st := &searchParamsState{}
		init := call.Argument(0)
		switch {
		case goja.IsUndefined(init) || goja.IsNull(init):
		case init.ExportType() != nil && init.ExportType().Kind().String() == "string":
			st.pairs = parseSearchParams(strings.TrimPrefix(init.String(), "?"))
		default:
			o, ok := init.(*goja.Object)
			if !ok {
				st.pairs = parseSearchParams(strings.TrimPrefix(init.String(), "?"))
				break
			}
			if iter := o.GetSymbol(goja.SymIterator); iter != nil && !goja.IsUndefined(iter) {
				vm.ForOf(o, func(v goja.Value) bool {
					pair, ok := v.(*goja.Object)
					if !ok {
						panic(vm.NewTypeError("URLSearchParams sequence entries must be pairs"))
					}
					var kv []string
					vm.ForOf(pair, func(e goja.Value) bool { kv = append(kv, e.String()); return len(kv) <= 2 })
					if len(kv) != 2 {
						panic(vm.NewTypeError("URLSearchParams sequence entries must be pairs"))
					}
					st.pairs = append(st.pairs, weburl.NameValuePair{Name: kv[0], Value: kv[1]})
					return len(st.pairs) <= maxSearchParams
				})
			} else {
				for _, k := range o.Keys() {
					st.pairs = append(st.pairs, weburl.NameValuePair{Name: k, Value: o.Get(k).String()})
				}
			}
		}
		st.recount()
		checkSize(st)
		o := newParams(st)
		return o
	}
	ctor := vm.ToValue(paramsCtor).(*goja.Object)
	_ = ctor.Set("prototype", pp)
	defineFrozen(pp, "constructor", ctor)
	defineFrozen(vm.GlobalObject(), "URLSearchParams", ctor)
	s.method(pp, "append", func(c goja.FunctionCall) goja.Value {
		st := paramsState(c.This)
		p := weburl.NameValuePair{Name: c.Argument(0).String(), Value: c.Argument(1).String()}
		st.pairs = append(st.pairs, p)
		st.bytes += len(p.Name) + len(p.Value) + 2
		checkSize(st)
		update(st)
		return goja.Undefined()
	})
	s.method(pp, "delete", func(c goja.FunctionCall) goja.Value {
		st := paramsState(c.This)
		name := c.Argument(0).String()
		kept := st.pairs[:0]
		for _, p := range st.pairs {
			if p.Name == name && (len(c.Arguments) < 2 || goja.IsUndefined(c.Argument(1)) || p.Value == c.Argument(1).String()) {
				continue
			}
			kept = append(kept, p)
		}
		st.pairs = kept
		st.recount()
		update(st)
		return goja.Undefined()
	})
	s.method(pp, "get", func(c goja.FunctionCall) goja.Value {
		for _, p := range paramsState(c.This).pairs {
			if p.Name == c.Argument(0).String() {
				return vm.ToValue(p.Value)
			}
		}
		return goja.Null()
	})
	s.method(pp, "getAll", func(c goja.FunctionCall) goja.Value {
		var out []any
		for _, p := range paramsState(c.This).pairs {
			if p.Name == c.Argument(0).String() {
				out = append(out, p.Value)
			}
		}
		return vm.NewArray(out...)
	})
	s.method(pp, "has", func(c goja.FunctionCall) goja.Value {
		for _, p := range paramsState(c.This).pairs {
			if p.Name == c.Argument(0).String() && (len(c.Arguments) < 2 || goja.IsUndefined(c.Argument(1)) || p.Value == c.Argument(1).String()) {
				return vm.ToValue(true)
			}
		}
		return vm.ToValue(false)
	})
	s.method(pp, "set", func(c goja.FunctionCall) goja.Value {
		st := paramsState(c.This)
		name, val := c.Argument(0).String(), c.Argument(1).String()
		kept := st.pairs[:0]
		done := false
		for _, p := range st.pairs {
			if p.Name == name {
				if done {
					continue
				}
				p.Value, done = val, true
			}
			kept = append(kept, p)
		}
		if !done {
			kept = append(kept, weburl.NameValuePair{Name: name, Value: val})
		}
		st.pairs = kept
		st.recount()
		checkSize(st)
		update(st)
		return goja.Undefined()
	})
	s.method(pp, "sort", func(c goja.FunctionCall) goja.Value {
		st := paramsState(c.This)
		sort.SliceStable(st.pairs, func(i, j int) bool { return utf16Less(st.pairs[i].Name, st.pairs[j].Name) })
		update(st)
		return goja.Undefined()
	})
	s.method(pp, "toString", func(c goja.FunctionCall) goja.Value {
		return vm.ToValue(serializeSearchParams(paramsState(c.This).pairs))
	})
	s.accessor(pp, "size", func(t goja.Value) goja.Value { return vm.ToValue(len(paramsState(t).pairs)) }, nil)
	s.method(pp, "forEach", func(c goja.FunctionCall) goja.Value {
		st := paramsState(c.This)
		fn, ok := goja.AssertFunction(c.Argument(0))
		if !ok {
			panic(vm.NewTypeError("forEach callback must be a function"))
		}
		for i := 0; i < len(st.pairs); i++ {
			p := st.pairs[i]
			if _, err := fn(c.Argument(1), vm.ToValue(p.Value), vm.ToValue(p.Name), c.This); err != nil {
				panic(err)
			}
		}
		return goja.Undefined()
	})
	iterate := func(pick func(weburl.NameValuePair) any) func(c goja.FunctionCall) goja.Value {
		return func(c goja.FunctionCall) goja.Value {
			st := paramsState(c.This)
			items := make([]any, len(st.pairs))
			for i, p := range st.pairs {
				items[i] = pick(p)
			}
			arr := vm.NewArray(items...)
			values, _ := goja.AssertFunction(arr.Get("values"))
			it, err := values(arr)
			if err != nil {
				panic(err)
			}
			return it
		}
	}
	entries := iterate(func(p weburl.NameValuePair) any { return vm.NewArray(p.Name, p.Value) })
	s.method(pp, "entries", entries)
	s.method(pp, "keys", iterate(func(p weburl.NameValuePair) any { return p.Name }))
	s.method(pp, "values", iterate(func(p weburl.NameValuePair) any { return p.Value }))
	_ = pp.DefineDataPropertySymbol(goja.SymIterator, pp.Get("entries"), goja.FLAG_TRUE, goja.FLAG_FALSE, goja.FLAG_TRUE)
	_ = pp.DefineDataPropertySymbol(goja.SymToStringTag, vm.ToValue("URLSearchParams"), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE)

	// ---- URL
	up := vm.NewObject()
	stateOf := func(v goja.Value) *urlState {
		if o, ok := v.(*goja.Object); ok {
			if st, ok := o.GetSymbol(s.urlSym).Export().(*urlState); ok {
				return st
			}
		}
		panic(vm.NewTypeError("not a URL"))
	}
	parse := func(c []goja.Value) (*weburl.Url, error) {
		raw := ""
		if len(c) > 0 {
			raw = c[0].String()
		}
		if len(raw) > 8192 {
			return nil, errors.New("url too long")
		}
		if len(c) > 1 && !goja.IsUndefined(c[1]) {
			base, err := weburl.Parse(c[1].String())
			if err != nil {
				return nil, err
			}
			return base.Parse(raw)
		}
		return weburl.Parse(raw)
	}
	urlCtor := func(call goja.ConstructorCall) *goja.Object {
		u, err := parse(call.Arguments)
		if err != nil {
			panic(vm.NewTypeError("Invalid URL"))
		}
		o := vm.NewObject()
		_ = o.SetPrototype(up)
		_ = o.DefineDataPropertySymbol(s.urlSym, vm.ToValue(&urlState{u: u}), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
		return o
	}
	uctor := vm.ToValue(urlCtor).(*goja.Object)
	_ = uctor.Set("prototype", up)
	defineFrozen(up, "constructor", uctor)
	defineFrozen(vm.GlobalObject(), "URL", uctor)
	s.method(uctor, "canParse", func(c goja.FunctionCall) goja.Value { _, err := parse(c.Arguments); return vm.ToValue(err == nil) })
	s.method(uctor, "parse", func(c goja.FunctionCall) goja.Value {
		u, err := parse(c.Arguments)
		if err != nil {
			return goja.Null()
		}
		o := vm.NewObject()
		_ = o.SetPrototype(up)
		_ = o.DefineDataPropertySymbol(s.urlSym, vm.ToValue(&urlState{u: u}), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
		return o
	})
	href := func(t goja.Value) goja.Value { return vm.ToValue(stateOf(t).u.Href(false)) }
	s.accessor(up, "href", href, func(t, v goja.Value) {
		st := stateOf(t)
		u, err := weburl.Parse(v.String())
		if err != nil {
			panic(vm.NewTypeError("Invalid URL"))
		}
		st.u = u
		s.resetLinkedParams(st)
	})
	s.method(up, "toString", func(c goja.FunctionCall) goja.Value { return href(c.This) })
	s.method(up, "toJSON", func(c goja.FunctionCall) goja.Value { return href(c.This) })
	s.accessor(up, "origin", func(t goja.Value) goja.Value { return vm.ToValue(anchorOrigin(stateOf(t).u)) }, nil)
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
	} {
		part := part
		s.accessor(up, part.name, func(t goja.Value) goja.Value { return vm.ToValue(part.get(stateOf(t).u)) }, func(t, v goja.Value) {
			st := stateOf(t)
			part.set(st.u, v.String())
			if part.name == "search" {
				s.resetLinkedParams(st)
			}
		})
	}
	s.accessor(up, "searchParams", func(t goja.Value) goja.Value {
		st := stateOf(t)
		if st.params == nil {
			ps := &searchParamsState{pairs: parseSearchParams(strings.TrimPrefix(st.u.Search(), "?")), url: st}
			ps.recount()
			st.params = newParams(ps)
		}
		return st.params
	}, nil)
	_ = up.DefineDataPropertySymbol(goja.SymToStringTag, vm.ToValue("URL"), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE)
}

// resetLinkedParams re-reads a URL's searchParams after its search changed.
func (s *Sandbox) resetLinkedParams(st *urlState) {
	if st.params == nil {
		return
	}
	if ps, ok := st.params.GetSymbol(s.paramsSym).Export().(*searchParamsState); ok {
		ps.pairs = parseSearchParams(strings.TrimPrefix(st.u.Search(), "?"))
		ps.recount()
	}
}

// parseSearchParams is the application/x-www-form-urlencoded parser.
func parseSearchParams(s string) []weburl.NameValuePair {
	var out []weburl.NameValuePair
	for _, part := range strings.Split(s, "&") {
		if part == "" {
			continue
		}
		name, value, _ := strings.Cut(part, "=")
		out = append(out, weburl.NameValuePair{Name: formDecode(name), Value: formDecode(value)})
		if len(out) >= maxSearchParams {
			break
		}
	}
	return out
}

func formDecode(s string) string {
	s = strings.ReplaceAll(s, "+", " ")
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			b.WriteByte(unhex(s[i+1])<<4 | unhex(s[i+2]))
			i += 2
			continue
		}
		b.WriteByte(s[i])
	}
	return strings.ToValidUTF8(b.String(), "\uFFFD")
}

// serializeSearchParams is the application/x-www-form-urlencoded serializer.
func serializeSearchParams(pairs []weburl.NameValuePair) string {
	var b strings.Builder
	for i, p := range pairs {
		if i > 0 {
			b.WriteByte('&')
		}
		formEncode(&b, p.Name)
		b.WriteByte('=')
		formEncode(&b, p.Value)
	}
	return b.String()
}

func formEncode(b *strings.Builder, s string) {
	const hex = "0123456789ABCDEF"
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ':
			b.WriteByte('+')
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '*', c == '-', c == '.', c == '_':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
}

func isHex(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }
func unhex(c byte) byte {
	switch {
	case c >= 'a':
		return c - 'a' + 10
	case c >= 'A':
		return c - 'A' + 10
	}
	return c - '0'
}

// utf16Less orders by UTF-16 code units, as URLSearchParams.sort requires.
func utf16Less(a, b string) bool {
	ra, rb := []rune(a), []rune(b)
	for i := 0; i < len(ra) && i < len(rb); i++ {
		if ra[i] != rb[i] {
			ua, ub := utf16Units(ra[i]), utf16Units(rb[i])
			return ua < ub
		}
	}
	return len(ra) < len(rb)
}

func utf16Units(r rune) uint32 {
	if r >= 0x10000 {
		r -= 0x10000
		return uint32(0xD800+(r>>10))<<16 | uint32(0xDC00+(r&0x3FF))
	}
	return uint32(r) << 16
}
