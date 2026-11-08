// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"strings"
	"testing"
)

// TestCryptoGetRandomValuesDrawsFromHostSource: crypto.getRandomValues fills
// integer views in place from the sandbox's host-replaceable random source
// (design decision 10), so a replay with an injected source is deterministic.
func TestCryptoGetRandomValuesDrawsFromHostSource(t *testing.T) {
	calls := 0
	s, _ := eventSandbox(t, Config{Rand: func() float64 { calls++; return float64(calls) / 256 }})
	eventEval(t, s, `
 assert(typeof crypto==='object'&&crypto===globalThis.crypto&&typeof crypto.getRandomValues==='function');
 assert(crypto.subtle===undefined);
 const a=new Uint8Array(6);assert(crypto.getRandomValues(a)===a);
 assert(a.some(b=>b!==0));
 const backing=new Uint8Array(8).fill(7),view=backing.subarray(2,6);crypto.getRandomValues(view);
 assert(backing[0]===7&&backing[1]===7&&backing[6]===7&&backing[7]===7&&Array.from(view).some(b=>b!==7));
 for(const ok of [new Int8Array(3),new Uint8ClampedArray(3),new Int16Array(3),new Uint16Array(3),new Int32Array(3),new Uint32Array(3),new BigInt64Array(2),new BigUint64Array(2)]){assert(crypto.getRandomValues(ok)===ok)}
 assert(crypto.getRandomValues(new Uint8Array(0)).length===0);
 for(const bad of [new Float32Array(2),new Float64Array(2),new DataView(new ArrayBuffer(4)),new ArrayBuffer(4),[1,2],{},null]){
   let name='';try{crypto.getRandomValues(bad)}catch(err){name=err instanceof DOMException?err.name:err.constructor.name}
   assert(name==='TypeMismatchError'||name==='TypeError',String(bad)+': '+name)
 }
 let quota='';try{crypto.getRandomValues(new Uint8Array(65537))}catch(err){quota=err.name}assert(quota==='QuotaExceededError');
 crypto.getRandomValues(new Uint8Array(65536));
 const u=crypto.randomUUID();assert(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(u),u);
 assert(crypto.randomUUID()!==u);
 let frozen=true;try{crypto.getRandomValues=1;frozen=crypto.getRandomValues!==1}catch(e){}assert(frozen);
 `)
	if calls == 0 {
		t.Fatal("crypto did not draw from the injected source")
	}

	// The same injected sequence yields the same bytes: replays are deterministic.
	seq := func() string {
		n := 0
		s, _ := eventSandbox(t, Config{Rand: func() float64 { n++; return float64(n%97) / 97 }})
		eventEval(t, s, `globalThis.out=Array.from(crypto.getRandomValues(new Uint8Array(32))).join()+' '+crypto.randomUUID()`)
		return s.vm.Get("out").String()
	}
	a, b := seq(), seq()
	if a != b || !strings.Contains(a, "-4") {
		t.Fatalf("replay differs:\n%s\n%s", a, b)
	}
}

// TestGoWasmLoaderPreflightPasses runs the environment checks at the top of
// Go's wasm_exec.js, which gloss's web terminal loads as a classic script.
func TestGoWasmLoaderPreflightPasses(t *testing.T) {
	s, _ := eventSandbox(t, Config{})
	eventEval(t, s, `
 if (!globalThis.crypto) throw new Error("globalThis.crypto is not available, polyfill required (crypto.getRandomValues only)");
 if (!globalThis.performance) throw new Error("globalThis.performance is not available, polyfill required (performance.now only)");
 if (!globalThis.TextEncoder) throw new Error("globalThis.TextEncoder is not available, polyfill required");
 if (!globalThis.TextDecoder) throw new Error("globalThis.TextDecoder is not available, polyfill required");
 const encoder = new TextEncoder("utf-8"); const decoder = new TextDecoder("utf-8");
 globalThis.Go = class { constructor() { this.argv = ["js"]; crypto.getRandomValues(new Uint8Array(8)); } };
 new Go();
 `)
}
