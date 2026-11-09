// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

// Package dom is ntbrowser's own mutable document tree. JavaScript never sees
// these structs: the jsvm package hands scripts integer handles and proxies
// every operation through Tree methods. nthtml's document.Document is
// immutable, so a committed Tree is serialized to HTML and re-parsed by nthtml
// on every frame; this package is the single writable model.
package dom

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// MaxExternalStyleBytes caps optional host-only stylesheet storage.
const MaxExternalStyleBytes = 4 << 20

// MaxTotalBytes is the most a host may raise TotalBytes to: nthtml's own
// input ceiling, which the serialized tree must still fit.
const MaxTotalBytes = 16 << 20

// Limits bound what scripts can grow the tree into. Zero fields use defaults.
// They are deliberately at or below nthtml's document.DefaultLimits so a
// serialized tree can never exceed the renderer's own budget: Nodes and Depth
// stay under nthtml's, and TotalBytes bounds the script-mutable serialized size (escaped
// text, tags, attributes and minted ids), so a tree inside these limits never
// makes document.Render fail with ErrLimit. An optional ExternalStyleBytes
// allowance adds host-loaded CSS; the host must raise the renderer byte cap by
// the same bounded amount. The per-node TextBytes cap alone
// would not do that (8000 × 64 KiB is far over nthtml's 1 MiB input cap).
type Limits struct {
	ExternalStyleBytes int // optional separate host stylesheet allowance; zero shares TotalBytes

	Nodes      int // total live nodes (default 8000; nthtml caps parsing at 10000)
	Depth      int // nesting depth (default 48; nthtml caps at 64)
	TextBytes  int // bytes per text node or attribute value (default 64 KiB)
	Attrs      int // attributes per element (default 32)
	TotalBytes int // upper bound of Serialize() output (default 1 MiB = nthtml DefaultLimits.Bytes)
}

var DefaultLimits = Limits{Nodes: 8000, Depth: 48, TextBytes: 64 << 10, Attrs: 32, TotalBytes: 1 << 20}

// Serialized-size accounting. Every cost is an upper bound of what write()
// emits for that node, so Σ cost ≥ len(Serialize()). Elements reserve room
// for a minted ` id="nt-h-N"` (≤ 20 bytes for ids under 10^8).
const elementOverhead = 5 + 20 // '<' + '>' + '</' + '>' + minted id reserve

func textCost(s string) int { return len(html.EscapeString(s)) }
func elementCost(tag string) int {
	extra := 0
	if tag == "iframe" {
		extra = 128
	}
	return 2*len(tag) + elementOverhead + extra
}
func attrCost(name, value string) int { return len(name) + 4 + textCost(value) } // ' name="value"'

func nodeCost(n *Node) int {
	if n.kind == Text {
		return textCost(n.text)
	}
	c := elementCost(n.tag) + stateCost(n)
	for _, a := range n.attrs {
		c += attrCost(a.Name, a.Value)
	}
	return c
}

var ErrLimit = errors.New("ntbrowser/dom: resource limit exceeded")

// NodeID is the only thing JavaScript ever holds. IDs are never reused within
// one Tree, so a stale handle fails closed instead of aliasing a new node.
type NodeID int

type Kind uint8

const (
	Element Kind = iota
	Text
)

type Node struct {
	parserScript bool

	id       NodeID
	kind     Kind
	tag      string // lower-case; empty for text nodes
	text     string // text nodes only
	attrs    []Attr // ordered, unique keys, lower-case names
	parent   *Node
	children []*Node
	// listeners is the set of event types JS registered on this node. The
	// serializer gives such elements a stable id attribute so nthtml's
	// Box.Anchor / Result.Anchors can be mapped back to a NodeID on hit test.
	listeners map[string]struct{}
	form      *formState // nil until a control's state changes (forms.go)
	playable  bool       // a <video> the profile lets the reader focus (MarkVideos)
}

type Attr struct{ Name, Value string }

func (n *Node) ParserScript() bool { return n.parserScript }

func (n *Node) ID() NodeID        { return n.id }
func (n *Node) Kind() Kind        { return n.kind }
func (n *Node) Tag() string       { return n.tag }
func (n *Node) Text() string      { return n.text }
func (n *Node) Parent() *Node     { return n.parent }
func (n *Node) Children() []*Node { return append([]*Node(nil), n.children...) }

func (n *Node) Attr(name string) (string, bool) {
	name = strings.ToLower(name)
	for _, a := range n.attrs {
		if a.Name == name {
			return a.Value, true
		}
	}
	return "", false
}

// HasListener reports whether any JS listener of the given type is attached.
func (n *Node) HasListener(typ string) bool {
	_, ok := n.listeners[typ]
	return ok
}

