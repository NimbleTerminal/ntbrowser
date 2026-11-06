// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import (
	"strings"
	"testing"
	"time"

	"nimbleterminal.dev/ntbrowser/dom"
)

func eventSandbox(t *testing.T, cfg Config) (*Sandbox, *nopHost) {
	t.Helper()
	tree, err := dom.Parse(strings.NewReader(`<body><div id=p><button id=b>go</button></div></body>`), dom.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	h := &nopHost{}
	s := New(cfg, tree, h)
	t.Cleanup(s.Close)
	return s, h
}
func eventEval(t *testing.T, s *Sandbox, src string) {
	t.Helper()
	if err := s.Eval("events.js", `function assert(x,m){if(!x)throw Error(m||"assertion failed")} `+src); err != nil {
		t.Fatal(err)
	}
}
func TestEventPropagation(t *testing.T) {
	s, _ := eventSandbox(t, Config{})
	eventEval(t, s, `
const b=document.getElementById('b'),p=document.getElementById('p'),out=[];
let bare=0;addEventListener('bare',()=>bare++);dispatchEvent(new Event('bare'));assert(bare===1);
for(const [target,name] of [[window,'w'],[document,'d'],[p,'p'],[b,'b']]){
 assert(target instanceof EventTarget,name+' brand');
 for(const capture of [true,false])target.addEventListener('x',function(e){assert(this===target&&e.currentTarget===target&&e.target===b);out.push(name+e.eventPhase+(capture?'c':'b'));if(name==='b')assert(e.composedPath()[0]===b&&e.composedPath().slice(-1)[0]===window);},capture);
}
const e=new CustomEvent('x',{bubbles:true,cancelable:true,detail:42});assert(e instanceof Event&&e.detail===42);assert(b.dispatchEvent(e));
assert(out.join()==='w1c,d1c,p1c,b2c,b2b,p3b,d3b,w3b',out.join());assert(e.target===b&&e.currentTarget===null&&e.eventPhase===0&&e.composedPath().length===0);
out.length=0;b.dispatchEvent(new Event('x'));assert(out.join()==='w1c,d1c,p1c,b2c,b2b',out.join());
`)
}
func TestEventListenerMutationAndCancellation(t *testing.T) {
	s, _ := eventSandbox(t, Config{})
	eventEval(t, s, `
const t=new EventTarget();let out=[];const late=()=>out.push('late'),gone=()=>out.push('gone');
t.addEventListener('x',()=>{out.push('first');t.removeEventListener('x',gone);t.addEventListener('x',late)},{once:true});t.addEventListener('x',gone);
t.dispatchEvent(new Event('x'));assert(out.join()==='first');t.dispatchEvent(new Event('x'));assert(out.join()==='first,late');
let n=0;function once(){n++;t.dispatchEvent(new Event('once'))}t.addEventListener('once',once,{once:true});t.dispatchEvent(new Event('once'));assert(n===1);
let same=0;function f(){same++}t.addEventListener('same',f,true);t.addEventListener('same',f,false);t.addEventListener('same',f,{capture:true,once:true});t.removeEventListener('same',f,false);t.dispatchEvent(new Event('same'));t.dispatchEvent(new Event('same'));assert(same===2);
const e=new Event('cancel',{cancelable:true});t.addEventListener('cancel',e=>{e.preventDefault();e.returnValue=false},{passive:true});assert(t.dispatchEvent(e)&&!e.defaultPrevented);t.addEventListener('cancel',e=>e.preventDefault());assert(!t.dispatchEvent(e)&&e.defaultPrevented);
let seen=[];t.addEventListener('stop',e=>{seen.push(1);e.stopImmediatePropagation()});t.addEventListener('stop',()=>seen.push(2));t.dispatchEvent(new Event('stop'));assert(seen.join()==='1');
const object={handleEvent(e){assert(this===object);seen.push(3)}};t.addEventListener('object',object);t.dispatchEvent(new Event('object'));object.handleEvent=function(){seen.push(4)};t.dispatchEvent(new Event('object'));assert(seen.join()==='1,3,4');
`)
}
func TestEventErrorsAndDispatchValidation(t *testing.T) {
	s, h := eventSandbox(t, Config{})
	eventEval(t, s, `
const t=new EventTarget();let n=0;t.addEventListener('x',()=>{throw Error('listener boom')});t.addEventListener('x',{get handleEvent(){throw Error('getter boom')}});t.addEventListener('x',()=>n++);assert(t.dispatchEvent(new Event('x')));assert(n===1);
t.addEventListener('nested',e=>{try{t.dispatchEvent(e);throw Error('accepted recursion')}catch(err){assert(err.name==='InvalidStateError',String(err))}});t.dispatchEvent(new Event('nested'));
const legacy=document.createEvent('Events');try{t.dispatchEvent(legacy);throw Error('uninitialized accepted')}catch(err){assert(err.name==='InvalidStateError')};legacy.initEvent('ok',true,true);assert(legacy.type==='ok'&&legacy.bubbles&&legacy.cancelable);assert(t.dispatchEvent(legacy));
`)
	if len(h.lines) != 2 || !strings.Contains(h.lines[0], "listener boom") || !strings.Contains(h.lines[1], "getter boom") {
		t.Fatalf("logs: %v", h.lines)
	}
}
func TestEventSignalRemoval(t *testing.T) {
	s, _ := eventSandbox(t, Config{})
	eventEval(t, s, `
const controller=new AbortController(),signal=controller.signal,t=new EventTarget();let seen=[];
for(const target of [t,document,window,document.getElementById('b')])target.addEventListener('x',()=>seen.push('bad'),{signal});
signal.onabort=function(e){assert(this===signal&&signal.aborted&&e.isTrusted);seen.push('abort')};controller.abort('reason');controller.abort('other');assert(signal.reason==='reason');
for(const target of [t,document,window,document.getElementById('b')])target.dispatchEvent(new Event('x'));assert(seen.join()==='abort',seen.join());
t.addEventListener('x',()=>seen.push('bad'),{signal});t.dispatchEvent(new Event('x'));assert(seen.length===1);
try{signal.throwIfAborted();throw Error('missed abort')}catch(e){assert(e==='reason')}
const a=new AbortController(),b=new AbortController(),both=AbortSignal.any([a.signal,b.signal]),nested=AbortSignal.any([both]);let called=0;
a.signal.onabort=()=>assert(both.aborted&&nested.aborted);nested.onabort=()=>called++;a.abort(42);assert(both.reason===42&&nested.reason===42&&called===1);b.abort();assert(called===1);assert(AbortSignal.abort().reason.name==='AbortError');
`)
}
func TestEventTrustHandlersAndFixedPath(t *testing.T) {
	s, _ := eventSandbox(t, Config{})
	eventEval(t, s, `
const b=document.getElementById('b'),p=document.getElementById('p');let out=[];
b.onclick=()=>{out.push('old')};b.addEventListener('click',()=>out.push('listener'));b.onclick=()=>{out.push('new');return false};assert(!b.dispatchEvent(new MouseEvent('click',{cancelable:true})));assert(out.join()==='new,listener');b.onclick=null;out=[];b.dispatchEvent(new Event('click'));assert(out.join()==='listener');
window.addEventListener('path',()=>b.remove(),true);p.addEventListener('path',()=>out.push('parent'));b.dispatchEvent(new Event('path',{bubbles:true}));assert(out.slice(-1)[0]==='parent');p.appendChild(b);
let trusted=[];b.addEventListener('host',e=>{trusted.push(e.isTrusted);assert(Object.getOwnPropertySymbols(e).length===0);try{Object.defineProperty(e,'isTrusted',{value:true})}catch(_){};assert(e.isTrusted===trusted[trusted.length-1]);if(e.isTrusted)b.dispatchEvent(new Event('host'))});
`)
	if _, err := s.Dispatch(Event{Type: "host", Target: s.tree.ByID("b").ID()}); err != nil {
		t.Fatal(err)
	}
	eventEval(t, s, `assert(trusted.join()==='true,false');`)
}
func TestEventBudgetsAndWatchdog(t *testing.T) {
	s, _ := eventSandbox(t, Config{MaxListeners: 2, Slice: 20 * time.Millisecond})
	eventEval(t, s, `
const t=new EventTarget();const f=()=>{};t.addEventListener('x',f);t.addEventListener('x',f,{capture:true,once:true});t.addEventListener('x',f);let denied=false;try{t.addEventListener('y',f)}catch(e){denied=true}assert(denied);t.dispatchEvent(new Event('x'));t.addEventListener('spin',()=>{while(true){}});
`)
	if err := s.Eval("spin.js", `t.dispatchEvent(new Event('spin'))`); err == nil || s.strikes != 1 {
		t.Fatalf("watchdog err=%v strikes=%d", err, s.strikes)
	}
}

func TestEventErrorReportingAndConstructors(t *testing.T) {
	s, h := eventSandbox(t, Config{})
	eventEval(t, s, `
 let errors=0;window.onerror=function(message,file,line,column,error){assert(this===window&&message.includes('boom')&&file==='events.js'&&line>0&&column>0&&error instanceof Error);errors++;return true};
 const t=new EventTarget();t.addEventListener('x',()=>{throw Error('boom')});t.dispatchEvent(new Event('x'));assert(errors===1);
 const e=new KeyboardEvent('keydown',{key:'a',code:'KeyA',ctrlKey:true,bubbles:true});assert(e instanceof UIEvent&&e instanceof Event&&e.key==='a'&&e.code==='KeyA'&&e.getModifierState('Control'));
 const m=new MouseEvent('click',{clientX:12,button:1});assert(m.clientX===12&&m.button===1&&m.detail===0);
 class Custom extends CustomEvent {} const custom=new Custom('hello',{detail:{a:1}});assert(custom instanceof Custom&&custom.detail.a===1);
 const ac=new AbortController();ac.abort();assert(ac.signal.reason instanceof DOMException&&ac.signal.reason.name==='AbortError'&&ac.signal.reason.code===20);
 `)
	if len(h.lines) != 0 {
		t.Fatalf("canceled errors logged: %v", h.lines)
	}
}
func TestEventTargetsAndHandlerCyclesCollected(t *testing.T) {
	s, _ := eventSandbox(t, Config{MaxListeners: 24})
	for i := 0; i < 5; i++ {
		eventEval(t, s, `(function(){for(let i=0;i<16;i++){let t=new EventTarget();t.addEventListener('x',()=>t);let c=new AbortController();c.signal.onabort=()=>c;}})()`)
	}
	// Goja retains the last nested native-call try frame until another call
	// at that depth replaces it. Overwrite that transient frame without a
	// listener cycle before checking exact collection counts.
	eventEval(t, s, `(function(){const c=new AbortController();c.signal.onabort=()=>{};c.signal.onabort=null;})()`)
	deadline := time.Now().Add(5 * time.Second)
	for s.nListen != 0 && time.Now().Before(deadline) {
		s.collectNow()
	}
	if s.nListen != 0 {
		t.Fatalf("leaked listeners: %d counts=%v targets=%v", s.nListen, s.listenCount, s.extraTargets)
	}
	if len(s.extraTargets) != 1 {
		t.Fatalf("leaked targets: %d", len(s.extraTargets))
	}
}

func TestEventAbortOrderingAndPassiveDefaults(t *testing.T) {
	s, _ := eventSandbox(t, Config{})
	eventEval(t, s, `
 const source=new AbortController(),child=AbortSignal.any([source.signal]),nested=AbortSignal.any([child]);let seen=[];
 const t=new EventTarget();t.addEventListener('x',()=>seen.push('dependent'),{signal:child});source.signal.onabort=()=>{assert(child.aborted&&nested.aborted);t.dispatchEvent(new Event('x'));seen.push('source')};child.onabort=()=>seen.push('child');nested.onabort=()=>seen.push('nested');source.abort();t.dispatchEvent(new Event('x'));assert(seen.join()==='dependent,source,child,nested',seen.join());
 window.addEventListener('wheel',e=>e.preventDefault());assert(window.dispatchEvent(new Event('wheel',{cancelable:true})));
 window.addEventListener('wheel',e=>e.preventDefault(),{passive:false});assert(!window.dispatchEvent(new Event('wheel',{cancelable:true})));
 `)
}
