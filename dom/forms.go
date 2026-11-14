// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package dom

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// formState is a control's current state (HTML's "value", "checkedness",
// "selectedness"). Attributes stay the defaults; the serializer writes the
// current state into each frame, so nthtml draws what the reader typed.
type formState struct {
	value                 string
	dirtyValue            bool
	checked, dirtyChecked bool
	selected, dirtySelect bool
}

// stateCost is what the serializer writes for n's state beyond its
// attributes; nodeCost includes it so Bytes stays an upper bound.
func stateCost(n *Node) int {
	f := n.form
	if f == nil {
		return 0
	}
	c := 0
	if f.dirtyValue {
		c += attrCost("value", f.value) + 1 // +1: textarea's leading-newline guard
	}
	if f.dirtyChecked && f.checked {
		c += attrCost("checked", "")
	}
	if f.dirtySelect && f.selected {
		c += attrCost("selected", "")
	}
	return c
}

func (n *Node) state() *formState {
	if n.form == nil {
		n.form = &formState{}
	}
	return n.form
}

// changeState applies fn to n's state and charges the serialized delta. With
// enforce, a delta over the budget fails and leaves the state unchanged.
func (t *Tree) changeState(n *Node, enforce bool, fn func(*formState)) error {
	before := stateCost(n)
	old := *n.state()
	fn(n.form)
	delta := stateCost(n) - before
	if enforce {
		if err := t.charge(delta); err != nil {
			*n.form = old
			return err
		}
	} else {
		t.bytes += delta // checked/selected markers: a few bytes per control
	}
	t.dirty = true
	return nil
}

// Value is the control's current value: the edited value, else the default
// (input: value attribute; textarea: its text; select: the value of its
// first selected option).
func (n *Node) Value() string {
	switch n.tag {
	case "input", "textarea":
		if n.form != nil && n.form.dirtyValue {
			return n.form.value
		}
		if n.tag == "textarea" {
			return TextContent(n)
		}
		v, _ := n.Attr("value")
		return v
	case "select":
		if opts := SelectedOptions(n); len(opts) > 0 {
			return OptionValue(opts[0])
		}
		return ""
	case "option":
		return OptionValue(n)
	}
	v, _ := n.Attr("value")
	return v
}

// SetValue sets an input's or textarea's current value. maxlength counts
// characters; the value is also capped at Limits.TextBytes bytes and charged
// to the tree budget (ErrLimit when either is exceeded, value unchanged).
func (t *Tree) SetValue(n *Node, v string) error {
	if n.kind != Element || (n.tag != "input" && n.tag != "textarea") {
		return errors.New("ntbrowser/dom: not a text control")
	}
	v = cleanText(v)
	if raw, ok := n.Attr("maxlength"); ok {
		if max, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && max >= 0 && utf8.RuneCountInString(v) > max {
			v = string([]rune(v)[:max])
		}
	}
	if len(v) > t.limits.TextBytes {
		return fmt.Errorf("%w: value over %d bytes", ErrLimit, t.limits.TextBytes)
	}
	return t.changeState(n, true, func(f *formState) { f.value, f.dirtyValue = v, true })
}

// Checked is a checkbox's or radio's current checkedness.
func (n *Node) Checked() bool {
	if n.form != nil && n.form.dirtyChecked {
		return n.form.checked
	}
	_, ok := n.Attr("checked")
	return ok
}

// SetChecked sets checkedness; radio-group exclusivity is the caller's.
func (t *Tree) SetChecked(n *Node, on bool) {
	_ = t.changeState(n, false, func(f *formState) { f.checked, f.dirtyChecked = on, true })
}

// Selected is an option's own selectedness (see SelectedOptions for the
// select-level default).
func (n *Node) Selected() bool {
	if n.form != nil && n.form.dirtySelect {
		return n.form.selected
	}
	_, ok := n.Attr("selected")
	return ok
}

// SetSelected sets an option's selectedness; single-select exclusivity is the
// caller's.
func (t *Tree) SetSelected(opt *Node, on bool) {
	_ = t.changeState(opt, false, func(f *formState) { f.selected, f.dirtySelect = on, true })
}

// ResetControl drops the current state, so the defaults show again.
func (t *Tree) ResetControl(n *Node) {
	if n.form == nil {
		return
	}
	t.bytes -= stateCost(n)
	n.form = nil
	t.dirty = true
	for _, c := range n.children { // a select resets its options
		if c.tag == "option" || c.tag == "optgroup" {
			t.ResetControl(c)
		}
	}
}

// Options lists a select's <option> elements in tree order, including those
// inside <optgroup>.
func Options(sel *Node) []*Node {
	var out []*Node
	for _, c := range sel.children {
		switch c.tag {
		case "option":
			out = append(out, c)
		case "optgroup":
			for _, o := range c.children {
				if o.tag == "option" {
					out = append(out, o)
				}
			}
		}
	}
	return out
}