// Tree is single-owner: only the VM goroutine mutates it. Serialize produces
// an immutable snapshot string that may be rendered on another goroutine.
type Tree struct {
	mutationHook func(Mutation)
	frames       map[NodeID]FrameImage
	limits       Limits
	root         *Node // synthetic "document" node
	nodes        map[NodeID]*Node
	// detached holds every parentless non-root node: subtree roots removed
	// from the document and nodes created but not (yet) inserted. The bridge
	// releases them once no wrapper in the subtree is alive; see Release.
	detached map[NodeID]*Node
	nextID   NodeID
	live     int
	bytes    int // Σ nodeCost over live nodes; ≥ len(Serialize())
	dirty    bool
	title    string
	// Scripts collected during Parse, in document order. They are omitted
	// from the serialized HTML so nthtml never sees them (defense in depth;
	// nthtml also never executes scripts).
	Scripts []Script
	// styles holds loaded external stylesheets keyed by the raw <link href>
	// (see styles.go); the serializer writes each as a <style> twin.
	styleBytes int
	styles     map[string]*injectedStyle
	ids        map[string]bool // Serialize's id-uniqueness table, nil outside it
}

type Script struct {
	Integrity, CrossOrigin string

	Inline string // inline source; empty when Src is set
	Src    string // external src as written; the host decides whether to fetch it
}

func limits(l Limits) Limits {
	d := DefaultLimits
	d.ExternalStyleBytes = max(0, min(l.ExternalStyleBytes, MaxExternalStyleBytes))
	if l.Nodes > 0 && l.Nodes < d.Nodes {
		d.Nodes = l.Nodes
	}
	if l.Depth > 0 && l.Depth < d.Depth {
		d.Depth = l.Depth
	}
	if l.TextBytes > 0 && l.TextBytes < d.TextBytes {
		d.TextBytes = l.TextBytes
	}
	if l.Attrs > 0 && l.Attrs < d.Attrs {
		d.Attrs = l.Attrs
	}
	if l.TotalBytes > 0 {
		// The byte budget alone may be raised, up to nthtml's input ceiling:
		// a host that admits a larger page body sets it to match.
		d.TotalBytes = min(l.TotalBytes, MaxTotalBytes)
	}
	return d // other values above defaults are ignored, mirroring nthtml's policy
}

// Parse builds a Tree from HTML using golang.org/x/net/html (already a
// dependency of nthtml). Script bodies are collected into t.Scripts and
// retained in the tree but omitted from rendering. Input must already be bounded by the caller.
func Parse(r io.Reader, l Limits) (*Tree, error) {
	t := &Tree{limits: limits(l), nodes: map[NodeID]*Node{}, detached: map[NodeID]*Node{}, styles: map[string]*injectedStyle{}}
	t.root = t.newNode(Element, "#document")
	delete(t.detached, t.root.id)
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}
	if err := t.ingest(t.root, doc, 0); err != nil {
		return nil, err
	}
	t.dirty = true
	return t, nil
}

func (t *Tree) ingest(parent *Node, src *html.Node, depth int) error {
	for c := src.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.DoctypeNode, html.CommentNode:
			continue
		case html.TextNode:
			// Parsed scripts may exceed the per-text-node cap. Keep exact text in
			// bounded chunks, charging every chunk to the ordinary node/byte budgets.
			if parent.tag == "script" && len(c.Data) > t.limits.TextBytes {
				text := cleanText(c.Data)
				for len(text) > 0 {
					end := min(len(text), t.limits.TextBytes)
					for end < len(text) && end > 0 && !utf8.RuneStart(text[end]) {
						end--
					}
					if end == 0 {
						return fmt.Errorf("%w: script text rune exceeds text limit", ErrLimit)
					}
					n, err := t.NewText(text[:end])
					if err != nil {
						return err
					}
					t.attach(parent, n)
					text = text[end:]
				}
				continue
			}
			if strings.TrimSpace(c.Data) == "" && parent.tag != "pre" {
				if parent.tag == "#document" || parent.tag == "html" || parent.tag == "head" {
					continue
				}
			}
			n, err := t.NewText(c.Data)
			if err != nil {
				return err
			}
			t.attach(parent, n)
		case html.ElementNode:
			switch c.DataAtom {
			case atom.Title:
				if c.FirstChild != nil {
					t.title = c.FirstChild.Data
				}
			}
			if depth+1 > t.limits.Depth {
				return fmt.Errorf("%w: depth %d", ErrLimit, t.limits.Depth)
			}
			n, err := t.NewElement(c.Data)
			if err != nil {
				if errors.Is(err, ErrLimit) {
					return err
				}
				// A tag name the serializer could not emit safely (framework
				// or non-ASCII custom elements): keep its content, drop the
				// element. Budget failures still abort, as nthtml's would.
				if err := t.ingest(parent, c, depth); err != nil {
					return err
				}
				continue
			}
			for _, a := range c.Attr {
				if err := t.SetAttr(n, a.Key, a.Val); err != nil {
					if errors.Is(err, ErrLimit) && !strings.Contains(err.Error(), "attributes") {
						return err // the page's byte budget, not a per-element cap
					}
					continue // @click, :class, [hidden], (input), #ref, or the 33rd attribute: dropped, page kept
				}
			}
			t.attach(parent, n)
			if err := t.ingest(n, c, depth+1); err != nil {
				return err
			}
			if n.tag == "script" {
				n.parserScript = true
				typ, _ := n.Attr("type")
				if isJSType(typ) {
					src, _ := n.Attr("src")
					integrity, _ := n.Attr("integrity")
					cors, present := n.Attr("crossorigin")
					if !present {
						cors = "none"
					} else if !strings.EqualFold(cors, "use-credentials") {
						cors = "anonymous"
					}
					sc := Script{Src: src, Integrity: integrity, CrossOrigin: cors}
					if src == "" {
						sc.Inline = TextContent(n)
					}
					t.Scripts = append(t.Scripts, sc)
				}
			}
		}
	}
	return nil
}

