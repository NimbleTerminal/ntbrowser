// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package jsvm

import (
	"go/build"
	"net"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"

	"nimbleterminal.dev/ntbrowser/dom"
)

// TestNoSystemImports pins the hardened boundary: the packages that touch the
// VM must not import anything that can reach the OS. netpolicy and cmd are
// where those imports belong.
func TestNoSystemImports(t *testing.T) {
	forbidden := map[string]bool{"os": true, "os/exec": true, "net": true, "net/http": true, "syscall": true, "io/fs": true, "plugin": true, "unsafe": true}
	for _, pkg := range []string{"nimbleterminal.dev/ntbrowser/jsvm", "nimbleterminal.dev/ntbrowser/dom", "nimbleterminal.dev/ntbrowser/profile", "nimbleterminal.dev/ntbrowser/content"} {
		p, err := build.Import(pkg, ".", 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range p.Imports {
			if forbidden[imp] {
				t.Errorf("%s imports %s", pkg, imp)
			}
		}
	}
}

type nopHost struct {
	lines    []string
	fetchErr error  // when set, Fetch fails with it once the test calls pending()
	pending  func() // the deferred completion, as the loop would deliver it later on the VM goroutine
}

func (h *nopHost) SetTimer(time.Duration, bool, goja.Callable, []goja.Value) (int, bool) {
	return 1, true
}
func (h *nopHost) ClearTimer(int)                 {}
func (h *nopHost) RequestFrame(goja.Callable) int { return 1 }
func (h *nopHost) CancelFrame(int)                {}
func (h *nopHost) Fetch(_ FetchRequest, done func(FetchResponse, error)) error {
	if h.fetchErr != nil {
		h.pending = func() { done(FetchResponse{}, h.fetchErr) }
	}
	return nil
}
func (h *nopHost) Log(level, line string) { h.lines = append(h.lines, level+": "+line) }

// TestGlobalSurface snapshots every global name scripts can see. A new name
// here is a review event: it means the boundary grew.
func TestGlobalSurface(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader("<p id=x>hi</p>"), dom.Limits{})
	s := New(Config{}, tree, &nopHost{})
	names := s.vm.GlobalObject().GetOwnPropertyNames()
	sort.Strings(names)
	for _, n := range names {
		switch n {
		case "eval", "require", "process", "XMLHttpRequest", "WebSocket", "Worker", "SharedArrayBuffer", "Atomics", "importScripts", "WebAssembly":
			t.Errorf("forbidden global present: %s", n)
		}
	}
	have := map[string]bool{}
	for _, n := range names {
		have[n] = true
	}
	for _, want := range []string{"document", "fetch", "console", "setTimeout", "requestAnimationFrame", "location"} {
		if !have[want] {
			t.Errorf("missing host global %s", want)
		}
	}
}

func TestWatchdogAndStrikes(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader("<p></p>"), dom.Limits{})
	h := &nopHost{}
	s := New(Config{Slice: 20 * time.Millisecond, Strikes: 2}, tree, h)
	for i := 0; i < 2; i++ {
		if err := s.Eval("spin.js", "for(;;){}"); err == nil {
			t.Fatal("expected interrupt")
		}
	}
	if !s.Disabled() {
		t.Fatal("expected scripting disabled after strikes")
	}
	if err := s.Eval("after.js", "1"); err != ErrDisabled {
		t.Fatalf("got %v", err)
	}
}

