// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"strings"
	"testing"
	"time"

	"nimbleterminal.dev/ntbrowser/dom"
)

// URL and URLSearchParams parse and serialise per WHATWG, stay linked, and
// confer nothing: there is no fetch behind them.
func TestURLAndSearchParams(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader("<body></body>"), dom.Limits{})
	s := New(Config{Slice: 30 * time.Second}, tree, &nopHost{}) // the cap probe below appends thousands of pairs under the race detector
	t.Cleanup(s.Close)
	err := s.Eval("url.js", `
		const u = new URL('../b?x=1&y=a+b#frag', 'https://user:pw@example.test:8443/a/c/d');
		const parts = [u.href, u.origin, u.protocol, u.host, u.hostname, u.port, u.pathname, u.search, u.hash, u.username];
		const p = u.searchParams;
		parts.push(p.get('y'), p.has('x'), p.has('x', '2'), p.size);
		p.append('z', 'é &=');
		p.set('x', '2');
		p.delete('y');
		parts.push(u.search, String(p), [...p].map(e => e.join(':')).join(','));
		u.search = '?q=hello%20world&q=2';
		parts.push(p.getAll('q').join('|'), p.size);
		const q = new URLSearchParams([['b', '1'], ['a', '2']]); q.sort();
		parts.push(String(q), new URLSearchParams({k: 'v w'}).toString(), new URLSearchParams('?a=1').toString());
		parts.push(URL.canParse('nope'), URL.canParse('/x', 'https://h.test'), String(URL.parse('nope')), JSON.stringify({u}));
		let threw = false; try { new URL('not a url') } catch (e) { threw = e instanceof TypeError }
		parts.push(threw, Object.prototype.toString.call(u), typeof u.searchParams.forEach);
		globalThis.result = parts.join(' | ');
	`)
	if err != nil {
		t.Fatal(err)
	}
	got := s.vm.Get("result").String()
	want := "https://user:pw@example.test:8443/a/b?x=1&y=a+b#frag | https://example.test:8443 | https: | example.test:8443 | example.test | 8443 | /a/b | ?x=1&y=a+b | #frag | user | a b | true | false | 2 | ?x=2&z=%C3%A9+%26%3D | x=2&z=%C3%A9+%26%3D | x:2,z:é &= | hello world|2 | 2 | a=2&b=1 | k=v+w | a=1 | false | true | null | {\"u\":\"https://user:pw@example.test:8443/a/b?q=hello%20world&q=2#frag\"} | true | [object URL] | function"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if err := s.Eval("big.js", `const big = new URLSearchParams(); let capped = false; try { for (let i = 0; i < 5000; i++) big.append('k' + i, 'v') } catch (e) { capped = /too many/.test(String(e)) } globalThis.capped = capped`); err != nil {
		t.Fatal(err)
	}
	if !s.vm.Get("capped").ToBoolean() {
		t.Fatal("parameter count not bounded")
	}
}

// document.write inserts after the running script with inert scripts;
// document.currentScript names a parser-inserted script while it runs.
func TestDocumentWriteAndCurrentScript(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader(`<body><div id="box"><script>1</script><p id="after">after</p></div></body>`), dom.Limits{})
	host := &nopHost{}
	s := New(Config{}, tree, host)
	t.Cleanup(s.Close)
	script := tree.Scripts[0].ID
	err := s.EvalScript("inline.js", `
		globalThis.cur = document.currentScript && document.currentScript.parentElement.id;
		document.write('<span id="w">written</span><script src="/x.js"><\/script>');
		document.writeln('tail');
		globalThis.order = document.getElementById('box').innerHTML;
		globalThis.later = document.currentScript === null || document.currentScript === undefined;
	`, script)
	if err != nil {
		t.Fatal(err)
	}
	if cur := s.vm.Get("cur").String(); cur != "box" {
		t.Fatalf("currentScript parent %q", cur)
	}
	order := s.vm.Get("order").String()
	span, p := strings.Index(order, `<span id="w">written</span>`), strings.Index(order, `<p id="after">`)
	if span < 0 || p < 0 || span > p || !strings.Contains(order[span:p], "tail") {
		t.Fatalf("written nodes not placed after the script: %s", order)
	}
	if !strings.Contains(strings.Join(host.lines, "\n"), "written scripts are not executed") {
		t.Fatalf("no warning for a written script: %v", host.lines)
	}
	if err := s.Eval("after.js", `globalThis.none = document.currentScript === null`); err != nil {
		t.Fatal(err)
	}
	if !s.vm.Get("none").ToBoolean() {
		t.Fatal("currentScript should be null outside a parser-inserted script")
	}
}
