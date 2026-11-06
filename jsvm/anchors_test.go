// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"strings"
	"testing"
)

func TestAnchorURLProperties(t *testing.T) {
	s, _, _ := formSandbox(t, `<a id="a" href="../path?q=1#part">old <b>text</b></a>`)
	defer s.Close()
	s.cfg.Location = "https://EXAMPLE.test:443/dir/page"
	imageEval(t, s, `const a=document.getElementById("a");const b=document.createElement("a");`)
	if got := evalString(t, s, `[a instanceof HTMLAnchorElement,b instanceof HTMLAnchorElement,a.href,a.origin,a.protocol,a.hostname,a.port,a.pathname,a.search,a.hash,a.text,String(a)].join("|")`); got != "true|true|https://example.test/path?q=1#part|https://example.test|https:|example.test||/path|?q=1|#part|old text|https://example.test/path?q=1#part" {
		t.Fatal(got)
	}
	imageEval(t, s, `a.protocol="http";a.hostname="other.test";a.port="8080";a.pathname="/two words";a.search="q=a b";a.hash="more words";a.username="u";a.password="p";a.text="changed";`)
	if got := evalString(t, s, `[a.href,a.host,a.text,a.children.length,a.getAttribute("href")].join("|")`); got != "http://u:p@other.test:8080/two%20words?q=a%20b#more%20words|other.test:8080|changed|0|http://u:p@other.test:8080/two%20words?q=a%20b#more%20words" {
		t.Fatal(got)
	}
	imageEval(t, s, `a.setAttribute("href","/live");`)
	if got := evalString(t, s, `a.href`); got != "https://example.test/live" {
		t.Fatal(got)
	}
}

func TestAnchorWHATWGParsingAndOpaqueOrigins(t *testing.T) {
	cases := []struct{ raw, href, origin string }{
		{`HTTP://0x7f000001:80/a/../b`, "http://127.0.0.1/b", "http://127.0.0.1"},
		{`https:\\EXAMPLE.test\a\b`, "https://example.test/a/b", "https://example.test"},
		{"https://bücher.example/", "https://xn--bcher-kva.example/", "https://xn--bcher-kva.example"},
		{"https://[2001:db8::1]:443/", "https://[2001:db8::1]/", "https://[2001:db8::1]"},
		{"javascript:alert(1)", "javascript:alert(1)", "null"},
		{"mailto:a@example.test", "mailto:a@example.test", "null"},
		{"file:///tmp/x", "file:///tmp/x", "null"},
		{"blob:https://example.test/id", "blob:https://example.test/id", "https://example.test"},
		{"http://[bad", "http://[bad", ""},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			s, _, _ := formSandbox(t, "")
			defer s.Close()
			imageEval(t, s, `const a=document.createElement("a")`)
			// Assign through the bridge without quoting page-controlled URL text into code.
			_ = s.vm.Set("inputURL", tc.raw)
			imageEval(t, s, `a.href=inputURL`)
			if got := evalString(t, s, `a.href+"|"+a.origin`); got != tc.href+"|"+tc.origin {
				t.Fatal(got)
			}
		})
	}
}

func TestAnchorMissingInvalidAndReflectedAttributes(t *testing.T) {
	s, _, _ := formSandbox(t, "")
	defer s.Close()
	s.cfg.Location = "https://example.test/page"
	imageEval(t, s, `const a=document.createElement("a");a.pathname="/ignored"`)
	if got := evalString(t, s, `[a.href,a.protocol,a.origin,a.hasAttribute("href")].join("|")`); got != "|:||false" {
		t.Fatal(got)
	}
	imageEval(t, s, `a.href="";a.target="_blank";a.download="file.txt";a.ping="https://tracker.test";a.hreflang="en";a.type="text/html";a.referrerPolicy="ORIGIN";`)
	if got := evalString(t, s, `[a.href,a.target,a.download,a.ping,a.hreflang,a.type,a.referrerPolicy].join("|")`); got != "https://example.test/page|_blank|file.txt|https://tracker.test|en|text/html|origin" {
		t.Fatal(got)
	}
	imageEval(t, s, `a.referrerPolicy="invalid";a.href="http://[bad";a.port="99"`)
	if got := evalString(t, s, `[a.referrerPolicy,a.href,typeof a.click].join("|")`); got != "|http://[bad|undefined" {
		t.Fatal(got)
	}
}

func TestAnchorRelListIsLiveAndIterable(t *testing.T) {
	s, _, _ := formSandbox(t, `<a id=a rel="one two one"></a>`)
	defer s.Close()
	imageEval(t, s, `const a=document.getElementById("a"), list=a.relList;list.add("three");list.remove("two");list.toggle("one",true);list.replace("three","four");`)
	if got := evalString(t, s, `[list===a.relList,list.length,list[0],list.item(99)===null,Array.from(list).join(),Object.keys(list).join(),String(list),a.rel].join("|")`); got != "true|2|one|true|one,four|0,1|one four|one four" {
		t.Fatal(got)
	}
	imageEval(t, s, `a.setAttribute("rel","live second live");`)
	if got := evalString(t, s, `[list.value,list[0],list.contains("second"),Array.from(list.entries()).map(x=>x.join(":")).join()].join("|")`); got != "live second live|live|true|0:live,1:second" {
		t.Fatal(got)
	}
	imageEval(t, s, `list.value="last";a.relList="assigned";`)
	if got := evalString(t, s, `[a.rel,list.value,list[1],list.supports("opener")].join("|")`); got != "assigned|assigned||false" {
		t.Fatal(got)
	}
}

func TestAnchorRelListValidationIsAtomic(t *testing.T) {
	s, _, _ := formSandbox(t, `<a id=a rel="old"></a>`)
	defer s.Close()
	imageEval(t, s, `const a=document.getElementById("a");let names=[];try{a.relList.add("new","bad token")}catch(e){names.push(e.name)};try{a.relList.toggle("")}catch(e){names.push(e.name)}`)
	if got := evalString(t, s, `a.rel+"|"+names.join()`); got != "old|InvalidCharacterError,SyntaxError" {
		t.Fatal(got)
	}
	imageEval(t, s, `a.rel="non\u00a0breaking"`)
	if evalString(t, s, `a.relList.length`) != "1" {
		t.Fatal("non-ASCII whitespace split token")
	}
	err := s.Eval("budget.js", `a.pathname="x".repeat(100000)`)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatal(err)
	}
}
