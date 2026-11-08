// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import "testing"

// TestHistoryIsInertPageLocalState: history exists for scripts that wrap
// pushState (analytics beacons, routers) but changes nothing the host owns.
func TestHistoryIsInertPageLocalState(t *testing.T) {
	s := dynamicSandbox(t, false)
	s.cfg.Location = "https://example.test/a"
	eventEval(t, s, `
 assert(typeof history==='object'&&history===window.history&&history instanceof History);
 assert(history.length===1&&history.state===null&&history.scrollRestoration==='auto');
 const st={page:2};
 assert(history.pushState(st,'','/b')===undefined&&history.state===st&&history.length===1);
 assert(location.href==='https://example.test/a');
 history.replaceState(null,'');assert(history.state===null);
 history.back();history.forward();history.go(-1);history.go();
 history.scrollRestoration='manual';assert(history.scrollRestoration==='manual');
 history.scrollRestoration='bogus';assert(history.scrollRestoration==='manual');
 let n='';try{history.pushState(1)}catch(e){n=e.name}assert(n==='TypeError');
 n='';try{History.prototype.pushState.call({},1,'')}catch(e){n=e.name}assert(n==='TypeError');
 n='';try{new History()}catch(e){n=e.name}assert(n==='TypeError');
 // Wrapping pushState, as beacons do, is ordinary property assignment and
 // must take effect on the instance without touching the prototype.
 const orig=history.pushState;let wrapped=0;history.pushState=function(...a){wrapped++;return orig.apply(this,a)};
 history.pushState({p:3},'');assert(wrapped===1&&history.state.p===3&&History.prototype.pushState===orig);
 `)
	if s.cfg.Location != "https://example.test/a" {
		t.Fatal("history changed the committed location")
	}
}
