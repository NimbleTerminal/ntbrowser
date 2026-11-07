// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package loop

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLateScriptReadinessAndDocumentLoad(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				if fail {
					http.Error(w, "denied", http.StatusForbidden)
					return
				}
				w.Header().Set("Content-Type", "text/javascript")
				fmt.Fprint(w, `function init(){document.getElementById('result').textContent='initialized';console.log('late:'+document.readyState)}
  if(document.readyState==='interactive'||document.readyState==='complete')init();else window.addEventListener('DOMContentLoaded',init);`)
			}))
			defer server.Close()
			l, sink, f := scriptTestLoop(t, externalProfile, server.URL)
			l.LoadPage(`<p id=result>waiting</p><script>
 if(document.readyState!=='loading')throw Error('initial state');
 window.addEventListener('DOMContentLoaded',()=>console.log('dom:'+document.readyState));
 window.addEventListener('load',e=>{if(e.target!==document||e.currentTarget!==window)throw Error('load target');console.log('load:'+document.readyState)});
 const s=document.createElement('script');s.src='/late.js';s.onerror=()=>console.log('failed:'+document.readyState);document.head.appendChild(s);
 </script>`, server.URL+"/", f)
			waitLog(t, sink, "dom:interactive")
			close(release)
			if fail {
				waitLog(t, sink, "failed:interactive")
			} else {
				waitLog(t, sink, "late:interactive")
			}
			waitLog(t, sink, "load:complete")
		})
	}
}
