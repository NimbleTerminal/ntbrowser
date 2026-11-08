// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"fmt"
	"net/url"

	"nimbleterminal.dev/ntbrowser/netpolicy"
	"nimbleterminal.dev/ntbrowser/subres"
)

// SubresourceTransport builds, per page base, the fetcher a subresource
// session talks through. The policy derives from the host's template
// (subresources design, decision 1): the page URL is the origin, AnyOrigin
// follows the mode, the page scheme refuses mixed content, AllowHosts,
// AllowPrivate and MaxRedirects are copied, and the body, in-flight and
// timeout caps follow the subresource config so a raised MaxInFlight never
// meets ErrBusy.
func SubresourceTransport(policy netpolicy.Policy, cfg subres.Config) func(base string) subres.Transport {
	return func(base string) subres.Transport {
		p, err := policyFor(policy, cfg, base)
		if err != nil {
			return netpolicy.New(netpolicy.Policy{}) // the zero policy denies everything; never nil
		}
		return netpolicy.New(p)
	}
}

func policyFor(policy netpolicy.Policy, cfg subres.Config, base string) (netpolicy.Policy, error) {
	cfg = cfg.WithDefaults()
	u, err := url.Parse(base)
	if err != nil {
		return netpolicy.Policy{}, err
	}
	if u.Scheme == "" {
		return netpolicy.Policy{}, fmt.Errorf("subresources: page base %q has no scheme", base)
	}
	// A file:// start page (the documented local case) has no origin: under
	// ModeAny its remote stylesheets and images still load; relative refs
	// resolve to file: and netpolicy's scheme check refuses them, so nothing
	// local is ever read. Under ModeOrigin nothing is same-origin, so
	// nothing loads.
	origin := u
	downgrade := u.Scheme
	if u.Scheme != "http" && u.Scheme != "https" {
		origin, downgrade = nil, ""
	}
	p := netpolicy.Policy{
		Trace:           policy.Trace,
		Cookies:         policy.Cookies,
		Origin:          origin,
		AllowHosts:      append([]string(nil), policy.AllowHosts...),
		AllowPrivate:    policy.AllowPrivate,
		AnyOrigin:       cfg.Mode == subres.ModeAny,
		NoDowngradeFrom: downgrade,
		MaxBody:         int64(max(cfg.MaxSheetBytes, 1<<20)),
		MaxImageBody:    int64(cfg.MaxImageBytes),
		Timeout:         cfg.Timeout,
		MaxInFlight:     cfg.MaxInFlight,
		MaxRedirects:    policy.MaxRedirects,
	}
	return p, nil
}
