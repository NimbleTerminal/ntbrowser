// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package dom

import (
	"fmt"
	"strings"
)

// Injected stylesheets (subresources design, decisions 5 and 20). The loader
// fetches a <link rel=stylesheet> and hands the text to SetStyle; the
// serializer then writes a <style> twin right after the first qualifying
// <link> for that href, so the cascade order between external and inline
// sheets is preserved. The DOM has no node for the twin: scripts cannot see
// or edit it. Every byte the twin will emit is charged to TotalBytes by default, or to the
// explicitly configured host-only ExternalStyleBytes allowance.

type injectedStyle struct {
	css      string // already sanitized
	wrapCost int    // 10 + len(media) charged for the @media wrapper, 0 when none
}

// styleOpen/styleClose are the fixed bytes of a twin: "<style>" + "</style>".
const styleFixedCost = 15

// twinCost is the accounted upper bound of one twin: the fixed tags, the
// sanitized text, and the wrapper for the media the first qualifying <link>
// currently carries.
func twinCost(st *injectedStyle) int { return styleFixedCost + len(st.css) + st.wrapCost }

// stylesheetLink reports whether n is a <link> the loader should fetch and the
// serializer should twin: rel is a case-insensitive token list containing
// "stylesheet" and not "alternate", href is non-empty, type is absent, empty
// or text/css, and disabled is absent (decision 20).
func stylesheetLink(n *Node) (href, media string, ok bool) {
	if n.kind != Element || n.tag != "link" {
		return "", "", false
	}
	rel, _ := n.Attr("rel")
	hasSheet, hasAlt := false, false
	for _, tok := range strings.Fields(rel) {
		switch strings.ToLower(tok) {
		case "stylesheet":
			hasSheet = true
		case "alternate":
			hasAlt = true
		}
	}
	if !hasSheet || hasAlt {
		return "", "", false
	}
	href, _ = n.Attr("href")
	if strings.TrimSpace(href) == "" {
		return "", "", false
	}
	if typ, has := n.Attr("type"); has && strings.TrimSpace(typ) != "" && !strings.EqualFold(strings.TrimSpace(typ), "text/css") {
		return "", "", false
	}
	if _, disabled := n.Attr("disabled"); disabled {
		return "", "", false
	}
	media, _ = n.Attr("media")
	return href, strings.TrimSpace(media), true
}

// mediaWrap classifies a <link media> value: "" means no wrapper (absent or
// "all"); ok false means the sheet must not be emitted at all (a condition
// that cannot be expressed safely must not be applied unconditionally).
func mediaWrap(media string) (cond string, ok bool) {
	if media == "" || strings.EqualFold(media, "all") {
		return "", true
	}
	if len(media) > 1024 || strings.ContainsAny(media, "<>{}\\") {
		return "", false
	}
	return media, true
}

func wrapCostFor(media string) int {
	cond, ok := mediaWrap(media)
	if !ok || cond == "" {
		return 0
	}
	return 10 + len(cond)
}

// walk visits elements in document order, skipping <noscript> subtrees: a
// page under ntbrowser has scripting, so their content is never rendered.
func (t *Tree) walk(fn func(n *Node)) {
	var visit func(*Node)
	visit = func(n *Node) {
		if n.kind == Element && n.tag == "noscript" {
			return
		}
		fn(n)
		for _, c := range n.children {
			visit(c)
		}
	}
	visit(t.root)
}

// LinkedStyles lists the raw href of every stylesheet <link> in document
// order, deduplicated.
func (t *Tree) LinkedStyles() []string {
	var out []string
	seen := map[string]bool{}
	t.walk(func(n *Node) {
		if href, _, ok := stylesheetLink(n); ok && !seen[href] {
			seen[href] = true
			out = append(out, href)
		}
	})
	return out
}

// firstLinkMedia returns the media of the first stylesheet <link> for href.
func (t *Tree) firstLinkMedia(href string) (media string, found bool) {
	t.walk(func(n *Node) {
		if found {
			return
		}
		if h, m, ok := stylesheetLink(n); ok && h == href {
			media, found = m, true
		}
	})
	return media, found
}

// ImageSources lists the raw non-empty src of every <img> in document order,
// deduplicated.
func (t *Tree) ImageSources() []string {
	var out []string
	seen := map[string]bool{}
	t.walk(func(n *Node) {
		if n.kind != Element || n.tag != "img" {
			return
		}
		if src, ok := n.Attr("src"); ok && src != "" && !seen[src] {
			seen[src] = true
			out = append(out, src)
		}
	})
	return out
}

