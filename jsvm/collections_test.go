// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package jsvm

import (
	"runtime"
	"testing"
)

func TestLiveTagCollections(t *testing.T) {
	s, _, _ := formSandbox(t, `<div id="root"><p id="first" name="named">one</p></div><p>outside</p>`)
	defer s.Close()
	imageEval(t, s, `let root=document.getElementById("root"), list=root.getElementsByTagName("P");if(!(list instanceof HTMLCollection)||list.length!==1||list[0]!==list.item(0)||list.namedItem("named")!==list[0]||list.first!==list[0]||list.item(5)!==null||list[5]!==undefined||list.namedItem("")!==null)throw Error("collection interface");let p=document.createElement("p");p.id="second";root.appendChild(p);if(list.length!==2||list[1]!==p||Array.from(list).length!==2)throw Error("collection is stale");list[0].remove();if(list.length!==1||list[0]!==p)throw Error("removed element retained");if(root.getElementsByTagName("div").length!==0||document.getElementsByTagName("*")[0]!==document.documentElement)throw Error("scope or wildcard");if(root.getElementsByTagName("p,div").length!==0)throw Error("tag treated as selector");root.remove();root=null;p=null;`)
	runtime.GC()
	imageEval(t, s, `if(list.length!==1||list[0].id!=="second")throw Error("detached collection lost subtree");list[0].remove();if(list.length!==0)throw Error("detached collection stale");`)
}

func TestParsedScriptsDiscoverableAndNeverReplayed(t *testing.T) {
	s, tree, _ := formSandbox(t, `<script nonce="fixture">window.original=1</script><script type="application/json">{"data":true}</script><p>end</p>`)
	defer s.Close()
	s.cfg.DynamicScripts = true
	s.cfg.AllowDynamicCode = true
	if len(tree.Scripts) != 1 {
		t.Fatal("data block scheduled")
	}
	imageEval(t, s, `let scripts=document.getElementsByTagName("script");if(scripts.length!==2||scripts[0].nonce!=="fixture"||scripts[0].text!=="window.original=1")throw Error("parsed scripts missing");let original=scripts[0];original.remove();document.body.appendChild(original);original.text="window.replayed=true";if(typeof replayed!=="undefined"||typeof original!=="object")throw Error("parsed script replayed");`)
}