func isJSType(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "text/javascript", "application/javascript", "text/ecmascript": // not "module": Goja has no ES modules
		return true
	}
	return false
}

func (t *Tree) newNode(k Kind, tag string) *Node {
	t.nextID++
	n := &Node{id: t.nextID, kind: k, tag: tag}
	t.nodes[n.id] = n
	t.detached[n.id] = n // parentless until attached
	return n
}

// charge reserves delta serialized bytes or fails with ErrLimit.
func (t *Tree) charge(delta int) error {
	if delta > 0 && t.contentBytes()+delta > t.limits.TotalBytes {
		return fmt.Errorf("%w: serialized size over %d bytes", ErrLimit, t.limits.TotalBytes)
	}
	t.bytes += delta
	return nil
}

// ---- mutation API: the only entry points the JS bridge is allowed to call ----

func (t *Tree) Root() *Node          { return t.root }
func (t *Tree) Title() string        { return t.title }
func (t *Tree) Dirty() bool          { return t.dirty }
func (t *Tree) ClearDirty()          { t.dirty = false }
func (t *Tree) Node(id NodeID) *Node { return t.nodes[id] }

func (t *Tree) Body() *Node {
	return t.find(t.root, func(n *Node) bool { return n.tag == "body" })
}

func (t *Tree) NewElement(tag string) (*Node, error) {
	tag = strings.ToLower(tag)
	if tag == "" || tag == "#document" || !validName(tag) {
		return nil, fmt.Errorf("ntbrowser/dom: invalid tag %q", tag)
	}
	if t.live+1 > t.limits.Nodes {
		return nil, fmt.Errorf("%w: %d nodes", ErrLimit, t.limits.Nodes)
	}
	if err := t.charge(elementCost(tag)); err != nil {
		return nil, err
	}
	t.live++
	return t.newNode(Element, tag), nil
}

// cleanText makes page text safe to carry: invalid UTF-8 becomes U+FFFD and
// NUL is dropped, so the serializer's output is always valid UTF-8 nthtml
// accepts.
func cleanText(s string) string {
	if utf8.ValidString(s) && !strings.ContainsRune(s, 0) {
		return s
	}
	return strings.ReplaceAll(strings.ToValidUTF8(s, "\uFFFD"), "\x00", "")
}

func (t *Tree) NewText(s string) (*Node, error) {
	s = cleanText(s)
	if len(s) > t.limits.TextBytes {
		return nil, fmt.Errorf("%w: text over %d bytes", ErrLimit, t.limits.TextBytes)
	}
	if t.live+1 > t.limits.Nodes {
		return nil, fmt.Errorf("%w: %d nodes", ErrLimit, t.limits.Nodes)
	}
	if err := t.charge(textCost(s)); err != nil {
		return nil, err
	}
	t.live++
	n := t.newNode(Text, "")
	n.text = s
	return n, nil
}

func (t *Tree) SetAttr(n *Node, name, value string) error {
	name = strings.ToLower(name)
	value = cleanText(value)
	if n.kind != Element || !validName(name) {
		return errors.New("ntbrowser/dom: invalid attribute name")
	}
	if len(value) > t.limits.TextBytes {
		return fmt.Errorf("%w: attribute over %d bytes", ErrLimit, t.limits.TextBytes)
	}
	// Event-handler attributes are inert: the only way to run code is
	// addEventListener through the bridge, which the loop controls.
	if strings.HasPrefix(name, "on") {
		return nil
	}
	for i := range n.attrs {
		if n.attrs[i].Name == name {
			if err := t.charge(textCost(value) - textCost(n.attrs[i].Value)); err != nil {
				return err
			}
			old := n.attrs[i].Value
			n.attrs[i].Value = value
			t.dirty = true
			t.styleMediaDelta(n)
			t.emitMutation(Mutation{Type: "attributes", Target: n, AttributeName: name, OldValue: old, OldPresent: true})
			return nil
		}
	}
	if len(n.attrs) >= t.limits.Attrs {
		return fmt.Errorf("%w: %d attributes", ErrLimit, t.limits.Attrs)
	}
	if err := t.charge(attrCost(name, value)); err != nil {
		return err
	}
	n.attrs = append(n.attrs, Attr{name, value})
	t.dirty = true
	t.styleMediaDelta(n)
	t.emitMutation(Mutation{Type: "attributes", Target: n, AttributeName: name})
	return nil
}

