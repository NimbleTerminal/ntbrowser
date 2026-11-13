// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package loop

import (
	"errors"
	"image"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"nimbleterminal.dev/nthtml/document"

	"nimbleterminal.dev/ntbrowser/content"
	"nimbleterminal.dev/ntbrowser/dom"
	"nimbleterminal.dev/ntbrowser/jsvm"
)

// ControlInfo describes the focused element to the host. Label, Options and
// Value are page-controlled text: the host passes anything it shows through
// app.clean, and never shows Value for a password.
type ControlInfo struct {
	Kind        string // "", "link", "text", "password", "textarea", "checkbox", "radio", "select", "button"
	Label       string // accessible name, ≤ 80 runes
	Value       string // current value of a text kind (the editor's seed)
	MaxLength   int    // characters; 0 = none
	Options     []string
	OptionIndex []int // OptionIndex[i]: Options[i]'s index among all the select's options (what Choose takes)
	Selected    []int // indices into Options
	Multiple    bool
	ReadOnly    bool
}

type (
	// FocusMsg reports a focus change. Source is the element's anchor id
	// ("" = focus cleared). User is true when a reader action moved focus
	// (FocusNext, a click); false for script focus() and autofocus, which
	// must never start text entry (forms design decision 4).
	FocusMsg struct {
		Source  string
		Control ControlInfo
		User    bool
	}
	// ValueMsg reports a script write to the focused control's value.
	ValueMsg struct{ Source, Value string }
)

// FocusNext moves focus to the next (or previous) element in the page's
// focus order and reports it as a reader action.
func (l *Loop) FocusNext(backward bool) { l.FocusNextAt(0, backward) }

// FocusNextAt rejects focus traversal queued against a superseded DOM/frame.
func (l *Loop) FocusNextAt(gen uint64, backward bool) {
	l.tryPost(func() {
		page := l.page
		if page == nil || page.killed || gen != 0 && (!l.FrameCurrent(gen) || page.Tree.Dirty()) {
			return
		}
		order := l.focusOrder(page)
		if len(order) == 0 {
			return
		}
		cur := -1
		for i, n := range order {
			if n.ID() == page.Sandbox.Active() {
				cur = i
				break
			}
		}
		var next int
		switch {
		case cur < 0 && backward:
			next = len(order) - 1
		case cur < 0:
			next = 0
		case backward:
			next = (cur - 1 + len(order)) % len(order)
		default:
			next = (cur + 1) % len(order)
		}
		l.userFocus(page, order[next])
	})
}

// focusOrder is HTML's sequential focus order over focusable elements with a
// box in the last frame: positive tabindex ascending, then tree order;
// tabindex < 0 is focusable by click or script only.
func (l *Loop) focusOrder(page *Page) []*dom.Node {
	visible := map[string]bool{}
	for _, b := range page.boxes {
		if b.Anchor != "" {
			visible[b.Anchor] = true
		}
	}
	type entry struct {
		n   *dom.Node
		tab int
	}
	var entries []entry
	anchor := page.Tree.Anchors()
	dom.Walk(page.Tree.Root(), func(n *dom.Node) {
		if !dom.Focusable(n) || !visible[anchor(n)] {
			return
		}
		tab := 0
		if raw, ok := n.Attr("tabindex"); ok {
			v, err := strconv.Atoi(strings.TrimSpace(raw))
			if err == nil {
				tab = v
			}
		}
		if tab >= 0 {
			entries = append(entries, entry{n, tab})
		}
	})
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i].tab, entries[j].tab
		if (a > 0) != (b > 0) {
			return a > 0
		}
		return a > 0 && a < b
	})
	out := make([]*dom.Node, len(entries))
	for i, e := range entries {
		out[i] = e.n
	}
	return out
}

// userFocus focuses n (nil clears) on the reader's behalf and reports it,
// unless a click on nothing focusable left focus as it was.
func (l *Loop) userFocus(page *Page, n *dom.Node) {
	var id dom.NodeID
	if n != nil {
		id = n.ID()
	}
	before := page.lastFocus
	if before != id {
		l.changeOnBlur(page, before)
	}
	l.report(ignoreDisabled(page.Sandbox.SetFocus(id)))
	page.lastFocus = page.Sandbox.Active()
	if moved := page.lastFocus != before; n != nil || moved {
		l.sendFocus(page, true, moved)
	}
}

