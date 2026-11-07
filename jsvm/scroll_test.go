// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import "testing"

func TestWindowScrollArguments(t *testing.T) {
	x, y := float64(0), float64(0)
	s, _ := eventSandbox(t, Config{ScrollPosition: func() (float64, float64) { return x, y }, Scroll: func(a, b float64, relative bool) {
		if relative {
			x += a
			y += b
		} else {
			x, y = a, b
		}
	}})
	eventEval(t, s, `
 scrollTo(10,20);assert(scrollX===10&&scrollY===20&&pageXOffset===10&&pageYOffset===20);
 scrollBy({top:5});assert(scrollX===10&&scrollY===25);
 scroll({left:8,behavior:'smooth'});assert(scrollX===8&&scrollY===25);
 scrollTo(NaN,Infinity);assert(scrollX===0&&scrollY===0);
 let bad=false;try{scrollTo({behavior:'bogus'})}catch(e){bad=e instanceof TypeError}assert(bad);
 `)
}
