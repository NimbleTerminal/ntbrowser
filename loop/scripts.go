// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package loop

import (
	"fmt"
	"nimbleterminal.dev/ntbrowser/jsvm"
	"nimbleterminal.dev/ntbrowser/netpolicy"
	"nimbleterminal.dev/ntbrowser/profile"
)

type dynamicScript struct {
	request    jsvm.ScriptRequest
	ready      bool
	body, name string
	err        error
}

func (l *Loop) loadDynamicScript(page *Page, generation uint64, request jsvm.ScriptRequest) {
	job := &dynamicScript{request: request, name: request.URL}
	if !request.Async {
		page.dynamicOrdered = append(page.dynamicOrdered, job)
	}
	// Admission and the pending cap are synchronous. Workers carry plain data,
	// never JS objects. Denials complete as tasks, like network failures.
	var denied error
	if l.cfg.Profile.Scripts != profile.ScriptsExternal {
		denied = fmt.Errorf("external scripts disabled")
	}
	if request.URL == "" {
		denied = fmt.Errorf("empty script src")
	}
	complete := func(response netpolicy.Response, err error) {
		if l.page != page || page.killed || l.navGen.Load() != generation {
			return
		}
		job.ready, job.err, job.body = true, err, response.Body
		if response.URL != "" {
			job.name = response.URL
		}
		finish := func(j *dynamicScript) {
			result := "ok"
			if j.err != nil {
				result = j.err.Error()
			}
			l.cfg.Trace.Record("dynamic_script", map[string]any{"url": j.name, "result": result, "async": j.request.Async})
			page.Sandbox.CompleteScript(j.request.ID, j.name, j.body, j.err)
			j.body = ""
		}
		if request.Async {
			finish(job)
		}
		for !page.killed && !page.Sandbox.Disabled() && len(page.dynamicOrdered) > 0 && page.dynamicOrdered[0].ready {
			next := page.dynamicOrdered[0]
			page.dynamicOrdered[0] = nil
			page.dynamicOrdered = page.dynamicOrdered[1:]
			finish(next)
		}
	}
	if denied != nil {
		go l.post(func() { complete(netpolicy.Response{}, denied) })
		return
	}
	l.fetchScript(page, generation, request.URL, request.Integrity, request.CrossOrigin, complete)
}