func (t *Tree) RemoveAttr(n *Node, name string) {
	name = strings.ToLower(name)
	for i := range n.attrs {
		if n.attrs[i].Name == name {
			old := n.attrs[i].Value
			t.bytes -= attrCost(n.attrs[i].Name, n.attrs[i].Value)
			n.attrs = append(n.attrs[:i], n.attrs[i+1:]...)
			t.dirty = true
			t.styleMediaDelta(n)
			t.emitMutation(Mutation{Type: "attributes", Target: n, AttributeName: name, OldValue: old, OldPresent: true})
			return
		}
	}
}

func (t *Tree) SetText(n *Node, s string) error {
	if len(s) > t.limits.TextBytes {
		return fmt.Errorf("%w: text over %d bytes", ErrLimit, t.limits.TextBytes)
	}
	s = cleanText(s)
	m := Mutation{Target: n}
	switch n.kind {
	case Text:
		if err := t.charge(textCost(s) - textCost(n.text)); err != nil {
			return err
		}
		m.Type = "characterData"
		m.OldValue = n.text
		m.OldPresent = true
		n.text = s
	case Element: // textContent setter semantics: replace all children
		// Allocate first so a failed write leaves the children in place
		// (the bridge may retry after a GC). An empty string just clears, as
		// in browsers, and costs no node.
		var c *Node
		if s != "" {
			var err error
			if c, err = t.NewText(s); err != nil {
				return err
			}
		}
		m.Type = "childList"
		m.Removed = n.Children()
		for _, old := range n.children {
			old.parent = nil
			t.detached[old.id] = old // the bridge releases them once unreferenced
		}
		n.children = nil
		if c != nil {
			t.attach(n, c)
			m.Added = []*Node{c}
		}
	}
	t.dirty = true
	if m.Type != "childList" || len(m.Added)+len(m.Removed) > 0 {
		t.emitMutation(m)
	}
	return nil
}

