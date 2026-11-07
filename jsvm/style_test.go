// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import "testing"

func TestInlineStyleReflection(t *testing.T) {
	s, _ := eventSandbox(t, Config{})
	eventEval(t, s, `
 let checks=0;function assert(x,m){checks++;if(!x)throw Error(m||'check '+checks)}
 const el=document.getElementById('b'), style=el.style;
 assert(style===el.style && style instanceof CSSStyleDeclaration);
 assert(style.width==='' && style.length===0);
 style.width='12em';style.backgroundColor='red';style.cssFloat='left';
 assert(el.getAttribute('style').includes('width: 12em;'));
 assert(style.getPropertyValue('background-color')==='red' && style.float==='left');
 assert(style.length===3 && style[0]==='width' && style.item(50)==='');
 el.setAttribute('style','height: 30px');
 assert(style.width==='' && style.height==='30px');
 style.cssText='width: 2px !important; width: 3px; background-image: url("a;b"); --X: a';
 assert(style.width==='2px' && style.getPropertyPriority('width')==='important');
 assert(style.getPropertyValue('--X')==='a' && style.getPropertyValue('--x')==='');
 assert(style.backgroundImage==='url("a;b")');
 style.setProperty('width','4px','important');assert(style.width==='4px');
 style.setProperty('width','5px','nonsense');assert(style.width==='4px');
 style.width='invalid';assert(style.width==='4px');
 style.width='3px; height: 9px';assert(style.width==='4px');
 assert(style.removeProperty('width')==='4px' && style.width==='');
 let before=el.getAttribute('style'),failed=false;
 try{style.cssText='x'.repeat(65537)}catch(e){failed=true}
 assert(failed && el.getAttribute('style')===before);
 el.style='width: 10px';assert(style.width==='10px');
 style.cssText=null;assert(style.length===0);
 `)
}