// changeOnBlur fires change on a text control focus is leaving when its
// value differs from the one it had when focused or last committed, as
// browsers do on blur (the host's editor only commits on Tab, Esc, Enter).
func (l *Loop) changeOnBlur(page *Page, id dom.NodeID) {
	n := page.Tree.Node(id)
	if n == nil || !page.Tree.Attached(n) || !dom.IsTextControl(n) || n.Value() == page.focusValue {
		return
	}
	page.focusValue = n.Value()
	l.fire(page, n, "change")
}

// clickControl gives a clicked label or control its activation behavior and
// reports whether it handled the click: a label activates its control; a
// checkbox, radio or button is focused and activated. Text fields, selects
// and links take ClickAt's ordinary path (focus, click, navigation).
func (l *Loop) clickControl(page *Page, target *dom.Node) bool {
	if target.Tag() == "label" {
		prevented, err := page.Sandbox.Dispatch(jsvm.Event{Type: "click", Target: target.ID()})
		l.report(ignoreDisabled(err))
		c := dom.LabeledControl(page.Tree, target)
		if prevented || c == nil || !dom.Focusable(c) {
			return true
		}
		l.userFocus(page, c)
		switch controlKind(c) {
		case "checkbox", "radio", "button":
			l.activate(page, c)
		}
		return true
	}
	switch controlKind(target) {
	case "checkbox", "radio", "button", "video", "summary":
		l.userFocus(page, target)
		l.activate(page, target)
		return true
	}
	return false
}

// videoSource is a <video>'s src, else its first <source> that is a video.
func videoSource(page *Page, n *dom.Node) string {
	if raw, ok := n.Attr("src"); ok {
		return document.ResolveURL(page.Base, raw)
	}
	for _, c := range n.Children() {
		if c.Tag() != "source" {
			continue
		}
		raw, _ := c.Attr("src")
		typ, _ := c.Attr("type")
		href := document.ResolveURL(page.Base, raw)
		if href != "" && content.Classify(typ, href) == content.Video {
			return href
		}
	}
	return ""
}

// sendFocus reports the current focus; moved (focus changed element) resets
// the change baseline, a repeated report of the same element does not.
func (l *Loop) sendFocus(page *Page, user, moved bool) {
	n := page.Tree.Node(page.Sandbox.Active())
	if n == nil {
		page.focusValue = ""
		l.send(FocusMsg{User: user})
		return
	}
	if moved {
		page.focusValue = n.Value()
	}
	source := dom.AnchorID(page.Tree, n)
	if l.cfg.Chunked && moved {
		visible := false
		if page.result != nil {
			_, visible = page.result.AnchorBounds(source)
		}
		if !visible {
			page.chunkReq.anchor, page.chunkReq.bottom = source, false
			page.chunkReq.seq++
			l.pendingFrame = true
		}
	}
	l.send(FocusMsg{Source: source, Control: controlInfo(page.Tree, n), User: user})
}

// reconcileFocus runs after every task: a focused element that was removed
// or disabled loses focus, and a focus change made by script (focus(),
// blur(), autofocus) is reported with User false.
func (l *Loop) reconcileFocus(page *Page) {
	if page.killed {
		return
	}
	act := page.Sandbox.Active()
	if n := page.Tree.Node(act); act != 0 && (n == nil || !page.Tree.Attached(n) || !dom.Focusable(n)) {
		l.report(ignoreDisabled(page.Sandbox.SetFocus(0)))
		act = page.Sandbox.Active()
	}
	if act != page.lastFocus {
		l.changeOnBlur(page, page.lastFocus) // script moved focus away from an edited field
		page.lastFocus = act
		l.sendFocus(page, false, true)
	}
}

// autofocus focuses the first visible [autofocus] element once, after the
// page's scripts have run and its first frame has boxes, unless something is
// focused already. reconcileFocus reports it as script focus.
func (l *Loop) autofocus(page *Page) {
	if page.autofocusDone || !page.loaded || len(page.boxes) == 0 {
		return
	}
	page.autofocusDone = true
	if page.Sandbox.Active() != 0 {
		return
	}
	visible := map[string]bool{}
	for _, b := range page.boxes {
		visible[b.Anchor] = true
	}
	anchor := page.Tree.Anchors()
	var target *dom.Node
	dom.Walk(page.Tree.Root(), func(n *dom.Node) {
		if _, ok := n.Attr("autofocus"); ok && target == nil && dom.Focusable(n) && visible[anchor(n)] {
			target = n
		}
	})
	if target != nil {
		l.report(ignoreDisabled(page.Sandbox.SetFocus(target.ID())))
	}
}