// TextContent concatenates descendant text, like the DOM getter.
func TextContent(n *Node) string {
	var b strings.Builder
	var walk func(*Node)
	walk = func(n *Node) {
		if n.kind == Text {
			b.WriteString(n.text)
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func (t *Tree) Append(parent, child *Node) error {
	return t.InsertBefore(parent, child, nil)
}

func (t *Tree) InsertBefore(parent, child, ref *Node) error {
	if parent.kind != Element || parent == child {
		return errors.New("ntbrowser/dom: invalid parent")
	}
	for a := parent; a != nil; a = a.parent {
		if a == child {
			return errors.New("ntbrowser/dom: cycle")
		}
	}
	if depthOf(parent)+height(child) > t.limits.Depth {
		return fmt.Errorf("%w: depth %d", ErrLimit, t.limits.Depth)
	}
	if ref != nil && ref.parent != parent {
		return errors.New("ntbrowser/dom: reference node is not a child")
	}
	if ref == child {
		return nil // insertBefore(node, node) is a move to its own place: no-op
	}
	if child.parent != nil {
		t.Detach(child)
	}
	if ref == nil {
		t.attach(parent, child)
		t.dirty = true
		previous, next := siblings(child)
		t.emitMutation(Mutation{Type: "childList", Target: parent, Added: []*Node{child}, PreviousSibling: previous, NextSibling: next})
		return nil
	}
	for i, c := range parent.children {
		if c == ref {
			parent.children = append(parent.children[:i], append([]*Node{child}, parent.children[i:]...)...)
			child.parent = parent
			delete(t.detached, child.id)
			t.dirty = true
			previous, next := siblings(child)
			t.emitMutation(Mutation{Type: "childList", Target: parent, Added: []*Node{child}, PreviousSibling: previous, NextSibling: next})
			return nil
		}
	}
	// Unreachable after the checks above; if it ever happens the child is
	// parentless and must stay reapable.
	t.detached[child.id] = child
	return errors.New("ntbrowser/dom: reference node is not a child")
}

// Detach removes the subtree from its parent but keeps it alive: browser
// semantics, where a removed node stays usable through JS references and can
// be re-inserted. Detached roots count toward the node budget until Release.
func (t *Tree) Detach(n *Node) {
	if n == t.root || n.parent == nil {
		return
	}
	parent := n.parent
	previous, next := siblings(n)
	t.detach(n)
	t.detached[n.id] = n
	t.dirty = true
	t.emitMutation(Mutation{Type: "childList", Target: parent, Removed: []*Node{n}, PreviousSibling: previous, NextSibling: next})
}

// Release frees a detached subtree. The bridge calls it when no JS wrapper
// for any node in the subtree is alive any more (or never existed).
func (t *Tree) Release(n *Node) {
	if n.parent != nil || n == t.root {
		return // attached nodes are never released
	}
	delete(t.detached, n.id)
	t.release(n)
}

// Attached reports whether n is reachable from the document root.
func (t *Tree) Attached(n *Node) bool { return t.RootOf(n) == t.root }

// RootOf returns the topmost ancestor: the document root, or a detached root.
func (t *Tree) RootOf(n *Node) *Node {
	for n.parent != nil {
		n = n.parent
	}
	return n
}

// Live is the number of nodes counted against the budget, attached or not.
func (t *Tree) Live() int { return t.live }

// Walk visits n and its descendants in document order.
func Walk(n *Node, fn func(*Node)) {
	fn(n)
	for _, c := range n.children {
		Walk(c, fn)
	}
}

func (t *Tree) AddListener(n *Node, typ string) {
	if n.listeners == nil {
		n.listeners = map[string]struct{}{}
	}
	if _, had := n.listeners[typ]; !had {
		n.listeners[typ] = struct{}{}
		// An element that can be clicked needs an anchor name in the raster.
		if _, hasID := n.Attr("id"); !hasID && n.kind == Element {
			t.dirty = true
		}
	}
}

func (t *Tree) RemoveListener(n *Node, typ string) { delete(n.listeners, typ) }

// ByID resolves an id attribute (including synthetic ones from Serialize).
func (t *Tree) ByID(id string) *Node {
	if n, ok := parseSynthetic(id); ok {
		return t.nodes[n]
	}
	return t.find(t.root, func(n *Node) bool { v, ok := n.Attr("id"); return ok && v == id })
}

// maxSelectorWork bounds the nodes one Query may visit inside :has()
// subtree searches, on top of the single tree walk; past it the query fails
// instead of running quadratically on a deep page.
const maxSelectorWork = 1 << 20

// queryState carries the per-Query :has() work budget to predicates.
type queryState struct {
	work     int
	exceeded bool
}

// Query supports tag, #id, .class, attribute presence and value suffixes
// ([a], [a=v], [a~=v], [a|=v], [a^=v], [a$=v], [a*=v]), a trailing
// :has(relative selector) with descendant or leading-child combinators,
// descendant combinations and comma-separated selector lists. Anything
// richer throws in JS. Results are in tree order and never repeat an element.
func (t *Tree) Query(from *Node, selector string, all bool) ([]*Node, error) {
	groups, splitErr := selectorGroups(selector)
	if splitErr != nil {
		return nil, errors.New("ntbrowser/dom: unsupported selector")
	}
	q := &queryState{}
	chains := make([][]func(*Node) bool, 0, len(groups))
	for _, group := range groups {
		preds, err := compoundChain(group, q, true)
		if err != nil {
			return nil, err
		}
		chains = append(chains, preds)
	}
	matches := func(c *Node) bool {
		for _, preds := range chains {
			if preds[len(preds)-1](c) && ancestorsMatch(c, preds[:len(preds)-1], from) {
				return true
			}
		}
		return false
	}
	var out []*Node
	var walk func(n *Node) bool
	walk = func(n *Node) bool {
		for _, c := range n.children {
			if c.kind == Element && matches(c) {
				out = append(out, c)
				if !all {
					return true
				}
			}
			if walk(c) {
				return true
			}
		}
		return false
	}
	walk(from)
	if q.exceeded {
		return nil, fmt.Errorf("%w: selector work over %d nodes", ErrLimit, maxSelectorWork)
	}
	return out, nil
}

// compoundChain parses one whitespace-separated descendant chain of at most
// eight compounds. allowHas admits :has() only at the top level.
func compoundChain(group string, q *queryState, allowHas bool) ([]func(*Node) bool, error) {
	parts, splitErr := selectorParts(group)
	if splitErr != nil || len(parts) == 0 || len(parts) > 8 {
		return nil, errors.New("ntbrowser/dom: unsupported selector")
	}
	preds := make([]func(*Node) bool, 0, len(parts))
	for _, p := range parts {
		pred, err := compoundPredicate(p, q, allowHas)
		if err != nil {
			return nil, err
		}
		preds = append(preds, pred)
	}
	return preds, nil
}

// compoundPredicate handles an optional trailing :has(...) on a compound and
// delegates the rest to simplePredicate.
func compoundPredicate(p string, q *queryState, allowHas bool) (func(*Node) bool, error) {
	at := strings.Index(p, ":has(")
	if at < 0 {
		if strings.Contains(p, "(") {
			return nil, fmt.Errorf("ntbrowser/dom: unsupported selector %q", p)
		}
		return simplePredicate(p)
	}
	if !allowHas || !strings.HasSuffix(p, ")") || strings.Contains(p[at+5:], ":has(") {
		return nil, fmt.Errorf("ntbrowser/dom: unsupported selector %q", p)
	}
	base := p[:at]
	if base == "" {
		base = "*"
	}
	basePred, err := simplePredicate(base)
	if err != nil {
		return nil, err
	}
	arg := strings.Trim(p[at+5:len(p)-1], " \t\r\n\f")
	child := false
	if rest, ok := strings.CutPrefix(arg, ">"); ok {
		child = true
		arg = strings.TrimLeft(rest, " \t\r\n\f")
	}
	if arg == "" || strings.ContainsAny(arg, ",>+~()") {
		return nil, fmt.Errorf("ntbrowser/dom: unsupported selector %q", p)
	}
	chain, err := compoundChain(arg, q, false)
	if err != nil {
		return nil, err
	}
	h := hasTest{preds: chain, child: child, q: q}
	return func(n *Node) bool { return basePred(n) && h.match(n) }, nil
}

// hasTest is a :has() argument: a descendant chain that must occur inside
// the candidate, anchored on a direct child when the argument began with >.
type hasTest struct {
	preds []func(*Node) bool
	child bool
	q     *queryState
}

func (h hasTest) match(root *Node) bool {
	last := h.preds[len(h.preds)-1]
	var walk func(n *Node) bool
	walk = func(n *Node) bool {
		for _, c := range n.children {
			h.q.work++
			if h.q.work > maxSelectorWork {
				h.q.exceeded = true
				return false
			}
			if c.kind == Element && last(c) && h.chainWithin(c, root) {
				return true
			}
			if walk(c) {
				return true
			}
			if h.q.exceeded {
				return false
			}
		}
		return false
	}
	return walk(root)
}

// chainWithin checks the ancestors of c, strictly below root, against the
// chain's earlier compounds. With a leading child combinator the first
// compound must match root's direct child on that path.
func (h hasTest) chainWithin(c, root *Node) bool {
	earlier := h.preds[:len(h.preds)-1]
	if !h.child {
		return ancestorsMatch(c, earlier, root)
	}
	// Find root's direct child on the path to c.
	top := c
	for top.parent != nil && top.parent != root {
		top = top.parent
	}
	if top.parent != root {
		return false
	}
	if len(earlier) == 0 {
		return c == top
	}
	if !earlier[0](top) || top == c {
		return false
	}
	return ancestorsMatch(c, earlier[1:], top)
}

func ancestorsMatch(n *Node, preds []func(*Node) bool, stop *Node) bool {
	i := len(preds) - 1
	for a := n.parent; a != nil && a != stop && i >= 0; a = a.parent {
		if preds[i](a) {
			i--
		}
	}
	return i < 0
}

func simplePredicate(p string) (func(*Node) bool, error) {
	if first := strings.IndexByte(p, '['); first >= 0 {
		base := p[:first]
		if base == "" {
			base = "*"
		}
		pred, err := simplePredicate(base)
		if err != nil {
			return nil, err
		}
		var attrs []attrTest
		rest := p[first:]
		for len(rest) > 0 {
			if rest[0] != '[' || len(attrs) >= 8 {
				return nil, fmt.Errorf("ntbrowser/dom: unsupported selector %q", p)
			}
			end := closingBracket(rest)
			if end < 0 {
				return nil, fmt.Errorf("ntbrowser/dom: unsupported selector %q", p)
			}
			test, ok := parseAttrTest(rest[1:end])
			if !ok {
				return nil, fmt.Errorf("ntbrowser/dom: unsupported selector %q", p)
			}
			attrs = append(attrs, test)
			rest = rest[end+1:]
		}
		return func(n *Node) bool {
			if !pred(n) {
				return false
			}
			for _, a := range attrs {
				if !a.match(n) {
					return false
				}
			}
			return true
		}, nil
	}
	if strings.ContainsAny(p, "[]()") {
		return nil, fmt.Errorf("ntbrowser/dom: unsupported selector %q", p)
	}

	// A compound: optional tag or *, then any number of #id and .class
	// tests (div.box, a#top.active, .one.two). Each piece must be a name.
	rest := p
	var tests []func(*Node) bool
	if cut := strings.IndexAny(rest, "#."); cut != 0 {
		head := rest
		if cut > 0 {
			head, rest = rest[:cut], rest[cut:]
		} else {
			rest = ""
		}
		switch {
		case head == "*":
		case validName(head) && !strings.ContainsAny(head, ":|"): // pseudo-classes and namespaces are not supported
			tag := strings.ToLower(head)
			tests = append(tests, func(n *Node) bool { return n.tag == tag })
		default:
			return nil, fmt.Errorf("ntbrowser/dom: unsupported selector %q", p)
		}
	}
	for len(rest) > 0 && len(tests) < 8 {
		kind := rest[0]
		end := strings.IndexAny(rest[1:], "#.")
		var name string
		if end < 0 {
			name, rest = rest[1:], ""
		} else {
			name, rest = rest[1:end+1], rest[end+1:]
		}
		if !validName(name) || strings.ContainsAny(name, ":|") {
			return nil, fmt.Errorf("ntbrowser/dom: unsupported selector %q", p)
		}
		if kind == '#' {
			tests = append(tests, func(n *Node) bool { v, ok := n.Attr("id"); return ok && v == name })
		} else {
			tests = append(tests, func(n *Node) bool { return hasClass(n, name) })
		}
	}
	if len(rest) > 0 {
		return nil, fmt.Errorf("ntbrowser/dom: unsupported selector %q", p)
	}
	return func(n *Node) bool {
		for _, test := range tests {
			if !test(n) {
				return false
			}
		}
		return true
	}, nil
}

// attrTest is one bracketed attribute condition: presence when op is empty,
// otherwise a CSS attribute value comparison. Values are case-sensitive.
type attrTest struct {
	name, op, value string
}

func (a attrTest) match(n *Node) bool {
	v, ok := n.Attr(a.name)
	if !ok {
		return false
	}
	switch a.op {
	case "":
		return true
	case "=":
		return v == a.value
	case "~=":
		if a.value == "" || strings.ContainsAny(a.value, " \t\r\n\f") {
			return false
		}
		for _, word := range strings.Fields(v) {
			if word == a.value {
				return true
			}
		}
		return false
	case "|=":
		return v == a.value || strings.HasPrefix(v, a.value+"-")
	case "^=":
		return a.value != "" && strings.HasPrefix(v, a.value)
	case "$=":
		return a.value != "" && strings.HasSuffix(v, a.value)
	case "*=":
		return a.value != "" && strings.Contains(v, a.value)
	}
	return false
}

// closingBracket finds the ']' that ends the condition opened at rest[0],
// skipping brackets inside a quoted value; -1 when the condition never closes.
func closingBracket(rest string) int {
	var quote byte
	for i := 1; i < len(rest); i++ {
		c := rest[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == ']':
			return i
		}
	}
	return -1
}

// parseAttrTest reads the inside of one bracket pair: a name, optionally an
// operator and a value that is either a quoted string or an identifier.
// Backslash escapes, namespaces and case flags are not supported.
func parseAttrTest(body string) (attrTest, bool) {
	const space = " \t\r\n\f"
	body = strings.Trim(body, space)
	eq := strings.IndexByte(body, '=')
	if eq < 0 {
		if !validName(body) || strings.ContainsAny(body, ":|") {
			return attrTest{}, false
		}
		return attrTest{name: strings.ToLower(body)}, true
	}
	name, op := body[:eq], "="
	if eq > 0 && strings.IndexByte("~|^$*", body[eq-1]) >= 0 {
		name, op = body[:eq-1], body[eq-1:eq+1]
	}
	name = strings.TrimRight(name, space)
	if !validName(name) || strings.ContainsAny(name, ":|") {
		return attrTest{}, false
	}
	value := strings.TrimLeft(body[eq+1:], space)
	switch {
	case value == "":
		return attrTest{}, false
	case value[0] == '"' || value[0] == '\'':
		if len(value) < 2 || value[len(value)-1] != value[0] || strings.ContainsAny(value[1:len(value)-1], "\\\n") {
			return attrTest{}, false
		}
		value = value[1 : len(value)-1]
	case !validName(value) || strings.ContainsAny(value, ":|"):
		return attrTest{}, false
	}
	return attrTest{name: strings.ToLower(name), op: op, value: value}, true
}

func hasClass(n *Node, cls string) bool {
	v, _ := n.Attr("class")
	for _, c := range strings.Fields(v) {
		if c == cls {
			return true
		}
	}
	return false
}

// ---- serialization: the frame handed to nthtml ----

// Serialize writes the tree as HTML that nthtml's document.Parse accepts.
// <script> nodes are omitted from rendering. Elements with listeners,
// and focusable elements, that have no id get a synthetic id so nthtml's
// Result.Anchors / Box.Anchor can be mapped back to NodeIDs for hit testing
// and focus. Form controls are written with their current state (forms.go).
// Text and attribute values are escaped by x/net/html, so a script cannot
// smuggle markup through textContent or setAttribute.
func (t *Tree) Serialize() string {
	var b strings.Builder
	seen := map[string]bool{} // hrefs whose <style> twin was written in this call
	t.ids = map[string]bool{} // ids already written in this call (frameID)
	t.write(&b, t.root, seen, false)
	t.ids = nil
	t.releaseOrphanStyles(seen)
	return b.String()
}

// inSVG is true inside an <svg> subtree (SVG namespace), where names are
// written with their SVG spelling (svgcase.go).
func (t *Tree) write(b *strings.Builder, n *Node, seen map[string]bool, inSVG bool) {
	switch n.kind {
	case Text:
		if n.parent != nil && (n.parent.tag == "style") {
			b.WriteString(sanitizeCSS(n.text)) // style text is raw, not escaped
			return
		}
		b.WriteString(html.EscapeString(n.text))
		return
	case Element:
		if f, ok := t.frames[n.id]; ok && n.tag == "iframe" {
			t.writeFrame(b, n, f)
			return
		}
		if n.tag == "noscript" || n.tag == "script" {
			return // scripting is on: <noscript> content is never shown (decision 18)
		}
		inSVG = inSVG || n.tag == "svg"
		tag := svgName(svgTagNames, n.tag, inSVG)
		if n.tag != "#document" {
			b.WriteByte('<')
			b.WriteString(tag)
			own, _ := n.Attr("id")
			id := frameID(n, t.ids[own])
			if own != "" {
				t.ids[own] = true
			}
			for _, a := range n.attrs {
				if skipState(n, a.Name) || (a.Name == "id" && a.Value != id) {
					continue // a replaced id is written below, as frameID decided
				}
				b.WriteByte(' ')
				b.WriteString(svgName(svgAttrNames, a.Name, inSVG))
				b.WriteString(`="`)
				b.WriteString(html.EscapeString(a.Value))
				b.WriteByte('"')
			}
			writeState(b, n)
			if id != "" && id != own {
				b.WriteString(` id="`)
				b.WriteString(html.EscapeString(id))
				b.WriteByte('"')
			}
			b.WriteByte('>')
		}
		if n.tag == "textarea" && n.form != nil && n.form.dirtyValue {
			if strings.HasPrefix(n.form.value, "\n") {
				b.WriteByte('\n') // the parser drops one leading newline in <textarea>
			}
			b.WriteString(html.EscapeString(n.form.value))
			b.WriteString("</textarea>")
			return
		}
		childSVG := inSVG && n.tag != "foreignobject" // foreignObject content is HTML again
		for _, c := range n.children {
			t.write(b, c, seen, childSVG)
		}
		if n.tag != "#document" && !voidElement(n.tag) {
			b.WriteString("</")
			b.WriteString(tag)
			b.WriteByte('>')
		}
		if n.tag == "link" {
			t.writeStyleTwin(b, n, seen)
		}
	}
}

// sanitizeCSS only prevents a </style> break-out, for inline <style> text and
// for injected external sheets alike; nthtml's own CSS parser enforces rule
// budgets, and url() resources reach it only through Options.BackgroundImages.
func sanitizeCSS(s string) string { return strings.ReplaceAll(s, "</", "<\\/") }

const syntheticPrefix = "nt-h-"

func syntheticID(id NodeID) string { return fmt.Sprintf("%s%d", syntheticPrefix, int(id)) }

func parseSynthetic(s string) (NodeID, bool) {
	if !strings.HasPrefix(s, syntheticPrefix) {
		return 0, false
	}
	var n int
	if _, err := fmt.Sscanf(s[len(syntheticPrefix):], "%d", &n); err != nil {
		return 0, false
	}
	return NodeID(n), true
}

// ---- internals ----

func (t *Tree) attach(p, c *Node) {
	c.parent = p
	p.children = append(p.children, c)
	delete(t.detached, c.id)
}

func (t *Tree) detach(c *Node) {
	p := c.parent
	for i, s := range p.children {
		if s == c {
			p.children = append(p.children[:i], p.children[i+1:]...)
			break
		}
	}
	c.parent = nil
}

func (t *Tree) release(n *Node) {
	delete(t.nodes, n.id)
	delete(t.detached, n.id)
	t.live--
	t.bytes -= nodeCost(n)
	for _, c := range n.children {
		t.release(c)
	}
}

// Bytes is the accounted upper bound of len(Serialize()).
func (t *Tree) Bytes() int { return t.bytes }

func (t *Tree) find(from *Node, pred func(*Node) bool) *Node {
	if pred(from) {
		return from
	}
	for _, c := range from.children {
		if r := t.find(c, pred); r != nil {
			return r
		}
	}
	return nil
}

func depthOf(n *Node) int {
	d := 0
	for ; n != nil; n = n.parent {
		d++
	}
	return d
}

func height(n *Node) int {
	h := 0
	for _, c := range n.children {
		h = max(h, height(c))
	}
	return h + 1
}

func validName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case i > 0 && (r >= '0' && r <= '9' || r == '-' || r == '_' || r == ':'):
		default:
			return false
		}
	}
	return true
}

func voidElement(tag string) bool {
	switch tag {
	case "br", "hr", "img", "meta", "link", "input", "col", "wbr":
		return true
	}
	return false
}

// DetachedRoots lists subtree roots removed from the document and not released.
func (t *Tree) DetachedRoots() []*Node {
	out := make([]*Node, 0, len(t.detached))
	for _, n := range t.detached {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// Listeners lists nodes that registered a listener of typ, in document order.
// The loop uses it to decide whether a terminal event is worth forwarding.
func (t *Tree) Listeners(typ string) []*Node {
	var out []*Node
	for _, n := range t.nodes {
		if n.HasListener(typ) {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// contentBytes excludes host-only sheets only when a separate allowance exists.
func (t *Tree) contentBytes() int {
	if t.limits.ExternalStyleBytes > 0 {
		return t.bytes - t.styleBytes
	}
	return t.bytes
}
