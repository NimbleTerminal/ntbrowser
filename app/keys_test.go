package app

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"nimbleterminal.dev/ntbrowser/loop"
	"nimbleterminal.dev/ntbrowser/netpolicy"
)

func newModel(t *testing.T, nav NavMode) *Model {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	l := loop.New(ctx, loop.Config{Width: 400}, dropSink{})
	go l.Run()
	m := New(l, netpolicy.Policy{AllowHosts: []string{"cdn.test"}}, nav, "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(loop.FrameMsg{Gen: 1, Result: renderTall(t, `<p>x</p>`)})
	m.current = mustURL(t, "https://page.test/a")
	m.history, m.hIndex = []string{"https://page.test/a"}, 0
	return m
}

func TestQuitAndHistoryKeys(t *testing.T) {
	m := newModel(t, NavFree)
	if _, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"}); cmd == nil || !m.closing {
		t.Fatal("q did not start shutdown")
	}
	m = newModel(t, NavFree)
	m.history = append(m.history, "https://page.test/b")
	m.hIndex = 1
	if _, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace}); cmd == nil {
		t.Fatal("backspace did not navigate back")
	}
	if m.hIndex != 1 {
		t.Fatal("history moved before the load succeeded")
	}
	if _, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModAlt}); cmd != nil {
		t.Fatal("forward at the end of history should do nothing")
	}
}

func TestNavigationModes(t *testing.T) {
	m := newModel(t, NavAllowlist)
	if cmd := m.admit("https://evil.test/"); cmd != nil || !strings.HasPrefix(m.status, "blocked:") {
		t.Fatalf("allowlist admitted evil: %q", m.status)
	}
	if cmd := m.admit("https://cdn.test/x"); cmd == nil {
		t.Fatal("allowlist refused an allowlisted host")
	}
	m = newModel(t, NavConfirm)
	if cmd := m.admit("https://other.test/"); cmd != nil || m.pendingNav != "https://other.test/" {
		t.Fatal("confirm did not prompt")
	}
	if _, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape}); cmd != nil || m.pendingNav != "" {
		t.Fatal("esc did not cancel")
	}
	_ = m.admit("https://other.test/")
	if _, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd == nil || m.pendingNav != "" {
		t.Fatal("enter did not navigate")
	}
	if cmd := m.admit("https://page.test/same"); cmd == nil {
		t.Fatal("confirm mode prompted for a same-origin link")
	}
}

func TestSearchKeysDoNotReachThePage(t *testing.T) {
	m := newModel(t, NavFree)
	m.viewer.Update(tea.KeyPressMsg{Code: '/', Text: "/"}) // opens nthtml's search box
	if !m.viewer.SearchInputActive() {
		t.Skip("nthtml search box not active headlessly")
	}
	m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if m.closing {
		t.Fatal("q typed into the search box quit the browser")
	}
}

// Review Focus 5: zero- and one-cell terminals must not panic.
func TestDegenerateTerminalSizes(t *testing.T) {
	m := newModel(t, NavFree)
	for _, sz := range []tea.WindowSizeMsg{{Width: 0, Height: 0}, {Width: 1, Height: 1}, {Width: 1, Height: 3}} {
		m.Update(sz)
		m.Update(loop.FrameMsg{Gen: uint64(10 + sz.Width), Result: renderTall(t, `<p>y</p>`)})
		if v := m.View(); v.Content == "" {
			t.Fatalf("empty view at %+v", sz)
		}
	}
	if w := documentWidth(m.viewer); w < 32 || w > 4096 {
		t.Fatalf("documentWidth out of range: %d", w)
	}
}
