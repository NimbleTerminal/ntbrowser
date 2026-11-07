// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
	"nimbleterminal.dev/ntbrowser/dom"
)

type messageHost struct {
	nopHost
	tasks map[int]goja.Callable
	order []int
	next  int
	deny  bool
}

func (h *messageHost) QueueTask(fn goja.Callable) (int, bool) {
	if h.deny {
		return 0, false
	}
	h.next++
	h.tasks[h.next] = fn
	h.order = append(h.order, h.next)
	return h.next, true
}
func (h *messageHost) ClearTask(id int) { delete(h.tasks, id) }
func messageSandbox(t *testing.T, cfg Config) (*Sandbox, *messageHost) {
	t.Helper()
	tree, _ := dom.Parse(strings.NewReader("<p id=x>x</p>"), dom.Limits{})
	h := &messageHost{tasks: map[int]goja.Callable{}}
	s := New(cfg, tree, h)
	t.Cleanup(s.Close)
	return s, h
}
func (h *messageHost) step(s *Sandbox) error {
	for len(h.order) > 0 {
		id := h.order[0]
		h.order = h.order[1:]
		if fn := h.tasks[id]; fn != nil {
			delete(h.tasks, id)
			return s.Call(fn, goja.Undefined())
		}
	}
	return nil
}
func messageStep(t *testing.T, s *Sandbox, h *messageHost) {
	t.Helper()
	if err := h.step(s); err != nil {
		t.Fatal(err)
	}
}

