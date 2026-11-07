// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import (
	"encoding/json"
	"testing"
)

func TestFrameProxyIsolationAndWireData(t *testing.T) {
	var messages []FramePost
	s, _ := eventSandbox(t, Config{FrameSelf: 2, FrameParent: 1, FrameTop: 1, PostFrame: func(m FramePost) error { messages = append(messages, m); return nil }})
	eventEval(t, s, `
 assert(parent===top&&parent!==window&&parent.self===parent);
 let denied=false;try{parent.document}catch(e){denied=e.name==='SecurityError'}assert(denied);
 Object.prototype.toJSON=()=>{throw Error('toJSON must not run')};
 parent.postMessage({answer:42,a:[true,'ok',null]},'*');parent.postMessage(null,'*');parent.postMessage('text','*');
 for(const v of [undefined,NaN,Infinity,new Map(),1n]){
 let fail=false;try{parent.postMessage(v,'*')}catch(e){fail=e.name==='DataCloneError'}assert(fail);
 }
 const cycle={};cycle.x=cycle;let fail=false;try{parent.postMessage(cycle,'*')}catch(e){fail=e.name==='DataCloneError'}assert(fail);
 `)
	if len(messages) != 3 {
		t.Fatalf("messages: %d", len(messages))
	}
	var m map[string]any
	if err := json.Unmarshal(messages[0].Data, &m); err != nil || m["answer"] != float64(42) {
		t.Fatalf("data %s: %v", messages[0].Data, err)
	}
	if string(messages[1].Data) != "null" || string(messages[2].Data) != `"text"` {
		t.Fatal("primitive messages")
	}
}