func ignoreDisabled(err error) error {
	if errors.Is(err, jsvm.ErrDisabled) {
		return nil
	}
	return err
}

// Activate performs the default action of the focused element (Enter or
// Space in browse mode): follow a link, toggle a checkbox or radio, press a
// button. Text fields and selects are the host's (edit mode, chooser).
func (l *Loop) Activate() { l.ActivateAt(0) }

// ActivateAt rejects activation queued against a superseded DOM/frame.
func (l *Loop) ActivateAt(gen uint64) {
	l.tryPost(func() {
		page := l.page
		if page == nil || page.killed || gen != 0 && (!l.FrameCurrent(gen) || page.Tree.Dirty()) {
			return
		}
		n := page.Tree.Node(page.Sandbox.Active())
		if n == nil || !page.Tree.Attached(n) {
			return
		}
		l.activate(page, n)
	})
}

func (l *Loop) activate(page *Page, n *dom.Node) {
	switch kind := controlKind(n); kind {
	case "summary":
		prevented, err := page.Sandbox.Dispatch(jsvm.Event{Type: "click", Target: n.ID()})
		l.report(ignoreDisabled(err))
		if !prevented && page == l.page && page.Tree.Attached(n) {
			if parent := n.Parent(); parent != nil && parent.Tag() == "details" {
				for _, child := range parent.Children() {
					if child.Tag() == "summary" {
						if child != n {
							return
						}
						break
					}
				}
				if _, open := parent.Attr("open"); open {
					page.Tree.RemoveAttr(parent, "open")
				} else {
					l.report(page.Tree.SetAttr(parent, "open", ""))
				}
			}
		}

	case "link":
		href := anchorHref(page, n)
		prevented, err := page.Sandbox.Dispatch(jsvm.Event{Type: "click", Target: n.ID(), Href: href})
		l.report(ignoreDisabled(err))
		href = anchorHref(page, n)
		if !prevented && href != "" {
			l.send(NavigateMsg{Href: href})
		}
	case "checkbox", "radio":
		l.toggleCheck(page, n)
	case "button":
		prevented, err := page.Sandbox.Dispatch(jsvm.Event{Type: "click", Target: n.ID()})
		l.report(ignoreDisabled(err))
		if !prevented {
			l.buttonDefault(page, n)
		}
	case "video":
		// Only a reader action gets here; script has no play().
		if href := videoSource(page, n); href != "" {
			l.send(NavigateMsg{Href: href})
		}
	case "text", "password", "textarea", "select":
		// The host edits or chooses; nothing happens in the page.
	default:
		_, err := page.Sandbox.Dispatch(jsvm.Event{Type: "click", Target: n.ID()})
		l.report(ignoreDisabled(err))
	}
}

// buttonDefault is a button's activation behavior after an unprevented
// click: a submit button submits its form, a reset button resets it.
func (l *Loop) buttonDefault(page *Page, n *dom.Node) {
	if page != l.page || !page.Tree.Attached(n) {
		return
	}
	if n.Tag() == "button" && !dom.Disabled(n) {
		typ, _ := n.Attr("type")
		if strings.EqualFold(typ, "button") || dom.FormOwner(n, page.Tree) == nil {
			target, has := n.Attr("popovertarget")
			action, _ := n.Attr("popovertargetaction")
			if command, ok := n.Attr("commandfor"); ok {
				target, has = command, true
				kind, _ := n.Attr("command")
				switch strings.ToLower(kind) {
				case "show-popover":
					action = "show"
				case "hide-popover":
					action = "hide"
				case "toggle-popover":
					action = "toggle"
				default:
					return
				}
			}
			if has {
				if dest := page.Tree.ByID(target); dest != nil && dest.PopoverMode() != "" {
					if !page.killed && l.cfg.Profile.ScriptsOn() {
						l.report(ignoreDisabled(page.Sandbox.TogglePopover(dest.ID(), action)))
					} else {
						open := !dest.PopoverOpen()
						switch strings.ToLower(action) {
						case "show":
							open = true
						case "hide":
							open = false
						}
						if open {
							for _, peer := range page.Tree.PopoverPeers(dest) {
								page.Tree.SetPopoverOpen(peer, false)
							}
						}
						page.Tree.SetPopoverOpen(dest, open)
					}
				}
				return
			}
		}
	}
	form := dom.FormOwner(n, page.Tree)
	if form == nil {
		return
	}
	typ := dom.InputType(n)
	if n.Tag() == "button" {
		typ, _ = n.Attr("type")
		typ = strings.ToLower(strings.TrimSpace(typ))
		if typ != "button" && typ != "reset" {
			typ = "submit"
		}
	}
	switch typ {
	case "submit", "image":
		l.submit(page, form, n, submitByReader)
	case "reset":
		l.reset(page, form)
	}
}

