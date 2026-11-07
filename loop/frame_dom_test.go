// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package loop

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"nimbleterminal.dev/ntbrowser/profile"
	"testing"
)

func TestSharedFrameDOMBidirectional(t *testing.T) {
	p := externalProfile
	p.Frames = profile.FramesOrigin
	p.FrameDOM = "same-origin"
	l, sink, f := scriptTestLoop(t, p, "https://example.test")
	child := `<p id=c>child</p><script>
 const d=parent.document;
 if(d!==parent.document||d.defaultView!==parent)throw Error('document identity');
 d.getElementById('p').textContent='from child';
 addEventListener('message',e=>{
  if(document.getElementById('c').textContent!=='from parent')throw Error('mutation not live');
  parent.postMessage('shared-ok','*');
 });
 parent.postMessage('ready','*');
 </script>`
	l.LoadPage(`<p id=p>parent</p><iframe id=f srcdoc="`+html.EscapeString(child)+`"></iframe><script>
 const f=document.getElementById('f');
 addEventListener('message',e=>{
  if(e.data==='shared-ok'){console.log(e.data);return}
  if(e.data!=='ready')return;
  if(document.getElementById('p').textContent!=='from child')throw Error('child write');
  const d=f.contentDocument;if(d!==f.contentWindow.document||d.defaultView!==f.contentWindow)throw Error('child document identity');
  const c=d.getElementById('c');if(c!==d.querySelector('#c')||c.ownerDocument!==d)throw Error('node identity');
  c.textContent='from parent';
  const span=d.createElement('span');span.id='new';span.appendChild(d.createTextNode('hello'));d.body.appendChild(span);
  if(d.querySelector('#new').textContent!=='hello'||!span.isConnected)throw Error('append');
  span.remove();if(span.isConnected||span.textContent!=='hello')throw Error('detach');
  d.body.appendChild(span);span.setAttribute('title','hello');if(span.getAttribute('title')!=='hello')throw Error('attribute');
  e.source.postMessage('check','*');
 });
 </script>`, "https://example.test/", f)
	waitLog(t, sink, "shared-ok")
}
func TestSharedDOMGateAndOpaqueOrigin(t *testing.T) {
	for _, gate := range []string{"off", "same-origin"} {
		for _, sandbox := range []string{"allow-scripts", "allow-scripts allow-same-origin"} {
			t.Run(gate+"/"+sandbox, func(t *testing.T) {
				p := externalProfile
				p.Frames = profile.FramesOrigin
				p.FrameDOM = gate
				l, sink, f := scriptTestLoop(t, p, "https://example.test")
				allow := gate == "same-origin" && sandbox == "allow-scripts allow-same-origin"
				child := fmt.Sprintf(`<p>child</p><script>let ok=false;try{ok=!!parent.document}catch(e){if(e.name!=='SecurityError')throw e}if(ok!==%v)throw Error('DOM gate');parent.postMessage('checked','*')</script>`, allow)
				l.LoadPage(`<iframe id=f sandbox="`+sandbox+`" srcdoc="`+html.EscapeString(child)+`"></iframe><script>addEventListener('message',()=>console.log('gate-ok'))</script>`, "https://example.test/", f)
				waitLog(t, sink, "gate-ok")
			})
		}
	}
}
func TestSharedDOMRevokedSynchronously(t *testing.T) {
	for _, change := range []string{`f.remove()`, `f.srcdoc='<p>replacement</p>'`, `f.sandbox='allow-scripts'`} {
		t.Run(change, func(t *testing.T) {
			p := externalProfile
			p.Frames = profile.FramesOrigin
			p.FrameDOM = "same-origin"
			l, sink, fetcher := scriptTestLoop(t, p, "https://example.test")
			l.LoadPage(`<iframe id=f srcdoc="<p id=c>child</p>"></iframe><script>
    const f=document.getElementById('f');let done=false;
    f.addEventListener('load',()=>{if(done)return;done=true;const d=f.contentDocument,c=d.getElementById('c');
    `+change+`;
    let denied=false;try{c.textContent='stale'}catch(e){denied=e.name==='SecurityError'}
    if(!denied)throw Error('stale write allowed');console.log('revoked');
    });
   </script>`, "https://example.test/", fetcher)
			waitLog(t, sink, "revoked")
		})
	}
}
func TestSharedDOMCannotInjectExecutableCode(t *testing.T) {
	p := externalProfile
	p.Frames = profile.FramesOrigin
	p.FrameDOM = "same-origin"
	l, sink, f := scriptTestLoop(t, p, "https://example.test")
	l.LoadPage(`<iframe id=f srcdoc="<p>child</p>"></iframe><script>
 const f=document.getElementById('f');f.addEventListener('load',()=>{
  const d=f.contentDocument;let denied=false;try{d.createElement('script')}catch(e){denied=e.name==='NotSupportedError'}if(!denied)throw Error('script creation');
  d.body.innerHTML='<p id=x>fragment</p><scr'+'ipt>parent.postMessage("EXECUTED","*")</scr'+'ipt>';
  if(d.getElementById('x').textContent!=='fragment')throw Error('fragment');
  d.body.appendChild(d.querySelector('script'));
  setTimeout(()=>console.log('inert-ok'),30);
 });
 addEventListener('message',()=>{throw Error('executed fragment script')});
 </script>`, "https://example.test/", f)
	waitLog(t, sink, "inert-ok")
}

