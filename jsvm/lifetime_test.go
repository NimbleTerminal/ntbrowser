package jsvm

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"nimbleterminal.dev/ntbrowser/dom"
)

// Browser semantics: a removed node stays alive while referenced and can be
// re-inserted with its listeners intact.
func TestDetachKeepsNodeAndListenersAlive(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader("<body><ul id=l><li id=a>a</li></ul></body>"), dom.Limits{})
	s := New(Config{}, tree, &nopHost{})
	src := `
		const li = document.getElementById("a");
		let clicks = 0;
		li.addEventListener("click", () => { clicks++; });
		li.remove();
		if (li.isConnected) throw new Error("still connected");
		if (li.textContent !== "a") throw new Error("detached node unusable");
		document.getElementById("l").appendChild(li);   // re-insert
		if (!li.isConnected) throw new Error("not reconnected");
		globalThis.readClicks = () => clicks;`
	if err := s.Eval("t.js", src); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Dispatch(Event{Type: "click", Target: tree.ByID("a").ID()}); err != nil {
		t.Fatal(err)
	}
	v, err := s.vm.RunString("readClicks()")
	if err != nil {
		t.Fatalf("readClicks(): %v", err)
	}
	if v.ToInteger() != 1 {
		t.Fatalf("listener lost across detach/attach: clicks=%d", v.ToInteger())
	}
}

// Detached subtrees that JavaScript no longer references are released from
// the node budget once the GC reports their wrappers.
func TestDetachedSubtreesAreReleasedAfterGC(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader("<body><div id=root></div></body>"), dom.Limits{Nodes: 400})
	s := New(Config{}, tree, &nopHost{})
	build := `
		const root = document.getElementById("root");
		for (let i = 0; i < 100; i++) {
			const d = document.createElement("div");
			d.addEventListener("click", () => {});
			const t = document.createTextNode("x" + i);
			d.appendChild(t);
			root.appendChild(d);
		}
		root.textContent = "";   // detach all 200 nodes; no JS references remain`
	if err := s.Eval("build.js", build); err != nil {
		t.Fatal(err)
	}
	before := tree.Live()
	deadline := time.Now().Add(5 * time.Second)
	for tree.Live() > 10 && time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(5 * time.Millisecond)
		s.Reap()
	}
	if tree.Live() > 10 {
		t.Fatalf("detached nodes not released: live=%d (was %d)", tree.Live(), before)
	}
	if s.nListen != 0 || s.HasListeners("click") {
		t.Fatalf("listener counts not reconciled: nListen=%d", s.nListen)
	}
	// And the budget is usable again.
	if err := s.Eval("again.js", `for (let i = 0; i < 150; i++) document.getElementById("root").appendChild(document.createElement("p"))`); err != nil {
		t.Fatal(err)
	}
}

// A listener on an attached element keeps firing even when the page dropped
// every reference to the element.
func TestAttachedListenerSurvivesWrapperDrop(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader("<body><p id=p>hi</p></body>"), dom.Limits{})
	h := &nopHost{}
	s := New(Config{}, tree, h)
	if err := s.Eval("t.js", `(() => { const p = document.getElementById("p"); p.addEventListener("click", () => console.log("fired")); })()`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		runtime.GC()
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := s.Dispatch(Event{Type: "click", Target: tree.ByID("p").ID()}); err != nil {
		t.Fatal(err)
	}
	if len(h.lines) != 1 || !strings.Contains(h.lines[0], "fired") {
		t.Fatalf("listener did not fire after GC: %q", h.lines)
	}
}

func TestFocusRoutesKeydown(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader("<body><button id=b>go</button><p id=p>x</p></body>"), dom.Limits{})
	h := &nopHost{}
	s := New(Config{}, tree, h)
	src := `
		document.getElementById("b").addEventListener("keydown", e => console.log("button got", e.key));
		document.addEventListener("keydown", e => console.log("document got", e.key, e.target === document.getElementById("b")));
		document.getElementById("b").focus();
		if (document.activeElement.id !== "b") throw new Error("focus() did not move activeElement");`
	if err := s.Eval("t.js", src); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Dispatch(Event{Type: "keydown", Key: "Enter"}); err != nil {
		t.Fatal(err)
	}
	if len(h.lines) != 2 || !strings.Contains(h.lines[0], "button got Enter") || !strings.Contains(h.lines[1], "document got Enter true") {
		t.Fatalf("keydown routing wrong: %q", h.lines)
	}
	// A non-focusable click target clears focus back to the body.
	s.Focus(tree.ByID("p").ID())
	if s.Active() != 0 {
		t.Fatal("non-focusable element took focus")
	}
}

// Nodes created by the page and never inserted are released once their
// wrappers are collected, so a create-and-drop loop cannot pin the budget.
func TestCreatedButNeverInsertedNodesAreReleased(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader("<body></body>"), dom.Limits{Nodes: 60})
	s := New(Config{}, tree, &nopHost{})
	// 500 creations against a 60-node budget only pass if the bridge's forced
	// collection reclaims dropped wrappers along the way.
	if err := s.Eval("churn.js", `for (let i = 0; i < 500; i++) { document.createElement("div"); }`); err != nil {
		t.Fatalf("create-and-drop exhausted the budget: %v", err)
	}
	base := tree.Live()
	deadline := time.Now().Add(5 * time.Second)
	for tree.Live() > 10 && time.Now().Before(deadline) {
		runtime.GC()
		s.Reap()
	}
	if tree.Live() > 10 {
		t.Fatalf("live nodes %d (was %d) after GC: never-inserted nodes not released", tree.Live(), base)
	}
}

// A textContent write that fails on the budget leaves the children attached
// and their listeners firing (the setter is transactional end to end).
func TestFailedTextContentWriteKeepsChildrenAndListeners(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader("<body><div id=d><b id=k>x</b></div></body>"), dom.Limits{Nodes: 12})
	s := New(Config{}, tree, &nopHost{})
	src := `
		globalThis.hits = 0;
		const k = document.getElementById("k");
		k.addEventListener("click", () => { globalThis.hits++; });
		const keep = [];
		for (;;) { try { keep.push(document.createElement("i")); } catch (e) { break; } } // fill the budget, keep refs
		let threw = false;
		try { document.getElementById("d").textContent = "replacement"; } catch (e) { threw = true; }
		if (!threw) throw new Error("write over budget did not throw");
		if (!k.isConnected) throw new Error("child detached by a failed write");`
	if err := s.Eval("t.js", src); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Dispatch(Event{Type: "click", Target: tree.ByID("k").ID()}); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.vm.RunString("hits"); v.ToInteger() != 1 {
		t.Fatalf("listener lost after failed write: hits=%d", v.ToInteger())
	}
}

// Close drops the runtime: scripts can no longer run, and a click on a link
// is reported as not prevented so navigation still works for the reader.
func TestCloseDropsRuntime(t *testing.T) {
	tree, _ := dom.Parse(strings.NewReader(`<body><a id=a href="/x">x</a></body>`), dom.Limits{})
	s := New(Config{}, tree, &nopHost{})
	if err := s.Eval("t.js", `document.getElementById("a").addEventListener("click", e => e.preventDefault())`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if !s.Disabled() {
		t.Fatal("not disabled")
	}
	prevented, err := s.Dispatch(Event{Type: "click", Target: tree.ByID("a").ID()})
	if prevented || err != ErrDisabled {
		t.Fatalf("after Close: prevented=%v err=%v", prevented, err)
	}
	if err := s.Eval("again.js", `1`); err != ErrDisabled {
		t.Fatalf("Eval after Close: %v", err)
	}
}
