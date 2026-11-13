// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"strconv"
	"strings"
	"weak"

	"github.com/dop251/goja"
)

// MediaState is what window.matchMedia evaluates against: the layout width
// and viewport height in CSS pixels and the preferred color scheme. It is
// rendering state the page could already infer from its layout, not host
// authority.
type MediaState struct {
	ReducedMotion string // empty/no-preference or reduce
	Hover         string // empty/hover or none
	Scripting     string // empty/enabled, initial-only, or none
	Width, Height int
	ColorScheme   string  // "light" or "dark"
	Scale         float64 // device pixels per CSS pixel (window.devicePixelRatio); 0 means 1
}

// scale is the device pixel ratio the page sees, 1 unless the host set one.
func (st MediaState) scale() float64 {
	if st.Scale >= 1 {
		return st.Scale
	}
	return 1
}

func defaultMedia() MediaState { return MediaState{Width: 800, Height: 600, ColorScheme: "light"} }

// maxMediaLists bounds live MediaQueryList objects per page.
const maxMediaLists = 256

type mediaList struct {
	ref     weak.Pointer[goja.Object]
	query   string
	matches bool
}

func (s *Sandbox) media() MediaState {
	if s.cfg.Media == nil {
		return defaultMedia()
	}
	st := s.cfg.Media()
	if st.Width <= 0 {
		st.Width = 800
	}
	if st.Height <= 0 {
		st.Height = 600
	}
	if st.ColorScheme != "dark" {
		st.ColorScheme = "light"
	}
	return st
}

// installMatchMedia defines window.matchMedia and MediaQueryList: a bounded
// set of live lists whose matches follow the host's media state, with change
// events when the host reports a new width or scheme.
func (s *Sandbox) installMatchMedia() {
	vm := s.vm
	proto := vm.NewObject()
	_ = proto.SetPrototype(s.eventTargetProto)
	ctor := vm.ToValue(func(goja.ConstructorCall) *goja.Object { panic(vm.NewTypeError("Illegal constructor")) }).(*goja.Object)
	_ = ctor.Set("prototype", proto)
	_ = ctor.SetPrototype(vm.Get("EventTarget").(*goja.Object))
	defineFrozen(proto, "constructor", ctor)
	defineFrozen(vm.GlobalObject(), "MediaQueryList", ctor)
	state := func(t goja.Value) *goja.Object {
		st := s.brand(t, "EventTarget")
		if !flag(st, "mediaList") {
			panic(vm.NewTypeError("not a MediaQueryList"))
		}
		return st
	}
	s.accessor(proto, "media", func(t goja.Value) goja.Value { return value(state(t), "media") }, nil)
	s.accessor(proto, "matches", func(t goja.Value) goja.Value {
		return vm.ToValue(evalMediaQuery(value(state(t), "media").String(), s.media()))
	}, nil)
	s.bindEventHandlers(proto)
	listenerAlias := func(add bool) func(c goja.FunctionCall) goja.Value {
		return func(c goja.FunctionCall) goja.Value {
			state(c.This)
			name := "removeEventListener"
			if add {
				name = "addEventListener"
			}
			fn, ok := goja.AssertFunction(proto.Get(name))
			if !ok {
				panic(vm.NewTypeError(name + " missing"))
			}
			if _, err := fn(c.This, vm.ToValue("change"), c.Argument(0)); err != nil {
				panic(err)
			}
			return goja.Undefined()
		}
	}
	s.method(proto, "addListener", listenerAlias(true))
	s.method(proto, "removeListener", listenerAlias(false))
	// devicePixelRatio is the same host state the resolution queries see:
	// a layout fact the page already observes, not authority.
	s.accessor(vm.GlobalObject(), "devicePixelRatio", func(goja.Value) goja.Value { return vm.ToValue(s.media().scale()) }, nil)
	s.method(vm.GlobalObject(), "matchMedia", func(c goja.FunctionCall) goja.Value {
		if len(c.Arguments) == 0 {
			panic(vm.NewTypeError("matchMedia requires a query"))
		}
		query := strings.TrimSpace(c.Argument(0).String())
		if len(query) > 1024 {
			panic(vm.NewTypeError("media query too long"))
		}
		o := vm.NewObject()
		_ = o.SetPrototype(proto)
		s.registerEventTarget(o, 0)
		st := s.private(o)
		_ = st.Set("mediaList", true)
		_ = st.Set("media", query)
		s.pruneMediaLists()
		if len(s.mediaLists) >= maxMediaLists {
			panic(vm.NewTypeError("too many MediaQueryList objects"))
		}
		s.mediaLists = append(s.mediaLists, &mediaList{ref: weak.Make(o), query: query, matches: evalMediaQuery(query, s.media())})
		return o
	})
}

