// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import (
	"github.com/dop251/goja"
	"math"
)

func (s *Sandbox) installScroll() {
	if s.cfg.ScrollPosition == nil || s.cfg.Scroll == nil {
		return
	}
	s.installScrollOn(s.vm.GlobalObject(), s.cfg.ScrollPosition, s.cfg.Scroll)
}

func (s *Sandbox) installScrollOn(g *goja.Object, position func() (float64, float64), scroll func(float64, float64, bool)) {
	for _, key := range []string{"scrollX", "scrollY", "pageXOffset", "pageYOffset"} {
		s.accessor(g, key, func(goja.Value) goja.Value {
			x, y := position()
			if key == "scrollX" || key == "pageXOffset" {
				return s.vm.ToValue(x)
			}
			return s.vm.ToValue(y)
		}, nil)
	}
	for _, name := range []string{"scroll", "scrollTo", "scrollBy"} {
		s.method(g, name, func(c goja.FunctionCall) goja.Value {
			relative := name == "scrollBy"
			x, y := position()
			if relative {
				x, y = 0, 0
			}
			number := func(v goja.Value) float64 {
				n := v.ToFloat()
				if math.IsNaN(n) || math.IsInf(n, 0) {
					return 0
				}
				return math.Max(-1e7, math.Min(1e7, n))
			}
			if len(c.Arguments) < 2 && (goja.IsNull(c.Argument(0)) || goja.IsUndefined(c.Argument(0))) {
				return goja.Undefined()
			}
			if o, ok := c.Argument(0).(*goja.Object); ok && len(c.Arguments) < 2 {
				if v := o.Get("left"); v != nil && !goja.IsUndefined(v) {
					x = number(v)
				}
				if v := o.Get("top"); v != nil && !goja.IsUndefined(v) {
					y = number(v)
				}
				if v := o.Get("behavior"); v != nil && !goja.IsUndefined(v) {
					b := v.String()
					if b != "auto" && b != "instant" && b != "smooth" {
						panic(s.vm.NewTypeError("invalid scroll behavior"))
					}
				}
			} else if len(c.Arguments) > 0 {
				x, y = number(c.Argument(0)), number(c.Argument(1))
			}
			scroll(x, y, relative)
			return goja.Undefined()
		})
	}
}
