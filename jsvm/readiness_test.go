// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import "testing"

func TestDocumentReadinessAndWindowLoad(t *testing.T) {
	s, _ := eventSandbox(t, Config{})
	eventEval(t, s, `assert(document.nodeType===9&&document.ownerDocument===null);
 for(const n of [document.body,document.createElement('div'),document.createTextNode('x')])assert(n.ownerDocument===document&&n.ownerDocument.defaultView===window);
 assert(document.readyState==='loading');document.readyState='complete';assert(document.readyState==='loading');let seen=[];
 document.onreadystatechange=e=>{assert(e.isTrusted&&!e.bubbles&&!e.cancelable&&e.target===document);seen.push(document.readyState)};
 document.addEventListener('load',()=>{throw Error('window load reached document')});
 window.onload=e=>{assert(e.isTrusted&&e.target===document&&e.currentTarget===window&&!e.bubbles&&!e.cancelable);seen.push('load')};`)
	for _, state := range []string{"interactive", "interactive", "loading", "complete", "complete"} {
		if err := s.SetDocumentReady(state); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DispatchDocumentLoad(); err != nil {
		t.Fatal(err)
	}
	eventEval(t, s, `assert(seen.join()==='interactive,complete,load');assert(document.readyState==='complete')`)
}