// SelectedOptions is a select's selection: the options whose selectedness is
// set, or for a single select with none, its first enabled option.
func SelectedOptions(sel *Node) []*Node {
	var out []*Node
	opts := Options(sel)
	for _, o := range opts {
		if o.Selected() {
			out = append(out, o)
		}
	}
	if _, multi := sel.Attr("multiple"); !multi {
		if len(out) > 1 {
			out = out[len(out)-1:] // the last selected wins, as in parsing
		}
		if len(out) == 0 {
			for _, o := range opts {
				if !OptionDisabled(o) {
					return []*Node{o}
				}
			}
		}
	}
	return out
}

// OptionValue is an option's value attribute, else its text.
func OptionValue(o *Node) string {
	if v, ok := o.Attr("value"); ok {
		return v
	}
	return strings.Join(strings.Fields(TextContent(o)), " ")
}

// OptionDisabled reports a disabled option or one inside a disabled optgroup.
func OptionDisabled(o *Node) bool {
	if _, ok := o.Attr("disabled"); ok {
		return true
	}
	if p := o.parent; p != nil && p.tag == "optgroup" {
		_, ok := p.Attr("disabled")
		return ok
	}
	return false
}

// FormOwner is n's form: the <form> its form attribute names, else its
// nearest <form> ancestor, else nil.
func FormOwner(n *Node, t *Tree) *Node {
	if id, ok := n.Attr("form"); ok {
		if f := t.ByID(id); f != nil && f.tag == "form" {
			return f
		}
	}
	for p := n.parent; p != nil; p = p.parent {
		if p.tag == "form" {
			return p
		}
	}
	return nil
}

func isFormControl(tag string) bool {
	switch tag {
	case "button", "input", "select", "textarea":
		return true
	}
	return false
}

// Disabled reports a disabled form control, including one inside a disabled
// fieldset (other than inside that fieldset's first legend).
func Disabled(n *Node) bool {
	if !isFormControl(n.tag) {
		return false
	}
	if _, ok := n.Attr("disabled"); ok {
		return true
	}
	for child, p := n, n.parent; p != nil; child, p = p, p.parent {
		if p.tag != "fieldset" {
			continue
		}
		if _, ok := p.Attr("disabled"); !ok {
			continue
		}
		var legend *Node
		for _, c := range p.children {
			if c.tag == "legend" {
				legend = c
				break
			}
		}
		if legend == nil || child != legend {
			return true
		}
	}
	return false
}

// Focusable reports whether the reader or a script can focus n: links with an
// href, enabled controls (not hidden inputs), summary, and any [tabindex].
func Focusable(n *Node) bool {
	if n == nil || n.kind != Element || Disabled(n) {
		return false
	}
	if n.tag == "input" {
		if typ, _ := n.Attr("type"); strings.EqualFold(typ, "hidden") {
			return false
		}
	}
	if _, ok := n.Attr("tabindex"); ok {
		return true
	}
	switch n.tag {
	case "a":
		_, ok := n.Attr("href")
		return ok
	case "button", "input", "select", "textarea", "summary":
		return true
	case "video":
		return n.playable
	}
	return false
}

// MarkVideos makes the document's <video> elements with a source focusable.
// The loop calls it only when the profile opened video.
func (t *Tree) MarkVideos() {
	Walk(t.root, func(n *Node) {
		if n.tag != "video" {
			return
		}
		_, ok := n.Attr("src")
		for _, c := range n.children {
			ok = ok || c.tag == "source"
		}
		n.playable = ok
	})
}

// InputType is an input's type attribute, lower-cased, "text" when missing.
func InputType(n *Node) string {
	typ, ok := n.Attr("type")
	typ = strings.ToLower(strings.TrimSpace(typ))
	if !ok || typ == "" {
		return "text"
	}
	return typ
}

// IsTextControl reports a textarea or an input the reader types into.
func IsTextControl(n *Node) bool {
	switch n.tag {
	case "textarea":
		return true
	case "input":
		switch InputType(n) {
		case "hidden", "checkbox", "radio", "submit", "reset", "button", "image", "file", "color", "range":
			return false
		}
		return true
	}
	return false
}

// writeState writes n's current state after its attributes; skipState names
// the default attributes that state replaces.
func writeState(b *strings.Builder, n *Node) {
	f := n.form
	if f == nil {
		return
	}
	if n.tag == "input" && f.dirtyValue {
		b.WriteString(` value="`)
		b.WriteString(html.EscapeString(f.value))
		b.WriteByte('"')
	}
	if f.dirtyChecked && f.checked {
		b.WriteString(` checked=""`)
	}
	if f.dirtySelect && f.selected {
		b.WriteString(` selected=""`)
	}
}