func TestSharedDOMCrossOriginIsDenied(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<p>other origin</p><script>let denied=false;try{parent.document}catch(e){denied=e.name==='SecurityError'}if(!denied)throw Error('parent DOM leaked');parent.postMessage('cross-origin-checked','*')</script>`)
	}))
	defer server.Close()
	p := externalProfile
	p.Frames = profile.FramesAllowlist
	p.FrameDOM = "same-origin"
	p.FrameGrants = []profile.ScriptGrant{{Source: "http://parent.test", Destination: server.URL}}
	l, sink, f := scriptTestLoop(t, p, "http://parent.test")
	l.LoadPage(`<iframe id=f src="`+server.URL+`"></iframe><script>
 addEventListener('message',()=>{const f=document.getElementById('f');if(f.contentDocument!==null)throw Error('child DOM leaked');let denied=false;try{f.contentWindow.document}catch(e){denied=e.name==='SecurityError'}if(!denied)throw Error('proxy DOM leaked');console.log('cross-origin-ok')});
 </script>`, "http://parent.test/", f)
	waitLog(t, sink, "cross-origin-ok")
}
func TestSharedDOMRetainsDetachedNodesAndBoundsHandles(t *testing.T) {
	p := externalProfile
	p.Frames = profile.FramesOrigin
	p.FrameDOM = "same-origin"
	l, sink, f := scriptTestLoop(t, p, "https://example.test")
	child := `<p id=c>retained</p><script>addEventListener('message',()=>{document.body.innerHTML='<p>replacement</p>';parent.postMessage('detached','*')})</script>`
	l.LoadPage(`<iframe id=f srcdoc="`+html.EscapeString(child)+`"></iframe><script>
 const f=document.getElementById('f');let retained,d;
 f.addEventListener('load',()=>{d=f.contentDocument;retained=d.getElementById('c');f.contentWindow.postMessage('detach','*')});
 addEventListener('message',()=>{
  if(retained.isConnected||retained.textContent!=='retained')throw Error('lost retained node');
  d.body.appendChild(retained);
  let bounded=false;try{for(let i=0;i<300;i++)d.createElement('span')}catch(e){bounded=e.name==='QuotaExceededError'}
  if(!bounded)throw Error('unbounded handles');console.log('handles-ok');
 });
 </script>`, "https://example.test/", f)
	waitLog(t, sink, "handles-ok")
}
