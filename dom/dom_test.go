package dom

import (
	"errors"
	"strings"
	"testing"
)

func mustTree(t *testing.T, html string, l Limits) *Tree {
	t.Helper()
	tr, err := Parse(strings.NewReader(html), l)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// Every parentless node is reapable: created-and-dropped nodes count against
// the budget only until Release, exactly like removed subtrees.
func TestCreatedNodesAreDetachedUntilInserted(t *testing.T) {
	tr := mustTree(t, `<body></body>`, Limits{Nodes: 20})
	before := tr.Live()
	n, err := tr.NewElement("div")
	if err != nil {
		t.Fatal(err)
	}
	if roots := tr.DetachedRoots(); len(roots) != 1 || roots[0] != n {
		t.Fatalf("fresh node not reapable: %v", roots)
	}
	body, _ := tr.Query(tr.Root(), "body", true)
	if err := tr.Append(body[0], n); err != nil {
		t.Fatal(err)
	}
	if len(tr.DetachedRoots()) != 0 {
		t.Fatal("attached node still listed as detached")
	}
	tr.Detach(n)
	tr.Release(n)
	if tr.Live() != before {
		t.Fatalf("live %d after release, want %d", tr.Live(), before)
	}
	// Dropped without ever being inserted.
	m, _ := tr.NewElement("span")
	tr.Release(m)
	if tr.Live() != before || tr.Node(m.ID()) != nil {
		t.Fatal("never-inserted node was not released")
	}
}

func TestInsertBeforeSelfIsAMoveToTheSamePlace(t *testing.T) {
	tr := mustTree(t, `<body><p id="a"></p><p id="b"></p></body>`, Limits{})
	a, b := tr.ByID("a"), tr.ByID("b")
	body := a.Parent()
	if err := tr.InsertBefore(body, a, a); err != nil {
		t.Fatalf("insertBefore(node, node): %v", err)
	}
	if kids := body.Children(); len(kids) != 2 || kids[0] != a || kids[1] != b || a.Parent() != body {
		t.Fatalf("order changed or node orphaned: %v", kids)
	}
	if len(tr.DetachedRoots()) != 0 {
		t.Fatal("a node leaked into the detached set")
	}
	// A reference node from elsewhere is rejected without touching the child.
	other, _ := tr.NewElement("div")
	if err := tr.InsertBefore(body, b, other); err == nil || b.Parent() != body {
		t.Fatalf("bad ref: err=%v parent=%v", err, b.Parent())
	}
}

func TestSetTextOverBudgetKeepsChildren(t *testing.T) {
	tr := mustTree(t, `<body><div id="d"><b>x</b><i>y</i></div></body>`, Limits{Nodes: 12})
	d := tr.ByID("d")
	// Fill the remaining node budget so the replacement text node cannot be
	// allocated; the children must survive the failed write.
	for {
		n, err := tr.NewElement("span")
		if err != nil {
			break
		}
		_ = n
	}
	err := tr.SetText(d, "replacement")
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("want ErrLimit, got %v", err)
	}
	if len(d.Children()) != 2 || d.Children()[0].Parent() != d {
		t.Fatalf("failed write dropped the children: %v", d.Children())
	}
	// Clearing never needs a node.
	if err := tr.SetText(d, ""); err != nil || len(d.Children()) != 0 {
		t.Fatalf("clear: err=%v children=%v", err, d.Children())
	}
}

// The accounted size is an upper bound of the serializer's output, and the
// cap holds under worst-case escaping and minted ids.
func TestSerializedSizeNeverExceedsAccountedBytes(t *testing.T) {
	tr := mustTree(t, `<body></body>`, Limits{TotalBytes: 8 << 10})
	body, _ := tr.Query(tr.Root(), "body", true)
	hostile := strings.Repeat(`<>&"'`, 20)
	for {
		el, err := tr.NewElement("div")
		if err != nil {
			break
		}
		if err := tr.SetAttr(el, "title", hostile); err != nil {
			break
		}
		txt, err := tr.NewText(hostile)
		if err != nil {
			break
		}
		if err := tr.Append(body[0], el); err != nil {
			break
		}
		if err := tr.Append(el, txt); err != nil {
			break
		}
		tr.AddListener(el, "click") // forces a minted id in the output
	}
	out := tr.Serialize()
	if len(out) > tr.Bytes() {
		t.Fatalf("serialized %d bytes > accounted %d", len(out), tr.Bytes())
	}
	if tr.Bytes() > 8<<10 {
		t.Fatalf("accounted %d bytes over the 8 KiB cap", tr.Bytes())
	}
	if !strings.Contains(out, `id="nt-h-`) || !strings.Contains(out, "&lt;&gt;&amp;") {
		t.Fatal("output lacks the minted id or escaping the bound must cover")
	}
	// Releasing gives the budget back.
	for _, c := range body[0].Children() {
		tr.Detach(c)
		tr.Release(c)
	}
	if _, err := tr.NewText(hostile); err != nil {
		t.Fatalf("budget not returned after release: %v", err)
	}
}

func TestParseChargesBytes(t *testing.T) {
	_, err := Parse(strings.NewReader(`<p>`+strings.Repeat("x", 3000)+`</p>`), Limits{TotalBytes: 2048})
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("oversize page parsed: %v", err)
	}
}
