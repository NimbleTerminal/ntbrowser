// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"time"

	"github.com/dop251/goja"
)

// prepareClocks gives Date and performance the same quantization phase, so
// combining their outputs cannot recover finer resolution. The default raw
// clock retains Go's monotonic component for elapsed time. Injected clocks
// support deterministic replay; backward movement is clamped for performance.
func prepareClocks(cfg *Config) (origin float64, now func() float64) {
	resolution := cfg.ClockResolution
	if resolution <= 0 {
		resolution = 10 * time.Millisecond
	}
	cfg.ClockResolution = resolution
	raw := cfg.Clock
	if raw == nil {
		raw = time.Now
	}
	start := raw()
	coarseStart := start.Truncate(resolution)
	phase := start.Sub(coarseStart)
	cfg.Clock = func() time.Time { return raw().Truncate(resolution) }
	origin = float64(coarseStart.UnixMilli())
	var last time.Duration
	now = func() float64 {
		elapsed := (raw().Sub(start) + phase).Truncate(resolution)
		if elapsed > last {
			last = elapsed
		}
		return float64(last) / float64(time.Millisecond)
	}
	return origin, now
}

// PerformanceNow is also the host's requestAnimationFrame timestamp source.
// Like every sandbox operation it is called only on the VM goroutine.
func (s *Sandbox) PerformanceNow() float64 {
	if s.performanceNow == nil {
		return 0
	}
	return s.performanceNow()
}

func (s *Sandbox) installPerformance() {
	vm := s.vm
	p := vm.NewObject()
	object := vm.NewObject()
	_ = object.SetPrototype(p)
	brand := func(this goja.Value) {
		if !this.StrictEquals(object) {
			panic(vm.NewTypeError("Illegal invocation"))
		}
	}
	ctor := vm.ToValue(func(goja.ConstructorCall) *goja.Object { panic(vm.NewTypeError("Illegal constructor")) }).(*goja.Object)
	_ = ctor.Set("prototype", p)
	defineFrozen(p, "constructor", ctor)
	defineFrozen(vm.GlobalObject(), "Performance", ctor)
	s.method(p, "now", func(c goja.FunctionCall) goja.Value { brand(c.This); return vm.ToValue(s.PerformanceNow()) })
	s.accessor(p, "timeOrigin", func(this goja.Value) goja.Value { brand(this); return vm.ToValue(s.performanceOrigin) }, nil)
	// Legacy Navigation Timing: the document's fetch and response share the
	// coarse time origin, nothing finer is exposed, and the load milestones
	// read 0 ("not available") since the sandbox has no load lifecycle.
	timing := vm.NewObject()
	for _, name := range []string{"navigationStart", "fetchStart", "domainLookupStart", "domainLookupEnd", "connectStart", "connectEnd", "requestStart", "responseStart", "responseEnd", "domLoading"} {
		defineFrozen(timing, name, vm.ToValue(s.performanceOrigin))
	}
	for _, name := range []string{"unloadEventStart", "unloadEventEnd", "redirectStart", "redirectEnd", "secureConnectionStart", "domInteractive", "domContentLoadedEventStart", "domContentLoadedEventEnd", "domComplete", "loadEventStart", "loadEventEnd"} {
		defineFrozen(timing, name, vm.ToValue(0))
	}
	navigation := vm.NewObject()
	for name, v := range map[string]int{"type": 0, "redirectCount": 0, "TYPE_NAVIGATE": 0, "TYPE_RELOAD": 1, "TYPE_BACK_FORWARD": 2, "TYPE_RESERVED": 255} {
		defineFrozen(navigation, name, vm.ToValue(v))
	}
	s.accessor(p, "timing", func(this goja.Value) goja.Value { brand(this); return timing }, nil)
	s.accessor(p, "navigation", func(this goja.Value) goja.Value { brand(this); return navigation }, nil)
	// No entries are recorded, so the Performance Timeline getters return
	// fresh empty arrays; pages take that path rather than a missing method.
	for _, name := range []string{"getEntries", "getEntriesByType", "getEntriesByName"} {
		s.method(p, name, func(c goja.FunctionCall) goja.Value { brand(c.This); return vm.NewArray() })
	}
	s.method(p, "toJSON", func(c goja.FunctionCall) goja.Value {
		brand(c.This)
		out := vm.NewObject()
		_ = out.Set("timeOrigin", s.performanceOrigin)
		_ = out.Set("timing", timing)
		_ = out.Set("navigation", navigation)
		return out
	})
	defineFrozen(vm.GlobalObject(), "performance", object)
}
