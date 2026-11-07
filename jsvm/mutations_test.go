// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import (
	"runtime"
	"testing"
	"time"
)

func TestMutationObserverRecordsAndOrdering(t *testing.T) {
	s, _ := eventSandbox(t, Config{})
	eventEval(t, s, `
 const el=document.getElementById('b');let order=[],records=[];
 const obs=new MutationObserver(function(rs,o){assert(this===obs&&o===obs);records.push(...rs);order.push('observer')});
 obs.observe(el,{attributeOldValue:true,attributeFilter:['style']});
 Promise.resolve().then(()=>order.push('before'));
 el.style.width='10px';el.style.width='20px';el.setAttribute('ignored','yes');
 Promise.resolve().then(()=>order.push('after'));
 assert(records.length===0);
 `)
	eventEval(t, s, `
 assert(order.join(',')==='before,observer,after',order);
 assert(records.length===2 && records[0].target===el);
 assert(records[0].oldValue===null && records[1].oldValue==='width: 10px;');
 assert(records[0].type==='attributes' && records[0].attributeName==='style');
 assert(records[0].attributeNamespace===null && records[0].addedNodes.length===0);
 assert(records[0].removedNodes.item(0)===null);
 obs.disconnect();el.style.width='30px';assert(obs.takeRecords().length===0);
 `)
}

func TestMutationObserverSubtreesAndAtomicFragments(t *testing.T) {
	s, _ := eventSandbox(t, Config{})
	eventEval(t, s, `
 const p=document.getElementById('p'),b=document.getElementById('b');let seen=[];
 const obs=new MutationObserver(rs=>seen.push(...rs));
 obs.observe(p,{childList:true,subtree:true,attributes:true,characterDataOldValue:true});
 b.firstChild.data='changed';
 p.removeChild(b);b.setAttribute('detached','yes');
 const pending=obs.takeRecords();
 assert(pending.length===3);
 assert(pending[0].type==='characterData'&&pending[0].oldValue==='go');
 assert(pending[1].removedNodes[0]===b&&[...pending[1].removedNodes][0]===b);
 assert(pending[2].target===b&&pending[2].attributeName==='detached');
 p.innerHTML='<i>one</i><span>two</span>';
 const r=obs.takeRecords();assert(r.length===1&&r[0].addedNodes.length===2&&r[0].removedNodes.length===0);
 const before=p.innerHTML;try{p.innerHTML='x'.repeat(65537)}catch(e){}
 assert(p.innerHTML===before && obs.takeRecords().length===0);
 `)
	eventEval(t, s, `b.setAttribute('later','no');assert(obs.takeRecords().length===0);assert(seen.length===0);`)
}

func TestMutationObserverValidationAndPrivateScheduler(t *testing.T) {
	s, _ := eventSandbox(t, Config{})
	eventEval(t, s, `
 const el=document.getElementById('b');let calls=0;
 const o=new MutationObserver(()=>calls++);
 for(const opts of [{},{attributes:false,attributeOldValue:true},{characterData:false,characterDataOldValue:true}]){
   let threw=false;try{o.observe(el,opts)}catch(e){threw=e instanceof TypeError}assert(threw);
 }
 o.observe(el,{attributes:true});
 Promise.prototype.then=function(){throw Error('tampered then')};
 Object.defineProperty(Promise,Symbol.species,{get(){throw Error('tampered species')}});
 el.setAttribute('a','1');
 `)
	eventEval(t, s, `assert(calls===1);o.disconnect();`)
}

func TestMutationObserverHostCheckpoint(t *testing.T) {
	s, _ := eventSandbox(t, Config{})
	eventEval(t, s, `let calls=0;new MutationObserver(()=>calls++).observe(document.getElementById('b'),{attributes:true});`)
	n := s.tree.ByID("b")
	if err := s.tree.SetAttr(n, "host", "yes"); err != nil {
		t.Fatal(err)
	}
	if s.mutationRecords != 1 {
		t.Fatal("host mutation was not queued")
	}
	if err := s.FlushMutations(); err != nil {
		t.Fatal(err)
	}
	eventEval(t, s, `assert(calls===1);`)
}