// StyleTexts returns the text of every <style> element in document order,
// for the loader's inline rule count.
func (t *Tree) StyleTexts() []string {
	var out []string
	t.walk(func(n *Node) {
		if n.kind == Element && n.tag == "style" {
			out = append(out, TextContent(n))
		}
	})
	return out
}

// SetStyle stores the loaded stylesheet for href. The text is sanitized
// against a </style> break-out and charged by delta: the same text and
// wrapper is a no-op, a change charges or credits the difference, "" removes
// the entry. On ErrLimit the previous text stays.
func (t *Tree) SetStyle(href, css string) error {
	if css == "" {
		if st, ok := t.styles[href]; ok {
			t.bytes -= twinCost(st)
			t.styleBytes -= twinCost(st)
			delete(t.styles, href)
			t.dirty = true
		}
		return nil
	}
	clean := sanitizeCSS(cleanText(css))
	media, _ := t.firstLinkMedia(href)
	next := &injectedStyle{css: clean, wrapCost: wrapCostFor(media)}
	old := 0
	if st, ok := t.styles[href]; ok {
		if st.css == next.css && st.wrapCost == next.wrapCost {
			return nil
		}
		old = twinCost(st)
	}
	if err := t.chargeStyle(twinCost(next) - old); err != nil {
		return err
	}
	t.styles[href] = next
	t.dirty = true
	return nil
}

// ClearStyles forgets every injected sheet and credits its bytes.
func (t *Tree) ClearStyles() {
	for href, st := range t.styles {
		t.bytes -= twinCost(st)
		t.styleBytes -= twinCost(st)
		delete(t.styles, href)
	}
	t.dirty = true
}

// MarkDirty asks for a frame without a tree change (a subresource arrived).
func (t *Tree) MarkDirty() { t.dirty = true }

// styleMediaDelta re-syncs the wrapper charge after a <link>'s attributes
// changed. It is called by SetAttr/RemoveAttr for link elements; it never
// fails, because a wrapper the budget cannot hold is simply not emitted
// (writeStyleTwin checks the charge before writing).
func (t *Tree) styleMediaDelta(n *Node) {
	if n.kind != Element || n.tag != "link" {
		return
	}
	for href, st := range t.styles {
		media, found := t.firstLinkMedia(href)
		if !found {
			continue
		}
		want := wrapCostFor(media)
		if want == st.wrapCost {
			continue
		}
		if err := t.chargeStyle(want - st.wrapCost); err != nil {
			continue // over budget: the twin stays accounted as before and is skipped if it would not fit
		}
		st.wrapCost = want
	}
}

// writeStyleTwin emits the <style> twin after a stylesheet <link>, once per
// href per Serialize call, and only when the emitted bytes are within what
// was charged.
func (t *Tree) writeStyleTwin(b *strings.Builder, n *Node, seen map[string]bool) {
	href, media, ok := stylesheetLink(n)
	if !ok || seen[href] {
		return
	}
	st, ok := t.styles[href]
	if !ok {
		return
	}
	seen[href] = true
	cond, usable := mediaWrap(media)
	if !usable {
		return
	}
	if cond != "" && 10+len(cond) > st.wrapCost {
		return // a media change the budget could not absorb: skip this frame rather than exceed the charge
	}
	b.WriteString("<style>")
	if cond != "" {
		b.WriteString("@media ")
		b.WriteString(cond)
		b.WriteString(" {")
	}
	b.WriteString(st.css)
	if cond != "" {
		b.WriteByte('}')
	}
	b.WriteString("</style>")
}

// releaseOrphanStyles credits sheets whose href no longer has a stylesheet
// <link> (a script changed link.href): called at the end of Serialize.
func (t *Tree) releaseOrphanStyles(seen map[string]bool) {
	for href, st := range t.styles {
		if seen[href] {
			continue
		}
		if _, found := t.firstLinkMedia(href); found {
			continue // present but not emitted (unusable media): keep it charged
		}
		t.bytes -= twinCost(st)
		t.styleBytes -= twinCost(st)
		delete(t.styles, href)
	}
}

func (t *Tree) chargeStyle(delta int) error {
	if t.limits.ExternalStyleBytes > 0 {
		if delta > 0 && t.styleBytes+delta > t.limits.ExternalStyleBytes {
			return fmt.Errorf("%w: external stylesheet size over %d bytes", ErrLimit, t.limits.ExternalStyleBytes)
		}
		t.bytes += delta
	} else if err := t.charge(delta); err != nil {
		return err
	}
	t.styleBytes += delta
	return nil
}
