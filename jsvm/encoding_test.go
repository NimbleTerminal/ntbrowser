// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"testing"

	"github.com/dop251/goja"
)

func TestTextEncoderUnicodeAndEncodeInto(t *testing.T) {
	s, _ := eventSandbox(t, Config{})
	eventEval(t, s, `
 const e=new TextEncoder();assert(e.encoding==='utf-8'&&e.encode() instanceof Uint8Array&&e.encode().length===0);
 assert(Array.from(e.encode('Aé😀\uD800Z\uDC00')).join()==='65,195,169,240,159,152,128,239,191,189,90,239,191,189');
 assert(Array.from(e.encode(null)).join()==='110,117,108,108');
 const backing=new Uint8Array(8).fill(99),dst=backing.subarray(2,7);
 let r=e.encodeInto('😀é',dst);assert(r.read===2&&r.written===4);assert(Array.from(backing).join()==='99,99,240,159,152,128,99,99');
 r=e.encodeInto('\uD800',new Uint8Array(2));assert(r.read===0&&r.written===0);
 r=e.encodeInto('\uD800',new Uint8Array(3));assert(r.read===1&&r.written===3);
 for(const bad of [[],new Uint8ClampedArray(4),new Int8Array(4),new DataView(new ArrayBuffer(4)),new Proxy(new Uint8Array(4),{})]){let rejected=false;try{e.encodeInto('a',bad)}catch(err){rejected=err instanceof TypeError}assert(rejected)}
 let rejected=false;try{e.encode(Symbol('x'))}catch(err){rejected=err instanceof TypeError}assert(rejected);
 class Encoder extends TextEncoder{};assert(new Encoder().encode('x')[0]===120);
 `)
}
func TestTextDecoderViewsStreamingAndErrors(t *testing.T) {
	s, _ := eventSandbox(t, Config{})
	eventEval(t, s, `
 const d=new TextDecoder();assert(d.encoding==='utf-8'&&!d.fatal&&!d.ignoreBOM&&d.decode()==='');
 const raw=new Uint8Array([0,65,66,0]);assert(d.decode(new DataView(raw.buffer,1,2))==='AB');assert(d.decode(raw.subarray(1,3))==='AB');assert(d.decode(new Uint16Array(raw.buffer,0,2))==='\0AB\0');
 assert(d.decode(new Uint8Array([0xE1,0x80,65]))==='\uFFFDA');
 assert(d.decode(new Uint8Array([0xED,0xA0,0x80]))==='\uFFFD\uFFFD\uFFFD');
 assert(d.decode(new Uint8Array([0xF0,0x9F,0x98]),{stream:true})==='');assert(d.decode(new Uint8Array([0x80]))==='😀');
 assert(d.decode(new Uint8Array([0xE1,0x80]),{stream:true})==='');assert(d.decode()==='\uFFFD');assert(d.decode()==='');
 const bom=new Uint8Array([239,187,191,65]);assert(d.decode(bom)==='A');assert(d.decode(bom)==='A');assert(new TextDecoder('utf8',{ignoreBOM:true}).decode(bom)==='\uFEFFA');
 assert(d.decode(bom.subarray(0,1),{stream:true})==='');assert(d.decode(bom.subarray(1,2),{stream:true})==='');assert(d.decode(bom.subarray(2))==='A');
 const fatal=new TextDecoder('utf-8',{fatal:true});let rejected=false;try{fatal.decode(new Uint8Array([255]))}catch(e){rejected=e instanceof TypeError};assert(rejected);assert(fatal.decode(new Uint8Array([65]))==='A');
 for(const bad of [null,[],{},'abc',new Proxy(raw,{})]){let rejected=false;try{d.decode(bad)}catch(e){rejected=e instanceof TypeError};assert(rejected)}
 for(const label of ['bogus','replacement','windows-1252']){let rejected=false;try{new TextDecoder(label)}catch(e){rejected=e instanceof RangeError};assert(rejected)}
 `)
}
func TestTextDecoderUTF16AndEveryChunkBoundary(t *testing.T) {
	s, _ := eventSandbox(t, Config{})
	eventEval(t, s, `
 for(const [label,bytes,text] of [
 ['utf-8',[239,187,191,65,240,159,152,128,225,128,66],'A😀\uFFFDB'],
 ['utf-16le',[255,254,65,0,61,216,0,222,0,216,66,0,77],'A😀\uFFFDB\uFFFD'],
 ['utf-16be',[254,255,0,65,216,61,222,0,216,0,0,66,77],'A😀\uFFFDB\uFFFD']]){
  const all=new Uint8Array(bytes);
  for(let i=0;i<=all.length;i++){const d=new TextDecoder(label);const got=d.decode(all.subarray(0,i),{stream:true})+d.decode(all.subarray(i));assert(got===text,label+':'+i+':'+got)}
 }
 assert(new TextDecoder('utf-16le').decode(new Uint8Array([0,216,65]))==='\uFFFD');
 assert(new TextDecoder('  UTF-16\n').encoding==='utf-16le');
 assert(new TextDecoder('utf-16be',{ignoreBOM:true}).decode(new Uint8Array([254,255]))==='\uFEFF');
 `)
}
func TestTextCodecLimitsAndIntrinsicBufferAccess(t *testing.T) {
	s, _ := eventSandbox(t, Config{MaxCodecBytes: 8})
	eventEval(t, s, `
 const e=new TextEncoder(),d=new TextDecoder();
 for(const f of [()=>e.encode('123456789'),()=>e.encode('😀😀😀'),()=>d.decode(new Uint8Array(9)),()=>d.decode(new Uint8Array([255,255,255]))]){let rejected=false;try{f()}catch(err){rejected=err instanceof RangeError}assert(rejected)}
 const dst=new Uint8Array(12).fill(7);let capped=false;try{e.encodeInto('ééééé',dst)}catch(err){capped=err instanceof RangeError}assert(capped&&dst.every(x=>x===7));
 const a=new Uint8Array([65,66]);Object.defineProperty(a,'buffer',{get(){throw Error('spoofed')}});Object.defineProperty(a,'byteLength',{value:999});Object.defineProperty(a,'byteOffset',{value:999});assert(d.decode(a)==='AB');assert(e.encodeInto('Z',a).written===1&&a[0]===90);
 let rejected=false;try{TextEncoder.prototype.encode.call({},'x')}catch(err){rejected=err instanceof TypeError}assert(rejected);
 assert(d.decode(new Uint8Array([65]))==='A');
 `)
}
func TestTextCodecsRejectDetachedBuffers(t *testing.T) {
	s, _ := eventSandbox(t, Config{})
	ab := s.vm.NewArrayBuffer([]byte{65, 66, 67})
	_ = s.vm.Set("buf", ab)
	eventEval(t, s, `const view=new Uint8Array(buf);const data=new DataView(buf);`)
	ab.Detach()
	eventEval(t, s, `for(const run of [()=>new TextDecoder().decode(buf),()=>new TextDecoder().decode(view),()=>new TextDecoder().decode(data),()=>new TextEncoder().encodeInto('x',view)]){let rejected=false;try{run()}catch(e){rejected=e instanceof TypeError}assert(rejected)}`)
	// A decode options getter may detach its input after initial argument checks.
	other := s.vm.NewArrayBuffer([]byte{65})
	_ = s.vm.Set("other", other)
	_ = s.vm.Set("detach", func(goja.FunctionCall) goja.Value { other.Detach(); return goja.Undefined() })
	eventEval(t, s, `let rejected=false;try{new TextDecoder().decode(other,{get stream(){detach();return false}})}catch(e){rejected=e instanceof TypeError}assert(rejected)`)
}
