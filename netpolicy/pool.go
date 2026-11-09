// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package netpolicy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// Pool is the connections a session's fetchers share: the navigation, the
// page, its subresources and every derived script and frame fetcher reuse one
// TCP/TLS connection per host instead of handshaking once each.
//
// A pool holds connections and nothing else. Every admission decision
// (Policy.Allowed on the URL and on each redirect hop, content types, body
// caps, the in-flight budget, cookies, SRI) stays with the Fetcher making the
// request and is evaluated per request, so sharing a pool shares no authority.
// The one decision made at dial time, vetting the resolved addresses, differs
// between policies only by AllowPrivate; the pool therefore keeps two
// transports and a policy without AllowPrivate can never be handed a
// connection that was dialed without the address check.
//
// The transports carry no per-policy timeout: Open bounds each request with
// the policy's own Timeout through its context.
type Pool struct {
	mu              sync.Mutex
	public, private *http.Transport
}

// NewPool returns an empty pool. Transports are built on first use, so a pool
// that never dials (the zero policy's) costs nothing.
func NewPool() *Pool { return &Pool{} }

// transport returns the transport for policies with the given AllowPrivate.
func (p *Pool) transport(allowPrivate bool) *http.Transport {
	p.mu.Lock()
	defer p.mu.Unlock()
	slot := &p.public
	if allowPrivate {
		slot = &p.private
	}
	if *slot == nil {
		*slot = newTransport(allowPrivate)
	}
	return *slot
}

// Close releases the pool's idle connections. Requests in flight finish; the
// pool stays usable.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, t := range []*http.Transport{p.public, p.private} {
		if t != nil {
			t.CloseIdleConnections()
		}
	}
}

func newTransport(allowPrivate bool) *http.Transport {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return &http.Transport{
		Proxy:               nil, // never consult HTTP_PROXY from the page's requests
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 6,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second,
		// Resolve, vet every address, then dial the vetted IP. Vetting the
		// hostname alone would allow DNS rebinding between check and connect.
		// A reused connection stays on the address vetted when it was dialed.
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if !allowPrivate && isForbiddenIP(net.IP(ip.Unmap().AsSlice())) {
					return nil, fmt.Errorf("%w: %s resolves to %s", ErrDenied, host, ip)
				}
			}
			return dialVetted(ctx, dialer.DialContext, network, ips, port)
		},
	}
}