func (s *Sandbox) pruneMediaLists() {
	live := s.mediaLists[:0]
	for _, l := range s.mediaLists {
		if l.ref.Value() != nil {
			live = append(live, l)
		}
	}
	for i := len(live); i < len(s.mediaLists); i++ {
		s.mediaLists[i] = nil
	}
	s.mediaLists = live
}

// MediaChanged re-evaluates every live MediaQueryList after the host's width
// or scheme changed and fires change events on those whose result flipped,
// as one entry under the usual watchdog.
func (s *Sandbox) MediaChanged() error {
	if s.vm == nil || s.disabled {
		return nil
	}
	st := s.media()
	var flipped []*mediaList
	s.pruneMediaLists()
	for _, l := range s.mediaLists {
		if now := evalMediaQuery(l.query, st); now != l.matches {
			l.matches = now
			flipped = append(flipped, l)
		}
	}
	if len(flipped) == 0 {
		return nil
	}
	return s.Run(func(vm *goja.Runtime) error {
		for _, l := range flipped {
			target := l.ref.Value()
			if target == nil {
				continue
			}
			ev := s.newEvent("change", false, false, false, s.eventProto)
			defineFrozen(ev, "matches", vm.ToValue(l.matches))
			defineFrozen(ev, "media", vm.ToValue(l.query))
			if _, err := s.dispatchEvent(target, ev); err != nil {
				return err
			}
		}
		return nil
	})
}

// evalMediaQuery evaluates a media query list against the state: comma
// separated queries OR together; each is an optional only/not, a media type
// and "and"-joined parenthesised features. Unknown features and types are
// false, as a browser treats them.
func evalMediaQuery(list string, st MediaState) bool {
	for _, q := range splitTopLevel(strings.ToLower(list), ',') {
		if evalOneMediaQuery(strings.TrimSpace(q), st) {
			return true
		}
	}
	return false
}

func evalOneMediaQuery(q string, st MediaState) bool {
	if q == "" {
		return true
	}
	negate := false
	if rest, ok := strings.CutPrefix(q, "not "); ok {
		negate, q = true, strings.TrimSpace(rest)
	} else if rest, ok := strings.CutPrefix(q, "only "); ok {
		q = strings.TrimSpace(rest)
	}
	result := true
	for _, part := range splitMediaAnd(q) {
		part = strings.TrimSpace(part)
		switch {
		case part == "":
			continue
		case strings.HasPrefix(part, "("):
			if !strings.HasSuffix(part, ")") {
				return false
			}
			feature := strings.TrimSpace(part[1 : len(part)-1])
			name, val, has := strings.Cut(feature, ":")
			name = strings.TrimSpace(name)
			val = strings.TrimSpace(val)
			if has {
				switch name {
				case "prefers-reduced-motion":
					if val != "reduce" && val != "no-preference" {
						return false
					}
				case "hover", "any-hover":
					if val != "hover" && val != "none" {
						return false
					}
				case "scripting":
					if val != "none" && val != "initial-only" && val != "enabled" {
						return false
					}
				}
			}
			if !evalMediaFeature(feature, st) {
				result = false
			}
		case part == "all" || part == "screen":
		default:
			result = false // print, speech, tv and unknown types
		}
	}
	return result != negate
}

// splitMediaAnd splits on the keyword "and" outside parentheses.
func splitMediaAnd(q string) []string {
	var parts []string
	depth, start := 0, 0
	for i := 0; i < len(q); i++ {
		switch q[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case 'a':
			if depth == 0 && strings.HasPrefix(q[i:], "and") && (i == 0 || q[i-1] == ' ' || q[i-1] == ')') && (i+3 == len(q) || q[i+3] == ' ' || q[i+3] == '(') {
				parts = append(parts, q[start:i])
				start = i + 3
				i += 2
			}
		}
	}
	return append(parts, q[start:])
}

func splitTopLevel(s string, sep byte) []string {
	var parts []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case sep:
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, s[start:])
}

