// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"nimbleterminal.dev/ntbrowser/loop"
	"nimbleterminal.dev/ntbrowser/profile"
)

func locationModel(t *testing.T, gate profile.ScriptNavigation) *Model {
	p, _ := profile.Builtin("browse")
	p.ScriptNavigation = gate
	m := New(nil, p, Options{})
	m.current = mustURL(t, "https://example.test/a")
	m.history = []string{m.current.String()}
	m.hIndex = 0
	m.documentNav = 1
	m.documentSeq = 1
	m.navSeq = 1
	return m
}
func locationRequest(m *Model, href, mode string) loop.ScriptNavigateMsg {
	return loop.ScriptNavigateMsg{Href: href, Mode: mode, Source: m.current.String(), Navigation: m.documentNav}
}
func TestScriptNavigationIntersectionAndStaleRequests(t *testing.T) {
	for _, tc := range []struct {
		gate            profile.ScriptNavigation
		nav             profile.Navigate
		href            string
		allowed, prompt bool
	}{
		{profile.ScriptNavOff, profile.NavFree, "https://example.test/b", false, false},
		{profile.ScriptNavOrigin, profile.NavOff, "https://example.test/b", false, false},
		{profile.ScriptNavOrigin, profile.NavFree, "https://other.test/b", false, false},
		{profile.ScriptNavOrigin, profile.NavFree, "https://example.test/b", true, false},
		{profile.ScriptNavAllowlist, profile.NavFree, "https://other.test/b", false, false},
		{profile.ScriptNavConfirm, profile.NavFree, "https://other.test/b", false, true},
		{profile.ScriptNavConfirm, profile.NavAllowlist, "https://other.test/b", false, false},
	} {
		m := locationModel(t, tc.gate)
		m.nav = tc.nav
		m.profile.Navigate = tc.nav
		cmd := m.scriptNavigate(locationRequest(m, tc.href, "assign"))
		if (cmd != nil) != tc.allowed || (m.pendingScript != nil) != tc.prompt {
			t.Fatalf("%+v: %s", tc, m.status.text)
		}
	}
	m := locationModel(t, profile.ScriptNavOrigin)
	v := locationRequest(m, "https://example.test/b", "assign")
	v.Navigation++
	if m.scriptNavigate(v) != nil || m.scriptNavCount != 0 {
		t.Fatal("stale navigation accepted")
	}
	v = locationRequest(m, "https://example.test/b", "assign")
	m.navSeq++
	if m.scriptNavigate(v) != nil {
		t.Fatal("old page superseded in-flight load")
	}
}
func TestScriptPromptCannotBeReplaced(t *testing.T) {
	m := locationModel(t, profile.ScriptNavConfirm)
	m.scriptNavigate(locationRequest(m, "https://one.test/", "replace"))
	m.scriptNavigate(locationRequest(m, "https://two.test/", "assign"))
	if m.pendingNav != "https://one.test/" || m.pendingScript.request.Mode != "replace" {
		t.Fatal("prompt replaced")
	}
	m.admit(loop.NavigateMsg{Href: "https://example.test/reader"})
	if m.pendingNav != "" || m.pendingScript != nil {
		t.Fatal("reader navigation kept stale prompt")
	}
}
func TestScriptNavigationRedirectScopeAndBudget(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("redirect escaped origin gate") }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer server.Close()
	m := locationModel(t, profile.ScriptNavOrigin)
	m.current, _ = url.Parse(server.URL)
	m.history[0] = server.URL
	m.policy.AllowPrivate = true
	m.policy.AllowHosts = []string{"127.0.0.1"}
	cmd := m.scriptNavigate(locationRequest(m, server.URL+"/redirect", "replace"))
	if cmd == nil {
		t.Fatal(m.status.text)
	}
	result := cmd().(pageLoadedMsg)
	if result.err == nil {
		t.Fatal("cross-origin redirect accepted")
	}
	m.scriptNavCount = m.profile.ScriptNavBudget()
	m.documentSeq = m.navSeq
	if m.scriptNavigate(locationRequest(m, server.URL+"/again", "assign")) != nil {
		t.Fatal("chain budget bypassed")
	}
}

func TestLocationReplaceAndReloadSelectExistingHistoryEntry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<p>next</p>"))
	}))
	defer server.Close()
	for _, mode := range []string{"assign", "replace", "reload"} {
		m := locationModel(t, profile.ScriptNavOrigin)
		m.current, _ = url.Parse(server.URL)
		m.history = []string{server.URL}
		m.policy.AllowPrivate = true
		target := server.URL
		if mode != "reload" {
			target += "/next"
		}
		cmd := m.scriptNavigate(locationRequest(m, target, mode))
		if cmd == nil {
			t.Fatal(m.status.text)
		}
		result := cmd().(pageLoadedMsg)
		if result.err != nil {
			t.Fatal(result.err)
		}
		want := pushNew
		if mode != "assign" {
			want = m.hIndex
		}
		if result.hist != want {
			t.Fatalf("%s: history %d", mode, result.hist)
		}
	}
}

func TestScriptFormsCannotResetLocationBudgetOrReplacePrompt(t *testing.T) {
	m := locationModel(t, profile.ScriptNavConfirm)
	m.scriptNavigate(locationRequest(m, "https://one.test/", "replace"))
	count := m.scriptNavCount
	if m.admit(loop.NavigateMsg{Href: "https://two.test/", Form: true, Script: true}) != nil || m.pendingNav != "https://one.test/" || m.scriptNavCount != count {
		t.Fatal("script form replaced prompt or reset budget")
	}
	m.pendingNav = ""
	m.pendingScript = nil
	m.admit(loop.NavigateMsg{Href: "https://example.test/form", Form: true, Script: true})
	if m.scriptNavCount != count {
		t.Fatal("script form reset budget")
	}
}

func TestLocationFragmentCommitsWithoutFetching(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := loop.New(ctx, loop.Config{}, dropSink{})
	go l.Run()
	m := locationModel(t, profile.ScriptNavOrigin)
	m.loop = l
	if cmd := m.scriptNavigate(locationRequest(m, "https://example.test/a#one", "assign")); cmd != nil {
		t.Fatal("fragment started a fetch")
	}
	if m.current.Fragment != "one" || len(m.history) != 2 || m.navSeq != 1 {
		t.Fatal(m.current, m.history)
	}
	if cmd := m.scriptNavigate(locationRequest(m, "https://example.test/a#two", "replace")); cmd != nil {
		t.Fatal("fragment replacement started a fetch")
	}
	if m.current.Fragment != "two" || len(m.history) != 2 || m.history[1] != "https://example.test/a#two" {
		t.Fatal(m.current, m.history)
	}
}
