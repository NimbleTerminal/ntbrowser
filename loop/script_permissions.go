// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package loop

import (
	"fmt"
	"net/url"
	"nimbleterminal.dev/ntbrowser/netpolicy"
	"nimbleterminal.dev/ntbrowser/origin"
	"nimbleterminal.dev/ntbrowser/profile"
	"time"
)

type ScriptPermissionMsg struct {
	ID, Navigation           uint64
	Source, Destination, URL string
	Expired                  bool
}
type scriptPermission struct {
	message ScriptPermissionMsg
	timer   *time.Timer
	resume  func(bool)
}

// fetchScript runs admission on the VM owner. Only the actual fetch/hash work
// leaves the goroutine; completion comes back with page and generation checks.
func (l *Loop) fetchScript(page *Page, generation uint64, src, integrity, cors string, done func(netpolicy.Response, error)) {
	targets := l.cfg.Profile.ScriptOrigins(page.Base)
	source, sourceErr := origin.Of(page.Base)
	for grant := range l.scriptSessions {
		if grant.Source == source {
			targets = append(targets, grant.Destination)
		}
	}
	baseFetcher := page.Fetcher.ForScripts(page.Base, targets)
	_, resolveErr := baseFetcher.Resolve(src)
	destination := ""
	if parsed, err := url.Parse(src); err == nil {
		if base, err := url.Parse(page.Base); err == nil {
			destination, _ = origin.Of(base.ResolveReference(parsed).String())
		}
	}
	run := func(fetcher *netpolicy.Fetcher) {
		go func() {
			defer fetcher.CloseScriptConnections()
			response, err := fetcher.Script(page.ctx, src, integrity, cors)
			l.post(func() {
				if l.page != page || page.killed || l.navGen.Load() != generation {
					return
				}
				if err != nil {
					l.send(LogMsg{"warn", fmt.Sprintf("blocked script: %s → %s: %s", source, src, err.Error())})
					l.cfg.Trace.Record("script_request_denied", map[string]string{"source": source, "url": src, "purpose": "script", "reason": err.Error()})
				}
				done(response, err)
			})
		}()
	}
	if resolveErr == nil {
		run(baseFetcher)
		return
	}
	// Only a missing origin grant is promptable. Recheck with that exact grant:
	// scheme, private-network and mixed-content refusals still cannot be approved.
	candidate := page.Fetcher.ForScripts(page.Base, append(targets, destination))
	promptable := sourceErr == nil && destination != "" && destination != source
	if promptable {
		_, err := candidate.Resolve(src)
		promptable = err == nil
	}
	refuse := func(reason string) {
		l.send(LogMsg{"warn", fmt.Sprintf("blocked script: %s → %s: %s", source, src, reason)})
		l.cfg.Trace.Record("script_request_denied", map[string]string{"source": source, "url": src, "purpose": "script", "reason": reason})
		go l.post(func() {
			if l.page == page && !page.killed && l.navGen.Load() == generation {
				done(netpolicy.Response{}, fmt.Errorf("script request denied: %s", reason))
			}
		})
	}
	if !promptable || !l.cfg.Profile.ScriptPrompts {
		refuse(resolveErr.Error())
		return
	}
	if len(page.scriptPermissions) >= 8 {
		refuse("permission queue full")
		return
	}
	l.scriptPermissionID++
	message := ScriptPermissionMsg{ID: l.scriptPermissionID, Navigation: generation, Source: page.Base, Destination: destination, URL: src}
	request := &scriptPermission{message: message}
	request.resume = func(allow bool) {
		if allow {
			run(candidate)
		} else {
			refuse("reader denied or permission expired")
		}
	}
	if page.scriptPermissions == nil {
		page.scriptPermissions = map[uint64]*scriptPermission{}
	}
	page.scriptPermissions[message.ID] = request
	request.timer = time.AfterFunc(30*time.Second, func() { l.post(func() { l.decideScriptPermission(message.ID, generation, "deny") }) })
	l.cfg.Trace.Record("script_permission", message)
	l.send(message)
}
func (l *Loop) DecideScriptPermission(id, generation uint64, decision string) {
	if !l.tryPost(func() { l.decideScriptPermission(id, generation, decision) }) {
		go l.post(func() { l.decideScriptPermission(id, generation, decision) })
	}
}
func (l *Loop) decideScriptPermission(id, generation uint64, decision string) {
	page := l.page
	if page == nil || page.killed || l.navGen.Load() != generation {
		return
	}
	request := page.scriptPermissions[id]
	if request == nil || request.message.Navigation != generation {
		return
	}
	delete(page.scriptPermissions, id)
	request.timer.Stop()
	expired := request.message
	expired.Expired = true
	l.send(expired)
	allow := decision == "once" || decision == "session"
	if decision == "session" {
		source, err := origin.Of(page.Base)
		grant := profile.ScriptGrant{Source: source, Destination: request.message.Destination}
		if err != nil || len(l.scriptSessions) >= 128 && !l.scriptSessions[grant] {
			allow = false
		} else {
			if l.scriptSessions == nil {
				l.scriptSessions = map[profile.ScriptGrant]bool{}
			}
			l.scriptSessions[profile.ScriptGrant{Source: source, Destination: request.message.Destination}] = true
		}
	}
	l.cfg.Trace.Record("script_permission_decision", map[string]any{"id": id, "navigation": generation, "decision": decision, "allowed": allow})
	request.resume(allow)
	if allow && decision == "session" {
		for pendingID, pending := range page.scriptPermissions {
			if pending.message.Destination == request.message.Destination {
				l.decideScriptPermission(pendingID, generation, "once")
			}
		}
	}
}
