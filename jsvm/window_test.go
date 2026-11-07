// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import "testing"

func TestSingleWindowRelationships(t *testing.T) {
	s, h := messageSandbox(t, Config{Location: "https://example.test/"})
	eventEval(t, s, `
 assert(top===window && parent===window && frames===window);
 assert(document.defaultView===window && window===self && self===globalThis);
 assert(length===0 && frames.length===0 && frames[0]===undefined);
 assert(opener===null && frameElement===null && closed===false);
 // DOM iframe elements do not create browsing contexts.
 document.body.appendChild(document.createElement('iframe'));
 assert(frames.length===0 && frames[0]===undefined);
 for(const key of ['top','frameElement','closed']) {
   const before=window[key];window[key]='spoof';assert(window[key]===before);
   if(key==='top')assert(!Reflect.deleteProperty(window,key));
 }
 let seen=[];onmessage=e=>{assert(e.source===window);seen.push(e.data)};
 top.postMessage('top','/');parent.postMessage('parent','/');frames.postMessage('frames','/');
 for(const target of [top,parent,frames,document.defaultView]) {
   let denied=false;try{target.location.replace('/next')}catch(e){denied=e.name==='SecurityError'}assert(denied);
 }
 assert(location.href==='https://example.test/');
 `)
	for i := 0; i < 3; i++ {
		messageStep(t, s, h)
	}
	eventEval(t, s, `assert(seen.join()==='top,parent,frames');
 parent={local:true};frames=[];opener={local:true};length=7;
 assert(parent.local&&Array.isArray(frames)&&opener.local&&length===7);
 assert(top===window&&document.defaultView===window);
 `)
	// Browser scripts commonly use this name for an animation frame counter.
	eventEval(t, s, `let frames=0;frames++;assert(frames===1 && Array.isArray(window.frames));`)
}
