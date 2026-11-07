// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import "testing"

func TestWindowPostMessageOriginAndOrdering(t *testing.T) {
	s, h := messageSandbox(t, Config{Location: "https://Example.test:443/path"})
	eventEval(t, s, `
 let seen=[],order=[];const original={value:1};
 window.onmessage=function(e){assert(this===window&&e.target===window&&e.source===window&&e.origin==='https://example.test');assert(e instanceof MessageEvent&&e.isTrusted&&!e.bubbles&&!e.cancelable&&Object.isFrozen(e.ports)&&e.ports.length===0);seen.push(e.data);order.push('message');Promise.resolve().then(()=>order.push('micro'))};
 postMessage(original);original.value=9;postMessage('wildcard','*');postMessage('default','/');postMessage('options',{targetOrigin:'https://EXAMPLE.test:443/ignored?query#hash',transfer:[]});postMessage('legacy','https://example.test',[]);
 postMessage('wrong-host','https://other.test');postMessage('wrong-port','https://example.test:444');postMessage('wrong-scheme','http://example.test');
 Promise.resolve().then(()=>order.push('promise'));order.push('script');assert(seen.length===0);
 `)
	messageStep(t, s, h)
	eventEval(t, s, `assert(seen[0].value===1&&order.join()==='script,promise,message,micro')`)
	for i := 0; i < 7; i++ {
		messageStep(t, s, h)
	}
	eventEval(t, s, `assert(seen.length===5&&seen.slice(1).join()==='wildcard,default,options,legacy')`)
	if s.messageBytes != 0 || len(s.portMessages) != 0 {
		t.Fatal("discarded origins retained queue charge")
	}
}
func TestWindowPostMessageValidationAndOpaqueOrigins(t *testing.T) {
	s, h := messageSandbox(t, Config{Location: "file:///tmp/example.html"})
	eventEval(t, s, `
 let seen=[];onmessage=e=>{assert(e.origin==='null'&&e.source===window);seen.push(e.data)};
 postMessage(1);postMessage(2,'*');postMessage(3,{});postMessage(4,null);postMessage('opaque','file:///tmp/example.html');postMessage('data','data:text/plain,hi');
 for(const target of ['','null','/relative','https://[bad','x'.repeat(2049)]){let failed=false;try{postMessage(1,target)}catch(e){failed=e instanceof DOMException&&e.name==='SyntaxError'}assert(failed)}
 for(const run of [()=>postMessage(),()=>postMessage(1,Symbol('x')),()=>postMessage.call(document,1,'*')]){let failed=false;try{run()}catch(e){failed=e instanceof TypeError}assert(failed)}
 let failed=false;try{postMessage(()=>{},'https://other.test')}catch(e){failed=e.name==='DataCloneError'}assert(failed);
 const buffer=new ArrayBuffer(1);for(const run of [()=>postMessage(1,'*',[buffer]),()=>postMessage(1,{transfer:[buffer]})]){let failed=false;try{run()}catch(e){failed=e.name==='DataCloneError'}assert(failed&&buffer.byteLength===1)}
 `)
	for i := 0; i < 6; i++ {
		messageStep(t, s, h)
	}
	eventEval(t, s, `assert(seen.join()==='1,2,3,4')`)
}
func TestWindowPostMessageSharesQueueAndClosesWithPage(t *testing.T) {
	s, h := messageSandbox(t, Config{Location: "https://a.test/", MaxMessages: 1})
	eventEval(t, s, `
 const c=new MessageChannel();let seen=[];onmessage=e=>seen.push(e.data);c.port1.onmessage=e=>seen.push(e.data);
 postMessage('window','*');let failed=false;try{c.port2.postMessage('port')}catch(e){failed=e.name==='QuotaExceededError'}assert(failed);
 `)
	messageStep(t, s, h)
	eventEval(t, s, `c.port2.postMessage('port');failed=false;try{postMessage('window','*')}catch(e){failed=e.name==='QuotaExceededError'}assert(failed)`)
	messageStep(t, s, h)
	eventEval(t, s, `assert(seen.join()==='window,port');failed=false;try{postMessage({get value(){postMessage('inner','*');return 'outer'}},'*')}catch(e){failed=e.name==='QuotaExceededError'}assert(failed)`)
	messageStep(t, s, h)
	eventEval(t, s, `assert(seen.join()==='window,port,inner');postMessage('stale','*')`)
	s.Close()
	if len(h.tasks) != 0 || len(s.portMessages) != 0 || s.messageBytes != 0 {
		t.Fatal("closed page retained posted message")
	}
}
func TestWindowPostMessageOriginIsDocumentBound(t *testing.T) {
	s, h := messageSandbox(t, Config{Location: "https://a.test/start"})
	eventEval(t, s, `let received;document.domain='evil.test';onmessage=e=>received=e;postMessage({origin:'https://evil.test',source:'spoof'},'https://a.test')`)
	s.SetLocation("https://a.test/start#changed")
	messageStep(t, s, h)
	eventEval(t, s, `assert(received.origin==='https://a.test'&&received.source===window&&received.data.origin==='https://evil.test')`)
}