// Submission caps (forms design decision 17), and the most rounds of
// script-requested submissions or resets one task may run.
const (
	maxFormData   = 64 << 10
	maxSubmitURL  = 16 << 10
	maxFormPasses = 16
)

// ImplicitSubmit is Enter in a text field (decision 10): the form's default
// button is activated if it has one; otherwise the form submits only when
// it has exactly one field that blocks implicit submission. A field outside
// any form does nothing.
func (l *Loop) ImplicitSubmit(source string) {
	l.tryPost(func() {
		page := l.page
		if page == nil || page.killed {
			return
		}
		n := page.Tree.ByID(source)
		if n == nil || !page.Tree.Attached(n) {
			return
		}
		form := dom.FormOwner(n, page.Tree)
		if form == nil {
			return
		}
		if b := dom.DefaultButton(page.Tree, form); b != nil {
			if !dom.Disabled(b) {
				l.activate(page, b)
			}
			return
		}
		blocking := 0
		for _, c := range dom.FormControls(page.Tree, form) {
			if dom.BlocksImplicitSubmission(c) {
				blocking++
			}
		}
		if blocking == 1 {
			l.submit(page, form, nil, submitByReader)
		}
	})
}

// submission is who asked for a form to be submitted.
type submission uint8

const (
	submitByReader        submission = iota // a click or implicit submission: fires submit
	submitByScriptRequest                   // form.requestSubmit(): fires submit; the navigation is script's
	submitByScript                          // form.submit(): no submit event; the navigation is script's
)

// submit runs HTML's form submission for GET (decisions 10–13, 17): the
// submit event (except for form.submit(), submitByScript), then the
// method, password, size and action checks, then a NavigateMsg the host
// admits like a link. Refusals are reported without any value.
func (l *Loop) submit(page *Page, form, submitter *dom.Node, how submission) {
	if !page.Tree.Attached(form) {
		return
	}
	if how != submitByScript {
		page.firing[form.ID()] = true // requestSubmit() from a submit listener is a no-op (HTML)
		prevented, err := page.Sandbox.Dispatch(jsvm.Event{Type: "submit", Target: form.ID()})
		delete(page.firing, form.ID())
		l.report(ignoreDisabled(err))
		if prevented || page.killed || !page.Tree.Attached(form) {
			return
		}
	}
	method, _ := form.Attr("method")
	if submitter != nil {
		if m, ok := submitter.Attr("formmethod"); ok {
			method = m
		}
	}
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "", "get":
	case "dialog":
		for p := form.Parent(); p != nil; p = p.Parent() {
			if p.Tag() == "dialog" {
				page.Tree.RemoveAttr(p, "open")
				break
			}
		}
		return
	default:
		l.send(LogMsg{"warn", "POST forms are not supported yet"})
		return
	}
	entries := dom.FormDataSet(page.Tree, form, submitter)
	for _, c := range dom.FormControls(page.Tree, form) {
		if c.Tag() == "input" && dom.InputType(c) == "password" && c.Value() != "" && !dom.Disabled(c) {
			if name, _ := c.Attr("name"); name != "" {
				l.send(LogMsg{"warn", "not submitted: GET would put a password in the URL"})
				return
			}
		}
	}
	query := dom.Encode(entries)
	action, ok := form.Attr("action")
	if submitter != nil {
		if a, has := submitter.Attr("formaction"); has {
			action, ok = a, true
		}
	}
	if !ok || strings.TrimSpace(action) == "" {
		action = page.Base
	}
	href := document.ResolveURL(page.Base, action)
	u, err := url.Parse(href)
	if href == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "file") {
		l.send(LogMsg{"warn", "form action not openable"})
		return
	}
	u.RawQuery, u.Fragment, u.RawFragment = query, "", ""
	if len(query) > maxFormData || len(u.String()) > maxSubmitURL {
		l.send(LogMsg{"warn", "form too large to submit"})
		return
	}
	l.send(NavigateMsg{Href: u.String(), Form: true, Script: how != submitByReader})
}

