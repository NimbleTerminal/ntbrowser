// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package loop

import "testing"

func TestDisclosureReaderDefaults(t *testing.T) {
	l, _, _ := formLoop(t, `<details id=d><summary id=s>summary</summary>body</details><button type=button id=b popovertarget=p>show</button><button type=button id=c commandfor=p command=hide-popover>hide</button><div popover id=p>popup</div>`)
	done := make(chan struct{})
	l.post(func() {
		defer close(done)
		page := l.page
		l.activate(page, page.Tree.ByID("s"))
		if _, open := page.Tree.ByID("d").Attr("open"); !open {
			t.Error("summary did not open details")
		}
		l.activate(page, page.Tree.ByID("b"))
		if !page.Tree.ByID("p").PopoverOpen() {
			t.Error("button did not show popover")
		}
		l.activate(page, page.Tree.ByID("c"))
		if page.Tree.ByID("p").PopoverOpen() {
			t.Error("command did not hide popover")
		}
	})
	<-done
}
func TestDisclosureSyntheticClickHasNoDefault(t *testing.T) {
	l, _, _ := formLoop(t, `<button id=b type=button popovertarget=p>show</button><div id=p popover>popup</div><script>document.getElementById('b').dispatchEvent(new Event('click',{bubbles:true,cancelable:true}))</script>`)
	done := make(chan struct{})
	l.post(func() {
		defer close(done)
		if l.page.Tree.ByID("p").PopoverOpen() {
			t.Error("synthetic click invoked default action")
		}
	})
	<-done
}
