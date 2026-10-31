package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"nimbleterminal.dev/ntbrowser/loop"
)

func typeText(m *Model, s string) {
	for _, r := range s {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// statusLines is the last two lines of the view: everything the app itself
// draws (the viewer above it carries its own terminal graphics).
func statusLines(m *Model) string {
	lines := strings.Split(m.View().Content, "\n")
	return strings.Join(lines[max(0, len(lines)-2):], "\n")
}

func textFocus(source, label string, user bool) loop.FocusMsg {
	return loop.FocusMsg{Source: source, User: user, Control: loop.ControlInfo{Kind: "text", Label: label}}
}

func TestUserFocusOnTextEntersEditMode(t *testing.T) {
	m := newModel(t, NavFree)
	m.Update(textFocus("q", "Search", true))
	if m.mode != modeEdit {
		t.Fatalf("mode %v", m.mode)
	}
	m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if m.closing || m.input.Value() != "q" {
		t.Fatalf("q quit or not typed: closing=%v value=%q", m.closing, m.input.Value())
	}
}

func TestScriptFocusStaysInBrowseMode(t *testing.T) {
	m := newModel(t, NavFree)
	m.Update(textFocus("q", "Search", false))
	if m.mode != modeBrowse {
		t.Fatalf("mode %v", m.mode)
	}
	if !strings.Contains(m.status, "focus: Search") {
		t.Fatalf("status %q", m.status)
	}
	m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if !m.closing {
		t.Fatal("q should quit in browse mode")
	}
}

func TestEditKeys(t *testing.T) {
	m := newModel(t, NavFree)
	m.Update(textFocus("q", "Search", true))
	typeText(m, "ab")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.mode != modeBrowse || m.focus.Source != "q" {
		t.Fatalf("esc: mode %v focus %q", m.mode, m.focus.Source)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // focused text field: back to editing, value kept
	if m.mode != modeEdit || m.input.Value() != "ab" {
		t.Fatalf("enter: mode %v value %q", m.mode, m.input.Value())
	}
	m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !m.closing {
		t.Fatal("ctrl+c must quit in edit mode")
	}
}

func TestFrameDuringEditKeepsEditor(t *testing.T) {
	m := newModel(t, NavFree)
	m.Update(textFocus("q", "Search", true))
	typeText(m, "abc")
	m.Update(loop.FrameMsg{Gen: 2, Result: renderTall(t, `<p>y</p>`)})
	if m.mode != modeEdit || m.input.Value() != "abc" || m.input.Position() != 3 {
		t.Fatalf("mode %v value %q pos %d", m.mode, m.input.Value(), m.input.Position())
	}
}

func TestFirstFrameKeepsFocusLine(t *testing.T) {
	m := newModel(t, NavFree)
	m.Update(pageLoadedMsg{html: "<p>x</p>", base: mustURL(t, "https://page.test/b"), seq: m.navSeq, hist: pushNew})
	m.Update(textFocus("q", "Search", false))
	m.Update(loop.FrameMsg{Gen: 2, Result: renderTall(t, `<p>y</p>`)})
	if m.status != "focus: Search" {
		t.Fatalf("status %q", m.status)
	}
}

func TestNavigationLeavesEditMode(t *testing.T) {
	m := newModel(t, NavFree)
	m.Update(textFocus("q", "Search", true))
	m.Update(pageLoadedMsg{html: "<p>x</p>", base: mustURL(t, "https://page.test/b"), seq: m.navSeq, hist: pushNew})
	if m.mode != modeBrowse || m.focus.Source != "" {
		t.Fatalf("mode %v focus %q", m.mode, m.focus.Source)
	}
}

func TestValueMsgUpdatesEditor(t *testing.T) {
	m := newModel(t, NavFree)
	m.Update(textFocus("q", "Search", true))
	typeText(m, "ab")
	m.Update(loop.ValueMsg{Source: "q", Value: "zz"})
	if m.input.Value() != "zz" || m.input.Position() != 2 {
		t.Fatalf("value %q pos %d", m.input.Value(), m.input.Position())
	}
	m.Update(loop.ValueMsg{Source: "other", Value: "no"})
	if m.input.Value() != "zz" {
		t.Fatal("ValueMsg for another control changed the editor")
	}
}

func TestChooserKeys(t *testing.T) {
	m := newModel(t, NavFree)
	m.Update(loop.FocusMsg{Source: "s", User: true, Control: loop.ControlInfo{Kind: "select", Label: "Size", Options: []string{"small", "large"}, OptionIndex: []int{0, 2}, Selected: []int{0}}})
	if m.mode != modeBrowse {
		t.Fatal("focusing a select must not open the chooser")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.mode != modeChoose || !strings.Contains(m.View().Content, "small (1/2)") {
		t.Fatalf("mode %v view %q", m.mode, m.View().Content)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if !strings.Contains(m.View().Content, "large (2/2)") {
		t.Fatal(m.View().Content)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.mode != modeBrowse {
		t.Fatalf("mode %v", m.mode)
	}
}

func TestLabelsAreCleaned(t *testing.T) {
	m := newModel(t, NavFree)
	m.Update(textFocus("q", "Se\x1b[31march\x07", false))
	if tail := statusLines(m); hasTerminalControls(tail) || !strings.Contains(tail, "focus: Se[31march") {
		t.Fatalf("label reached the terminal unclean: %q", tail)
	}
	m.Update(loop.FocusMsg{Source: "s", User: true, Control: loop.ControlInfo{Kind: "select", Label: "x", Options: []string{"evil\x1b]0;t\x07"}, OptionIndex: []int{0}}})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if tail := statusLines(m); hasTerminalControls(tail) || !strings.Contains(tail, "evil]0;t") {
		t.Fatalf("option label reached the terminal unclean: %q", tail)
	}
}

func TestFormSubmitConfirmWording(t *testing.T) {
	m := newModel(t, NavConfirm)
	m.Update(loop.NavigateMsg{Href: "https://other.test/s?q=secret", Form: true})
	if m.status != "Submit form to other.test? Enter to send · Esc to stay" {
		t.Fatalf("status %q", m.status)
	}
}

func TestPasswordNeverShown(t *testing.T) {
	m := newModel(t, NavFree)
	m.Update(loop.FocusMsg{Source: "p", User: true, Control: loop.ControlInfo{Kind: "password", Label: "Password"}})
	typeText(m, "secret")
	if strings.Contains(m.View().Content, "secret") || strings.Contains(m.status, "secret") {
		t.Fatal("typed password shown")
	}
	m2 := newModel(t, NavFree)
	m2.Update(loop.FocusMsg{Source: "p", User: true, Control: loop.ControlInfo{Kind: "password", Label: "Password", Value: "hunter2"}})
	if m2.input.Value() != "hunter2" {
		t.Fatalf("editor not seeded: %q", m2.input.Value())
	}
	typeText(m2, "x")
	if m2.input.Value() != "hunter2x" {
		t.Fatalf("typing did not append: %q", m2.input.Value())
	}
	if strings.Contains(m2.View().Content, "hunter2") || strings.Contains(m2.status, "hunter2") {
		t.Fatal("pre-filled password shown")
	}
}

func TestEnterWithoutFocusDoesNothing(t *testing.T) {
	m := newModel(t, NavFree)
	if _, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil || m.mode != modeBrowse {
		t.Fatalf("enter without focus: cmd %v mode %v", cmd, m.mode)
	}
}

func TestConfirmPromptWinsOverEditMode(t *testing.T) {
	m := newModel(t, NavConfirm)
	m.Update(textFocus("q", "Search", true))
	m.Update(loop.NavigateMsg{Href: "https://other.test/s?q=1", Form: true})
	if !strings.Contains(m.View().Content, "Submit form to other.test?") {
		t.Fatalf("prompt hidden by the editor: %q", m.View().Content)
	}
	seq := m.navSeq
	if _, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd == nil || m.navSeq != seq+1 || m.pendingNav != "" {
		t.Fatalf("enter did not navigate once: seq %d→%d", seq, m.navSeq)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.navSeq != seq+1 {
		t.Fatal("second enter navigated again")
	}

	m = newModel(t, NavConfirm)
	m.Update(textFocus("q", "Search", true))
	m.Update(loop.NavigateMsg{Href: "https://other.test/s?q=1", Form: true})
	seq = m.navSeq
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.navSeq != seq || m.pendingNav != "" || !strings.HasPrefix(m.status, "stayed on") {
		t.Fatalf("esc: seq %d→%d status %q", seq, m.navSeq, m.status)
	}

	m = newModel(t, NavConfirm)
	seq = m.navSeq
	if _, cmd := m.Update(loop.NavigateMsg{Href: "https://page.test/s?q=1", Form: true}); cmd == nil || m.navSeq != seq+1 || m.pendingNav != "" {
		t.Fatal("same-origin submission should navigate without a prompt")
	}
}

func TestAllowlistBlocksFormSubmission(t *testing.T) {
	m := newModel(t, NavAllowlist)
	seq := m.navSeq
	m.Update(loop.NavigateMsg{Href: "https://evil.test/s?q=1", Form: true})
	if m.navSeq != seq || !strings.HasPrefix(m.status, "blocked: ") {
		t.Fatalf("seq %d→%d status %q", seq, m.navSeq, m.status)
	}
}

func TestTextareaPanelRows(t *testing.T) {
	m := newModel(t, NavFree)
	m.Update(loop.FocusMsg{Source: "t", User: true, Control: loop.ControlInfo{Kind: "textarea", Label: "Comment"}})
	if m.viewRows() != 24-2-6 {
		t.Fatalf("viewRows %d", m.viewRows())
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.viewRows() != 24-2 {
		t.Fatalf("panel not closed: %d", m.viewRows())
	}
}

func TestPasteGoesToEditor(t *testing.T) {
	m := newModel(t, NavFree)
	m.Update(textFocus("q", "Search", true))
	m.Update(tea.PasteMsg{Content: "pasted text"})
	if m.input.Value() != "pasted text" {
		t.Fatalf("value %q", m.input.Value())
	}
	if m.input.KeyMap.Paste.Enabled() {
		t.Fatal("ctrl+v clipboard paste must stay disabled")
	}
}

// Review #2: a textarea's line breaks survive opening, committing and
// reopening the editor; long textareas are not capped at 99 lines.
func TestTextareaKeepsLineBreaks(t *testing.T) {
	m := newModel(t, NavFree)
	m.Update(loop.FocusMsg{Source: "t", User: true, Control: loop.ControlInfo{Kind: "textarea", Label: "C", Value: "line1\nline2"}})
	if m.area.Value() != "line1\nline2" {
		t.Fatalf("seed %q", m.area.Value())
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.focus.Control.Value != "line1\nline2" {
		t.Fatalf("commit %q", m.focus.Control.Value)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.area.Value() != "line1\nline2" {
		t.Fatalf("reopen %q", m.area.Value())
	}
	m.Update(loop.ValueMsg{Source: "t", Value: "a\nb"})
	if m.area.Value() != "a\nb" {
		t.Fatalf("script write %q", m.area.Value())
	}
	if m.area.MaxHeight != 0 {
		t.Fatalf("MaxHeight %d", m.area.MaxHeight)
	}
}

// Review (re-graded): a script write of the value already shown (input
// masks) must not move the cursor.
func TestValueMsgSameValueKeepsCursor(t *testing.T) {
	m := newModel(t, NavFree)
	m.Update(textFocus("q", "Search", true))
	typeText(m, "abc")
	m.input.SetCursor(1)
	m.Update(loop.ValueMsg{Source: "q", Value: "abc"})
	if m.input.Position() != 1 {
		t.Fatalf("cursor moved to %d", m.input.Position())
	}
}

func TestFocusRingFollowsFrames(t *testing.T) {
	m := newModel(t, NavFree)
	m.Update(loop.FrameMsg{Gen: 2, Result: renderTall(t, `<p><input id=q></p>`)})
	m.Update(textFocus("q", "Search", false))
	if m.ring != "q" {
		t.Fatalf("ring %q after focus", m.ring)
	}
	m.Update(loop.FrameMsg{Gen: 3, Result: renderTall(t, `<p><input id=q></p>`)})
	if m.ring != "q" {
		t.Fatalf("ring %q not re-applied after a frame", m.ring)
	}
	m.Update(loop.FrameMsg{Gen: 4, Result: renderTall(t, `<p>gone</p>`)})
	if m.ring != "" {
		t.Fatalf("ring %q on an element no longer drawn", m.ring)
	}
	m.Update(loop.FrameMsg{Gen: 5, Result: renderTall(t, `<p><input id=q></p>`)})
	m.Update(loop.FocusMsg{})
	if m.ring != "" {
		t.Fatalf("ring %q after focus cleared", m.ring)
	}
}

// Re-applying the ring after a frame must not pull a reader who scrolled
// away back to the focused element.
func TestFrameKeepsScrollWithFocus(t *testing.T) {
	m := newModel(t, NavFree)
	m.Update(loop.FrameMsg{Gen: 2, Result: renderTall(t, `<p><input id=q></p>`)})
	m.Update(textFocus("q", "Search", false))
	m.viewer.Pan(0, 800)
	before, ok := m.viewer.DocumentViewport()
	if !ok || before.Min.Y == 0 {
		t.Fatalf("viewport %v %v", before, ok)
	}
	m.Update(loop.FrameMsg{Gen: 3, Result: renderTall(t, `<p><input id=q></p>`)})
	if after, _ := m.viewer.DocumentViewport(); after.Min != before.Min {
		t.Fatalf("scroll moved %v → %v", before.Min, after.Min)
	}
}