func TestMutationObserverBounds(t *testing.T) {
	s, _ := eventSandbox(t, Config{Slice: time.Second})
	eventEval(t, s, `
 const el=document.getElementById('b');let calls=0;
 new MutationObserver(()=>calls++).observe(el,{attributes:true});
 for(let i=0;i<1025;i++)el.setAttribute('a',String(i));
 assert(el.getAttribute('a')==='1024');
 `)
	if !s.mutationStopped || s.mutationRecords != 0 || len(s.mutationPins) != 0 {
		t.Fatal("overflow did not release observer resources")
	}
	eventEval(t, s, `assert(calls===0);let denied=false;try{new MutationObserver(()=>{}).observe(el,{attributes:true})}catch(e){denied=e.name==='QuotaExceededError'}assert(denied);`)
}

func TestMutationObserverCallbackStorm(t *testing.T) {
	s, _ := eventSandbox(t, Config{Slice: time.Second})
	eventEval(t, s, `
 const el=document.getElementById('b');let calls=0;
 new MutationObserver(()=>{calls++;el.setAttribute('x',String(calls))}).observe(el,{attributes:true});
 el.setAttribute('x','0');
 `)
	if !s.mutationStopped || s.mutationRecords != 0 {
		t.Fatal("callback storm escaped limit")
	}
	eventEval(t, s, `assert(calls===128,calls);`)
}

func TestMutationObserverWatchdogAndRecovery(t *testing.T) {
	s, _ := eventSandbox(t, Config{Slice: 20 * time.Millisecond, Strikes: 3})
	err := s.Eval("observer.js", `
 const el=document.getElementById('b');let delivered=0;
 const slow=new MutationObserver(()=>{while(true){}});
 slow.observe(el,{attributes:true});
 new MutationObserver(()=>delivered++).observe(el,{attributes:true});
 el.setAttribute('x','1');
 `)
	if err == nil || s.strikes != 1 {
		t.Fatalf("observer escaped watchdog: %v; strikes %d", err, s.strikes)
	}
	eventEval(t, s, `slow.disconnect();`)
	eventEval(t, s, `assert(delivered===1,delivered);`)
}

func TestMutationObserverExceptionDoesNotStopOthers(t *testing.T) {
	s, _ := eventSandbox(t, Config{})
	eventEval(t, s, `
 const el=document.getElementById('b');let delivered=0,errors=0;
 addEventListener('error',e=>{errors++;e.preventDefault()});
 new MutationObserver(()=>{throw Error('observer failure')}).observe(el,{attributes:true});
 new MutationObserver(()=>delivered++).observe(el,{attributes:true});
 el.setAttribute('x','1');
 `)
	eventEval(t, s, `assert(delivered===1&&errors===1);`)
}

func TestMutationObserverActiveAndRegistrationLimits(t *testing.T) {
	s, _ := eventSandbox(t, Config{Slice: time.Second})
	eventEval(t, s, `
 const all=[];for(let i=0;i<64;i++){const o=new MutationObserver(()=>{});o.observe(document,{subtree:true,attributes:true});all.push(o)}
 let denied=false;try{new MutationObserver(()=>{}).observe(document,{attributes:true})}catch(e){denied=e.name==='QuotaExceededError'}assert(denied);
 all[0].disconnect();new MutationObserver(()=>{}).observe(document,{attributes:true});
 all.forEach(o=>o.disconnect());
 const many=new MutationObserver(()=>{});let nodes=[];
 for(let i=0;i<255;i++){let n=document.createElement('div');nodes.push(n);many.observe(n,{attributes:true})}
 denied=false;try{many.observe(document.createElement('div'),{attributes:true})}catch(e){denied=e.name==='QuotaExceededError'}assert(denied);
 many.disconnect();
 `)
}

func TestMutationObserverPinsDetachedTargets(t *testing.T) {
	s, _ := eventSandbox(t, Config{})
	eventEval(t, s, `
 let observedID;
 const observer=new MutationObserver(()=>{});
 (()=>{const n=document.createElement('div');n.id='kept';document.body.appendChild(n);observedID=n;
 observer.observe(n,{attributes:true});n.remove();observedID=null})();
 `)
	// The observer owns the registration even when the page drops its wrapper.
	runtime.GC()
	s.Reap()
	if len(s.mutationPins) != 1 {
		t.Fatal("registration pin lost")
	}
	for id := range s.mutationPins {
		if s.tree.Node(id) == nil {
			t.Fatal("observed detached node reaped")
		}
	}
	eventEval(t, s, `observer.disconnect();`)
	if len(s.mutationPins) != 0 {
		t.Fatal("disconnect leaked node pins")
	}
}