func skipState(n *Node, attr string) bool {
	f := n.form
	if f == nil {
		return false
	}
	switch attr {
	case "value":
		return n.tag == "input" && f.dirtyValue
	case "checked":
		return f.dirtyChecked
	case "selected":
		return f.dirtySelect
	}
	return false
}

// RadioGroup lists the attached radios in n's group (same name, same form
// owner), n included, in tree order. A radio without a name is its own group.
func RadioGroup(t *Tree, n *Node) []*Node {
	name, _ := n.Attr("name")
	if name == "" {
		return []*Node{n}
	}
	owner := FormOwner(n, t)
	var out []*Node
	Walk(t.root, func(m *Node) {
		if m.tag == "input" && InputType(m) == "radio" {
			if mn, _ := m.Attr("name"); mn == name && FormOwner(m, t) == owner {
				out = append(out, m)
			}
		}
	})
	return out
}

// CheckRadio checks n and unchecks the rest of its group.
func (t *Tree) CheckRadio(n *Node) {
	for _, m := range RadioGroup(t, n) {
		if m != n && m.Checked() {
			t.SetChecked(m, false)
		}
	}
	t.SetChecked(n, true)
}

// SelectOption selects opt; in a single select it deselects the others.
func (t *Tree) SelectOption(sel, opt *Node, on bool) {
	if _, multi := sel.Attr("multiple"); !multi && on {
		for _, o := range Options(sel) {
			if o != opt && o.Selected() {
				t.SetSelected(o, false)
			}
		}
	}
	t.SetSelected(opt, on)
}

// OwnerSelect is an option's select (through an optgroup), or nil.
func OwnerSelect(opt *Node) *Node {
	for p := opt.parent; p != nil; p = p.parent {
		switch p.tag {
		case "select":
			return p
		case "optgroup":
			continue
		}
		return nil
	}
	return nil
}

// needsAnchor reports elements the host must map back from a frame box:
// listener-bearing, focusable, label and popover elements.
func needsAnchor(n *Node) bool {
	return len(n.listeners) > 0 || Focusable(n) || n.tag == "label" || n.PopoverMode() != ""
}

// frameID is the id Serialize writes for n, given whether an earlier element
// already used n's id: its own id when that is unique so far and not in the
// reserved synthetic form; otherwise, for an element the host must map back,
// a synthetic id; otherwise its own (possibly duplicate) id, so styling is
// untouched. "" means no id is written.
func frameID(n *Node, taken bool) string {
	id, ok := n.Attr("id")
	reserved := strings.HasPrefix(id, syntheticPrefix)
	switch {
	case ok && id != "" && !reserved && !taken:
		return id
	case needsAnchor(n):
		return syntheticID(n.id)
	case ok && id != "" && !reserved:
		return id
	}
	return ""
}

// AnchorID is the id nthtml reports for n's boxes, the same one Serialize
// writes: unique among the elements the host maps back (a duplicate or
// reserved page id is replaced by a synthetic one in the frame only).
func AnchorID(t *Tree, n *Node) string { return t.Anchors()(n) }

// Anchors returns AnchorID for every element of the tree as it is now, with
// the first-use table built once (focus order asks for every element).
func (t *Tree) Anchors() func(*Node) string {
	first := map[string]*Node{}
	Walk(t.root, func(m *Node) {
		if id, ok := m.Attr("id"); ok && id != "" && first[id] == nil {
			first[id] = m
		}
	})
	return func(n *Node) string {
		id, _ := n.Attr("id")
		if a := frameID(n, first[id] != nil && first[id] != n); a != "" {
			return a
		}
		return syntheticID(n.id)
	}
}

func isLabelable(n *Node) bool {
	if n.tag == "input" {
		return InputType(n) != "hidden"
	}
	switch n.tag {
	case "button", "select", "textarea", "meter", "output", "progress":
		return true
	}
	return false
}

// LabeledControl is the control a <label> activates: the element its for
// attribute names, else its first labelable descendant.
func LabeledControl(t *Tree, label *Node) *Node {
	if id, ok := label.Attr("for"); ok {
		if c := t.ByID(id); c != nil && isLabelable(c) {
			return c
		}
		return nil
	}
	return t.find(label, func(n *Node) bool { return n != label && n.kind == Element && isLabelable(n) })
}

// LabelText is the text of the first label for control (for= or an
// enclosing label), whitespace-collapsed, or "".
func LabelText(t *Tree, control *Node) string {
	if id, ok := control.Attr("id"); ok && id != "" {
		var found *Node
		Walk(t.root, func(n *Node) {
			if found == nil && n.tag == "label" {
				if f, _ := n.Attr("for"); f == id {
					found = n
				}
			}
		})
		if found != nil {
			return strings.Join(strings.Fields(TextContent(found)), " ")
		}
	}
	for p := control.parent; p != nil; p = p.parent {
		if p.tag == "label" {
			return strings.Join(strings.Fields(TextContent(p)), " ")
		}
	}
	return ""
}