func TestMessageChannelStartOrderingAndClose(t *testing.T) {
	s, h := messageSandbox(t, Config{})
	eventEval(t, s, `
 const channel=new MessageChannel(),seen=[];assert(channel.port1 instanceof MessagePort&&channel.port1 instanceof EventTarget);
 channel.port1.addEventListener('message',function(e){assert(this===channel.port1&&e instanceof MessageEvent&&e.target===this&&e.isTrusted&&!e.cancelable&&!e.bubbles);assert(e.origin===''&&e.source===null&&Object.isFrozen(e.ports)&&e.ports.length===0);seen.push(e.data);Promise.resolve().then(()=>seen.push('micro'))});
 channel.port2.postMessage(1);channel.port2.postMessage(2);assert(seen.length===0);
 `)
	if len(h.tasks) != 0 {
		t.Fatal("unstarted port scheduled")
	}
	eventEval(t, s, `channel.port1.start();`)
	messageStep(t, s, h)
	eventEval(t, s, `assert(seen.join()==='1,micro');channel.port1.close();channel.port2.postMessage(3);`)
	messageStep(t, s, h)
	eventEval(t, s, `assert(seen.join()==='1,micro,2,micro');`)
	if len(s.portMessages) != 0 || s.messageBytes != 0 {
		t.Fatal("queue not drained")
	}
}
func TestMessageChannelHandlerStartsAndYields(t *testing.T) {
	s, h := messageSandbox(t, Config{})
	eventEval(t, s, `
 const c=new MessageChannel();let count=0;c.port1.onmessage=()=>{count++;if(count<3)c.port2.postMessage('again')};c.port2.postMessage('first');assert(count===0);
 `)
	for i := 1; i <= 3; i++ {
		messageStep(t, s, h)
		if got := s.vm.Get("count").ToInteger(); got != int64(i) {
			t.Fatalf("delivered batch: %d", got)
		}
	}
	eventEval(t, s, `const d=new MessageChannel();let n=0;d.port1.addEventListener('message',()=>n++);d.port2.postMessage(0);d.port1.onmessage=null;`)
	messageStep(t, s, h)
	eventEval(t, s, `assert(n===1)`)
}
func TestMessageChannelStructuredCopy(t *testing.T) {
	s, h := messageSandbox(t, Config{})
	eventEval(t, s, `
 const c=new MessageChannel();let received; c.port1.onmessage=e=>received=e.data;
 const buffer=new Uint8Array([0,1,2,3]).buffer,view=new Uint8Array(buffer,1,2),data=new DataView(buffer,2,1);
 const shared={value:1},payload={shared,again:shared,view,data,buffer,date:new Date(1234),map:new Map(),set:new Set(),array:[,undefined,NaN,-0,Infinity]};payload.self=payload;payload.map.set(payload,shared);payload.set.add(payload);Object.defineProperty(payload,'__proto__',{value:{safe:true},enumerable:true});
 c.port2.postMessage(payload);shared.value=9;view[0]=99;payload.array[1]='changed';
 `)
	messageStep(t, s, h)
	eventEval(t, s, `
 assert(received!==payload&&received.self===received&&received.shared===received.again&&received.shared.value===1);
 assert(received.view instanceof Uint8Array&&received.view.byteOffset===1&&received.view.length===2&&received.view[0]===1&&received.buffer===received.view.buffer&&received.data.buffer===received.buffer);
 assert(received.date.getTime()===1234&&received.map.get(received)===received.shared&&received.set.has(received));
 assert(!(0 in received.array)&&received.array[1]===undefined&&Number.isNaN(received.array[2])&&Object.is(received.array[3],-0)&&received.array[4]===Infinity);
 assert(Object.getPrototypeOf(received)===Object.prototype&&Object.hasOwn(received,'__proto__')&&received.__proto__.safe);
 `)
}
func TestMessageChannelRejectsCapabilitiesAndTransfers(t *testing.T) {
	s, h := messageSandbox(t, Config{})
	eventEval(t, s, `
 const c=new MessageChannel();let count=0;c.port1.onmessage=()=>count++;
 for(const data of [()=>{},Symbol('x'),{fn(){}},document,document.getElementById('x'),c.port1,new TextEncoder(),new WeakMap(),Promise.resolve(),123n,new Proxy({},{}),/x/]){let rejected=false;try{c.port2.postMessage(data)}catch(e){rejected=e.name==='DataCloneError'}assert(rejected, 'accepted '+Object.prototype.toString.call(data))}
 const b=new ArrayBuffer(8);for(const transfer of [[b],{transfer:[b]},[c.port1]]){let rejected=false;try{c.port2.postMessage('x',transfer)}catch(e){rejected=e.name==='DataCloneError'}assert(rejected&&b.byteLength===8)}
 c.port2.postMessage('good',[]);c.port2.postMessage('good',{transfer:[]});
 `)
	messageStep(t, s, h)
	messageStep(t, s, h)
	eventEval(t, s, `assert(count===2)`)
}
func TestMessageChannelQuotasAndReentrantSerialization(t *testing.T) {
	s, h := messageSandbox(t, Config{MaxMessages: 1, MaxMessagePorts: 4, MaxMessageBytes: 256})
	eventEval(t, s, `
 const c=new MessageChannel();let got=[];c.port1.onmessage=e=>got.push(e.data);
 let rejected=false;try{c.port2.postMessage({get value(){c.port2.postMessage('inner');return 'outer'}})}catch(e){rejected=e.name==='QuotaExceededError'}assert(rejected);
 `)
	if len(s.portMessages) != 1 {
		t.Fatalf("overfull queue: %d", len(s.portMessages))
	}
	messageStep(t, s, h)
	eventEval(t, s, `assert(got.join()==='inner');rejected=false;try{c.port2.postMessage('x'.repeat(200))}catch(e){rejected=e.name==='QuotaExceededError'}assert(rejected);c.port2.postMessage('ok');`)
	messageStep(t, s, h)
	h.deny = true
	eventEval(t, s, `let denied=false;try{c.port2.postMessage('denied')}catch(e){denied=e.name==='QuotaExceededError'}assert(denied)`)
	if s.messageBytes != 0 || len(s.portMessages) != 0 {
		t.Fatal("failed task retained message")
	}
	h.deny = false
	eventEval(t, s, `const second=new MessageChannel();denied=false;try{new MessageChannel()}catch(e){denied=e.name==='QuotaExceededError'}assert(denied)`)
}
func TestMessageChannelLifetimeAndShutdown(t *testing.T) {
	s, h := messageSandbox(t, Config{MaxMessagePorts: 8})
	for i := 0; i < 10; i++ {
		eventEval(t, s, `(function(){const c=new MessageChannel();c.port1.onmessage=()=>c;c.port1.close();c.port2.close()})()`)
		s.collectNow()
	}
	eventEval(t, s, `
 let port;let got=0;(function(){const c=new MessageChannel();port=c.port2;c.port1.onmessage=()=>got++})();port.postMessage(1);
 `)
	s.collectNow()
	messageStep(t, s, h)
	eventEval(t, s, `assert(got===1);port.postMessage(2);`)
	if len(h.tasks) != 1 {
		t.Fatal("missing pending task")
	}
	s.Close()
	if len(h.tasks) != 0 || s.messagePorts != nil || s.portMessages != nil || s.cloneMessage != nil {
		t.Fatal("Close retained channel resources")
	}
}
func TestMessageChannelWatchdog(t *testing.T) {
	s, h := messageSandbox(t, Config{Slice: 20 * time.Millisecond})
	eventEval(t, s, `const c=new MessageChannel();c.port1.onmessage=()=>{for(;;){}};c.port2.postMessage(1);`)
	err := h.step(s)
	if err == nil || s.strikes != 1 {
		t.Fatalf("watchdog: %v strikes=%d", err, s.strikes)
	}
}

func TestMessageChannelCloneLimitsAndDetachedBuffer(t *testing.T) {
	s, _ := messageSandbox(t, Config{})
	eventEval(t, s, `const c=new MessageChannel();for(const make of [()=>new ArrayBuffer(1048576),()=>new Array(5000),()=>{let x={};for(let i=0;i<70;i++)x={x};return x}]){let rejected=false;try{c.port1.postMessage(make())}catch(e){rejected=e.name==='QuotaExceededError'}assert(rejected)};`)
	ab := s.vm.NewArrayBuffer([]byte{1, 2})
	_ = s.vm.Set("buffer", ab)
	ab.Detach()
	eventEval(t, s, `let rejected=false;try{c.port1.postMessage(buffer)}catch(e){rejected=e.name==='DataCloneError'}assert(rejected)`)
	if len(s.portMessages) != 0 || s.messageBytes != 0 {
		t.Fatal("failed clone retained queue charge")
	}
}
