// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"testing"

	"nimbleterminal.dev/ntbrowser/sessionstorage"
)

// TestLocalStorageIsASeparateStorageArea: localStorage binds the host's local
// store for the document origin, independent of sessionStorage, through the
// same Storage interface; a closed gate raises SecurityError.
func TestLocalStorageIsASeparateStorageArea(t *testing.T) {
	local := sessionstorage.New(0, 0)
	session := sessionstorage.New(0, 0)
	s, _ := eventSandbox(t, Config{SessionStorage: session.Open("https://example.test/"), LocalStorage: local.Open("https://example.test/")})
	eventEval(t, s, `
 assert(localStorage instanceof Storage && sessionStorage instanceof Storage && localStorage !== sessionStorage);
 assert(localStorage === window.localStorage && localStorage.length === 0);
 localStorage.setItem('theme', 'dark'); localStorage.count = '2';
 assert(localStorage.getItem('theme') === 'dark' && localStorage.length === 2 && localStorage.key(1) === 'count');
 assert(sessionStorage.getItem('theme') === null && sessionStorage.length === 0);
 sessionStorage.setItem('tab', '1'); assert(localStorage.getItem('tab') === null);
 assert(Storage.prototype.getItem.call(localStorage, 'theme') === 'dark');
 let n = ''; try { Storage.prototype.getItem.call({}, 'x') } catch (e) { n = e.name } assert(n === 'TypeError');
 localStorage.removeItem('count'); assert(localStorage.length === 1);
 `)
	if v, ok := local.Open("https://example.test/").Get("t\x00h\x00e\x00m\x00e\x00"); !ok || v != "d\x00a\x00r\x00k\x00" {
		t.Fatalf("host store not written: %q %v", v, ok)
	}
	if session.Open("https://example.test/").Len() != 1 {
		t.Fatal("session store crossed over")
	}

	closed, _ := eventSandbox(t, Config{SessionStorage: session.Open("https://example.test/")})
	eventEval(t, closed, `
 let n = ''; try { localStorage } catch (e) { n = e.name } assert(n === 'SecurityError', n);
 assert(sessionStorage.length === 1);
 `)
}