func evalMediaFeature(f string, st MediaState) bool {
	// Range syntax: width >= 768px, 768px <= width, 600px < width <= 1200px.
	for _, op := range []string{"<=", ">=", "<", ">", "="} {
		if strings.Contains(f, op) {
			return evalMediaRange(f, st)
		}
	}
	name, val, hasVal := strings.Cut(f, ":")
	name, val = strings.TrimSpace(name), strings.TrimSpace(val)
	dim := func(base string) (int, bool) {
		switch base {
		case "width":
			return st.Width, true
		case "height":
			return st.Height, true
		}
		return 0, false
	}
	if rest, ok := strings.CutPrefix(name, "min-"); ok {
		if d, ok := dim(rest); ok {
			n, ok := mediaLength(val)
			return ok && d >= n
		}
	}
	if rest, ok := strings.CutPrefix(name, "max-"); ok {
		if d, ok := dim(rest); ok {
			n, ok := mediaLength(val)
			return ok && d <= n
		}
	}
	if d, ok := dim(name); ok {
		if !hasVal {
			return d > 0
		}
		n, ok := mediaLength(val)
		return ok && d == n
	}
	switch name {
	case "prefers-color-scheme":
		return val == st.ColorScheme
	case "orientation":
		if st.Width >= st.Height {
			return val == "landscape"
		}
		return val == "portrait"
	case "prefers-reduced-motion":
		preference := "no-preference"
		if st.ReducedMotion == "reduce" {
			preference = "reduce"
		}
		return !hasVal && preference == "reduce" || hasVal && val == preference
	case "prefers-reduced-transparency", "prefers-reduced-data":
		return !hasVal || val == "no-preference"
	case "prefers-contrast":
		return val == "no-preference"
	case "forced-colors", "inverted-colors":
		return !hasVal && false || val == "none"
	case "hover", "any-hover":
		capability := "hover"
		if st.Hover == "none" {
			capability = "none"
		}
		return !hasVal && capability == "hover" || hasVal && val == capability
	case "pointer", "any-pointer":
		return !hasVal || val == "fine"
	case "display-mode":
		return val == "browser"
	case "scripting":
		capability := "enabled"
		if st.Scripting == "none" || st.Scripting == "initial-only" {
			capability = st.Scripting
		}
		return !hasVal && capability != "none" || hasVal && val == capability
	case "update":
		return !hasVal || val == "fast"
	case "color":
		return !hasVal || val == "8"
	case "monochrome":
		return hasVal && val == "0"
	case "color-gamut":
		return val == "srgb"
	case "resolution", "min-resolution", "max-resolution", "-webkit-device-pixel-ratio", "-webkit-min-device-pixel-ratio", "-webkit-max-device-pixel-ratio":
		r, ok := mediaResolution(val)
		if !ok {
			return false
		}
		switch {
		case strings.HasPrefix(name, "min-") || strings.HasPrefix(name, "-webkit-min"):
			return st.scale() >= r
		case strings.HasPrefix(name, "max-") || strings.HasPrefix(name, "-webkit-max"):
			return st.scale() <= r
		}
		return r == st.scale()
	}
	return false
}

func evalMediaRange(f string, st MediaState) bool {
	tokens := strings.Fields(f)
	// Normalise "a op b" and "a op b op c" into comparisons.
	compare := func(l, op, r string) bool {
		lv, lok := mediaOperand(l, st)
		rv, rok := mediaOperand(r, st)
		if !lok || !rok {
			return false
		}
		switch op {
		case "<":
			return lv < rv
		case "<=":
			return lv <= rv
		case ">":
			return lv > rv
		case ">=":
			return lv >= rv
		case "=":
			return lv == rv
		}
		return false
	}
	switch len(tokens) {
	case 3:
		return compare(tokens[0], tokens[1], tokens[2])
	case 5:
		return compare(tokens[0], tokens[1], tokens[2]) && compare(tokens[2], tokens[3], tokens[4])
	}
	return false
}

func mediaOperand(tok string, st MediaState) (int, bool) {
	switch tok {
	case "width":
		return st.Width, true
	case "height":
		return st.Height, true
	}
	return mediaLength(tok)
}

// mediaLength parses px, em and rem (16px) lengths and unitless zero.
func mediaLength(v string) (int, bool) {
	v = strings.TrimSpace(v)
	if v == "0" {
		return 0, true
	}
	mult := 1.0
	switch {
	case strings.HasSuffix(v, "px"):
		v = strings.TrimSuffix(v, "px")
	case strings.HasSuffix(v, "rem"):
		v, mult = strings.TrimSuffix(v, "rem"), 16
	case strings.HasSuffix(v, "em"):
		v, mult = strings.TrimSuffix(v, "em"), 16
	default:
		return 0, false
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || n < 0 || n > 1<<20 {
		return 0, false
	}
	return int(n*mult + 0.5), true
}

func mediaResolution(v string) (float64, bool) {
	v = strings.TrimSpace(v)
	for _, u := range []struct {
		suffix string
		per    float64
	}{{"dppx", 1}, {"x", 1}, {"dpi", 96}, {"dpcm", 96 / 2.54}} {
		if n, ok := strings.CutSuffix(v, u.suffix); ok {
			f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
			return f / u.per, err == nil
		}
	}
	f, err := strconv.ParseFloat(v, 64) // bare ratio, as -webkit-device-pixel-ratio takes
	return f, err == nil
}