// reset restores every control of form to its defaults after an
// unprevented reset event (decision 14).
func (l *Loop) reset(page *Page, form *dom.Node) {
	page.resetting[form.ID()] = true // reset() from a reset listener is a no-op (HTML's "locked for reset")
	prevented, err := page.Sandbox.Dispatch(jsvm.Event{Type: "reset", Target: form.ID()})
	delete(page.resetting, form.ID())
	l.report(ignoreDisabled(err))
	if prevented || page.killed {
		return
	}
	focused := page.Tree.Node(page.Sandbox.Active())
	before := ""
	if focused != nil {
		before = focused.Value()
	}
	for _, c := range dom.FormControls(page.Tree, form) {
		page.Tree.ResetControl(c)
		for _, o := range dom.Options(c) {
			page.Tree.ResetControl(o)
		}
	}
	if focused != nil && dom.IsTextControl(focused) && focused.Value() != before {
		page.focusValue = focused.Value()
		l.send(ValueMsg{Source: dom.AnchorID(page.Tree, focused), Value: focused.Value()})
	}
}

// toggleCheck is a checkbox's or radio's activation (decision 8): change the
// state, dispatch click, restore if it was prevented, else input and change.
func (l *Loop) toggleCheck(page *Page, n *dom.Node) {
	group := []*dom.Node{n}
	if dom.InputType(n) == "radio" {
		group = dom.RadioGroup(page.Tree, n)
	}
	before := make([]bool, len(group))
	for i, m := range group {
		before[i] = m.Checked()
	}
	if dom.InputType(n) == "radio" {
		page.Tree.CheckRadio(n)
	} else {
		page.Tree.SetChecked(n, !n.Checked())
	}
	prevented, err := page.Sandbox.Dispatch(jsvm.Event{Type: "click", Target: n.ID()})
	l.report(ignoreDisabled(err))
	if prevented {
		for i, m := range group {
			page.Tree.SetChecked(m, before[i])
		}
		return
	}
	changed := false
	for i, m := range group {
		changed = changed || m.Checked() != before[i]
	}
	if changed {
		l.fire(page, n, "input")
		l.fire(page, n, "change")
	}
}

// fire dispatches a bubbling, non-cancelable event when the page listens.
func (l *Loop) fire(page *Page, n *dom.Node, typ string) {
	if page.Sandbox.HasListeners(typ) {
		_, err := page.Sandbox.Dispatch(jsvm.Event{Type: typ, Target: n.ID()})
		l.report(ignoreDisabled(err))
	}
}

// SetValue sets a text control's value from the host's editor; final marks
// a commit (Tab, Esc, Enter), which fires change if the value differs from
// the one it had when focused or last committed. The value is never logged.
func (l *Loop) SetValue(source, v string, final bool) {
	l.tryPost(func() {
		page := l.page
		if page == nil || page.killed {
			return
		}
		n := page.Tree.ByID(source)
		if n == nil || !page.Tree.Attached(n) || !dom.IsTextControl(n) {
			return
		}
		if n.Value() != v {
			if err := page.Tree.SetValue(n, v); err != nil {
				l.send(LogMsg{"warn", "value not set: over the size limit"})
				return
			}
			l.fire(page, n, "input")
		}
		if final && n.Value() != page.focusValue {
			page.focusValue = n.Value()
			l.fire(page, n, "change")
		}
	})
}

// Choose applies the host's select chooser: index is among all the
// select's options (ControlInfo.OptionIndex); toggle flips it in a
// multiple select, otherwise it becomes the selection.
func (l *Loop) Choose(source string, index int, toggle bool) {
	l.tryPost(func() {
		page := l.page
		if page == nil || page.killed {
			return
		}
		sel := page.Tree.ByID(source)
		if sel == nil || sel.Tag() != "select" || !page.Tree.Attached(sel) || dom.Disabled(sel) {
			return
		}
		opts := dom.Options(sel)
		if index < 0 || index >= len(opts) || dom.OptionDisabled(opts[index]) {
			return
		}
		opt := opts[index]
		if toggle {
			page.Tree.SelectOption(sel, opt, !opt.Selected())
		} else {
			page.Tree.SelectOption(sel, opt, true)
		}
		l.fire(page, sel, "input")
		l.fire(page, sel, "change")
	})
}

