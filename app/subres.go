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
	if u.Scheme == "" || u.Host == "" {
		return netpolicy.Policy{}, fmt.Errorf("subresources: page base %q has no origin", base)
	}
	p := netpolicy.Policy{
		Origin:          u,
		AllowHosts:      append([]string(nil), policy.AllowHosts...),
		AllowPrivate:    policy.AllowPrivate,
		AnyOrigin:       cfg.Mode == subres.ModeAny,
		NoDowngradeFrom: u.Scheme,
		MaxBody:         int64(max(cfg.MaxSheetBytes, 1<<20)),
		Timeout:         cfg.Timeout,
		MaxInFlight:     cfg.MaxInFlight,
		MaxRedirects:    policy.MaxRedirects,
	}
	return p, nil
}