func TestBridgeMutatesTreeNotPointers(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader("<body><p id=x>hi</p></body>"), dom.Limits{})
	s := New(Config{}, tree, &nopHost{})
	src := `
		const p = document.getElementById("x");
		p.textContent = "<b>changed</b>";
		p.setAttribute("class", "note");
		const q = document.createElement("p");
		q.textContent = "two";
		document.body.appendChild(q);
		p.addEventListener("click", e => e.preventDefault());
		q.addEventListener("click", () => {}); // no id: serializer must mint one
		typeof p.__proto__.appendChild === "function" && Object.getOwnPropertySymbols(p).length === 1`
	if err := s.Eval("t.js", src); err != nil {
		t.Fatal(err)
	}
	html := tree.Serialize()
	for _, want := range []string{`<p id="x" class="note">&lt;b&gt;changed&lt;/b&gt;</p>`, `<p id="nt-h-`, `two</p>`} {
		if !strings.Contains(html, want) {
			t.Errorf("serialized html missing %q:\n%s", want, html)
		}
	}
	prevented, err := s.Dispatch(Event{Type: "click", Target: tree.ByID("x").ID(), Href: "https://example.com"})
	if err != nil || !prevented {
		t.Fatalf("prevented=%v err=%v", prevented, err)
	}
	if !s.HasListeners("click") || s.HasListeners("keydown") {
		t.Fatal("listener bookkeeping wrong")
	}
}

func TestConsoleStripsTerminalEscapes(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader(""), dom.Limits{})
	h := &nopHost{}
	s := New(Config{}, tree, h)
	_ = s.Eval("c.js", `console.log("a\u001b]52;c;payload\u0007b", {k:1})`)
	if len(h.lines) != 1 || strings.ContainsRune(h.lines[0], 0x1b) || !strings.Contains(h.lines[0], `{"k":1}`) {
		t.Fatalf("unsafe or wrong log line: %q", h.lines)
	}
}

func TestTextContentSetterReleasesListeners(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader("<body><ul id=l></ul></body>"), dom.Limits{})
	s := New(Config{MaxListeners: 4}, tree, &nopHost{})
	src := `
		const ul = document.getElementById("l");
		for (let round = 0; round < 10; round++) {
			ul.textContent = "";               // frees previous children
			for (let i = 0; i < 3; i++) {
				const li = document.createElement("li");
				li.addEventListener("click", () => {});
				ul.appendChild(li);
			}
		}`
	if err := s.Eval("churn.js", src); err != nil {
		t.Fatalf("listener budget leaked across textContent resets: %v", err)
	}
	if s.nListen != 3 {
		t.Fatalf("nListen=%d want 3", s.nListen)
	}
}

// Every error the sandbox throws or rejects with is a plain JavaScript error
// carrying only a message: no reflected Go value (spec §2 anti-goal). Checked
// for a failed fetch (whatever error chain the Go HTTP stack produced) and a
// dom budget failure.
func TestErrorsNeverCarryGoValues(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader("<body></body>"), dom.Limits{Nodes: 6})
	host := &nopHost{fetchErr: &url.Error{Op: "Get", URL: "https://x.test/", Err: &net.OpError{Op: "dial", Addr: &net.TCPAddr{IP: net.IPv4(10, 1, 2, 3), Port: 443}}}}
	s := New(Config{}, tree, host)
	src := `
		globalThis.report = [];
		function inspect(tag, e) {
			report.push(tag + ":" + (e instanceof Error) + ":" + (e.value === undefined) + ":" + Object.keys(e).join(",") + ":" + typeof e.message);
		}
		const keep = []; // held, so the GC cannot reclaim them and the budget trips
		try { for (;;) keep.push(document.createElement("div")); } catch (e) { inspect("dom", e); }
		fetch("https://x.test/").catch(e => inspect("fetch", e));`
	if err := s.Eval("t.js", src); err != nil {
		t.Fatal(err)
	}
	if host.pending == nil {
		t.Fatal("fetch was not started")
	}
	host.pending() // completion arrives outside any slice; the rejection and its catch run now
	v, _ := s.vm.RunString("report.join(\"|\")")
	got := v.String()
	for _, want := range []string{"dom:true:true::string", "fetch:true:true::string"} {
		if !strings.Contains(got, want) {
			t.Fatalf("page saw a non-plain error: %q (want %q)", got, want)
		}
	}
}