// formHooks connects the sandbox's form methods to the loop. Hooks run while
// script runs: they only send messages or queue tasks.
func (l *Loop) formHooks(page *Page) jsvm.FormHooks {
	return jsvm.FormHooks{
		Submit: func(form, submitter dom.NodeID, fireEvent bool) {
			if fireEvent && page.firing[form] {
				return
			}
			page.formTasks = append(page.formTasks, func() {
				if f := page.Tree.Node(form); f != nil {
					how := submitByScript
					if fireEvent {
						how = submitByScriptRequest
					}
					l.submit(page, f, page.Tree.Node(submitter), how)
				}
			})
		},
		Reset: func(form dom.NodeID) {
			if page.resetting[form] {
				return
			}
			page.formTasks = append(page.formTasks, func() {
				if f := page.Tree.Node(form); f != nil {
					l.reset(page, f)
				}
			})
		},
		ValueChanged: func(id dom.NodeID) {
			if id != page.Sandbox.Active() {
				return
			}
			if n := page.Tree.Node(id); n != nil && dom.IsTextControl(n) {
				l.send(ValueMsg{Source: dom.AnchorID(page.Tree, n), Value: n.Value()})
			}
		},
	}
}

// clickTarget refines ClickAt's hit: the innermost identified element under
// the point, when it is a link, a control or a label, wins over the smallest
// anchored box (inline elements paint only a 1px anchor marker).
func (l *Loop) clickTarget(page *Page, area image.Rectangle) *dom.Node {
	if page.result == nil {
		return nil
	}
	p := area.Min.Add(area.Size().Div(2))
	id, ok := page.result.SourceAt(p)
	if !ok {
		return nil
	}
	n := page.Tree.ByID(id)
	if n == nil {
		return nil
	}
	for m := n; m != nil; m = m.Parent() {
		if m.Tag() == "label" || dom.Focusable(m) {
			return m
		}
	}
	return nil
}

func controlKind(n *dom.Node) string {
	switch n.Tag() {
	case "summary":
		return "summary"
	case "video":
		return "video"
	case "a":
		if _, ok := n.Attr("href"); ok {
			return "link"
		}
	case "textarea":
		return "textarea"
	case "select":
		return "select"
	case "button":
		return "button"
	case "input":
		switch typ := dom.InputType(n); typ {
		case "checkbox", "radio":
			return typ
		case "password":
			return "password"
		case "submit", "reset", "button", "image":
			return "button"
		}
		if dom.IsTextControl(n) {
			return "text"
		}
	}
	return ""
}

func controlInfo(t *dom.Tree, n *dom.Node) ControlInfo {
	c := ControlInfo{Kind: controlKind(n)}
	c.Label = accessibleName(t, n)
	switch c.Kind {
	case "text", "password", "textarea":
		c.Value = n.Value()
		if raw, ok := n.Attr("maxlength"); ok {
			if v, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && v > 0 {
				c.MaxLength = v
			}
		}
		_, c.ReadOnly = n.Attr("readonly")
	case "select":
		_, c.Multiple = n.Attr("multiple")
		selected := map[*dom.Node]bool{}
		for _, o := range dom.SelectedOptions(n) {
			selected[o] = true
		}
		for i, o := range dom.Options(n) {
			if dom.OptionDisabled(o) {
				continue
			}
			if selected[o] {
				c.Selected = append(c.Selected, len(c.Options))
			}
			c.Options = append(c.Options, truncate(strings.Join(strings.Fields(dom.TextContent(o)), " "), 80))
			c.OptionIndex = append(c.OptionIndex, i)
		}
	}
	return c
}

// accessibleName is a short label for the status line: the control's label
// text, aria-label, placeholder, name, or a button's or link's own text.
func accessibleName(t *dom.Tree, n *dom.Node) string {
	name := dom.LabelText(t, n)
	for _, attr := range []string{"aria-label", "placeholder", "title"} {
		if name == "" {
			name, _ = n.Attr(attr)
		}
	}
	if name == "" {
		switch controlKind(n) {
		case "button", "link":
			if n.Tag() == "input" {
				name, _ = n.Attr("value")
			} else {
				name = dom.TextContent(n)
			}
		}
	}
	if name == "" {
		name, _ = n.Attr("name")
	}
	return truncate(strings.Join(strings.Fields(name), " "), 80)
}

func truncate(s string, runes int) string {
	if utf8.RuneCountInString(s) <= runes {
		return s
	}
	return string([]rune(s)[:runes-1]) + "…"
}
