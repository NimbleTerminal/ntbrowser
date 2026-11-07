// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
package netpolicy

import "net/url"

// ForFrame retains network vetting and the parent's semaphore, but grants only
// explicit frame destinations. Frame requests carry no cookies.
func (f *Fetcher) ForFrame(base string, origins []string) *Fetcher {
	d := f.ForScripts(base, origins)
	p := d.p
	p.MaxBody = 256 << 10
	p.Cookies = nil
	out := New(p)
	out.sem = f.sem
	return out
}

// ForFramePage creates a child's fetch/subresource policy without carrying
// frame-navigation or parent-origin grants into page script authority.
func (f *Fetcher) ForFramePage(base string, hosts []string, any bool) *Fetcher {
	p := f.p
	u, err := url.Parse(base)
	if p.Origin == nil || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return New(Policy{})
	}
	p.Origin = u
	p.AllowOrigins = nil
	p.AllowHosts = append([]string(nil), hosts...)
	p.AnyOrigin = any
	p.Cookies = nil
	p.MaxBody = 1 << 20
	if p.Origin != nil {
		p.NoDowngradeFrom = p.Origin.Scheme
	}
	out := New(p)
	out.sem = f.sem
	return out
}
