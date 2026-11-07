// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import "testing"

func TestInnerHTMLWrappersAndScriptInertness(t *testing.T) {
	s, _ := eventSandbox(t, Config{AllowDynamicCode: true, DynamicScripts: true})
	eventEval(t, s, `
 const old=document.getElementById('b'),parent=old.parentNode;let clicks=0,runs=0;
 old.addEventListener('click',()=>clicks++);
 parent.innerHTML='<span id="new">new &amp; text</span><script>runs++</script>';
 assert(!old.isConnected&&old.textContent==='go');old.dispatchEvent(new Event('click'));assert(clicks===1);
 assert(document.getElementById('new').ownerDocument===document&&document.getElementById('new').textContent==='new & text');
 const script=parent.querySelector('script');document.body.appendChild(script);script.text='runs++';assert(runs===0);
 parent.appendChild(old);old.dispatchEvent(new Event('click'));assert(clicks===2&&old.isConnected);
 const before=parent.innerHTML;let failed=false;try{parent.innerHTML='x'.repeat(65537)}catch(e){failed=true}assert(failed&&parent.innerHTML===before);
 parent.innerHTML=null;assert(parent.childNodes.length===0);
 const table=document.createElement('table');table.innerHTML='<tr><td>cell</td></tr>';assert(table.querySelector('td').textContent==='cell');
 `)
}
