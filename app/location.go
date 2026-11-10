// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package app

import (
	"net/url"
	"strings"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntbrowser/loop"
	"nimbleterminal.dev/ntbrowser/profile"
)

type scriptPlan struct {
	request               loop.ScriptNavigateMsg
	hist                  int
	anyOrigin, originOnly bool
}

func sameLocationOrigin(a, b *url.URL) bool {
	if a == nil || b == nil || a.Scheme != b.Scheme || !strings.EqualFold(a.Hostname(), b.Hostname()) {
		return false
	}
	port := func(u *url.URL) string {
		if u.Port() != "" {
			return u.Port()
		}
		if u.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	return port(a) == port(b)
}
func (m *Model) blockScriptNavigation(reason string) tea.Cmd {
	m.setStatus(statusError, "blocked script navigation: "+reason)
	m.policy.Trace.Record("script_navigation_denied", reason)
	return nil
}
func (m *Model) scriptNavigate(v loop.ScriptNavigateMsg) tea.Cmd {
	if m.current == nil || v.Navigation != m.documentNav || v.Source != m.current.String() || m.navSeq != m.documentSeq {
		return nil
	}
	if m.pendingNav != "" || len(m.scriptPermissions) > 0 {
		return nil
	} // scripts cannot replace a reader-visible prompt
	gate := m.profile.ScriptNavigation
	if !m.profile.ScriptsOn() || !m.profile.NavigationOpen() || gate == "" || gate == profile.ScriptNavOff {
		return m.blockScriptNavigation("gate is off")
	}
	if m.scriptNavCount >= m.profile.ScriptNavBudget() {
		return m.blockScriptNavigation("automatic navigation limit reached; navigate or reload manually")
	}
	u, err := url.Parse(v.Href)
	if err != nil || len(v.Href) > 2048 || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return m.blockScriptNavigation("invalid HTTP(S) destination")
	}
	if v.Mode != "assign" && v.Mode != "replace" && v.Mode != "reload" {
		return m.blockScriptNavigation("invalid operation")
	}
	if v.Mode == "reload" && v.Href != m.current.String() {
		return m.blockScriptNavigation("reload destination differs from document")
	}
	same := sameLocationOrigin(m.current, u)
	plan := scriptPlan{request: v, hist: pushNew}
	if v.Mode == "replace" || v.Mode == "reload" {
		plan.hist = m.hIndex
	}
	p := m.policy
	p.Origin = m.current
	p.AnyOrigin = false
	switch gate {
	case profile.ScriptNavOrigin:
		if !same {
			return m.blockScriptNavigation("destination is not same-origin")
		}
		p.AllowHosts = nil
		plan.originOnly = true
	case profile.ScriptNavAllowlist:
	case profile.ScriptNavConfirm:
		p.AnyOrigin = m.nav != NavAllowlist
		plan.anyOrigin = p.AnyOrigin
	default:
		return m.blockScriptNavigation("unknown gate")
	}
	if err := p.Allowed(u); err != nil {
		return m.blockScriptNavigation(err.Error())
	}
	m.scriptNavCount++
	m.policy.Trace.Record("script_navigation", v)
	if gate == profile.ScriptNavConfirm || m.nav == NavConfirm && !same {
		m.pendingNav = v.Href
		m.pendingScript = &plan
		m.setStatus(statusPlain, "Script requests "+v.Mode+" to "+v.Href+"? Enter to allow · Esc to stay")
		return nil
	}
	if m.nav == NavConfirm && same {
		plan.originOnly = true
	}
	return m.executeScriptNavigation(plan)
}
func (m *Model) executeScriptNavigation(plan scriptPlan) tea.Cmd {
	v := plan.request
	if m.current == nil || v.Navigation != m.documentNav || v.Source != m.current.String() {
		return nil
	}
	u, _ := url.Parse(v.Href)
	a, b := *m.current, *u
	a.Fragment, a.RawFragment = "", ""
	b.Fragment, b.RawFragment = "", ""
	if v.Mode != "reload" && a.String() == b.String() && (u.Fragment != m.current.Fragment || strings.Contains(v.Href, "#")) {
		var cmd tea.Cmd
		if !m.chunkFragment(v.Href) {
			cmd, _ = m.cssFragment(v.Href)
		}
		if plan.hist == pushNew {
			m.history = append(m.history[:m.hIndex+1], v.Href)
			m.hIndex = len(m.history) - 1
		} else if m.hIndex >= 0 {
			m.history[m.hIndex] = v.Href
		}
		m.current = u
		m.loop.CommitFragment(m.documentNav, v.Href)
		return cmd
	}
	return m.navigate(v.Href, plan.hist, plan.anyOrigin, plan.originOnly)
}
