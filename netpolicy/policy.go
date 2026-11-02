// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

// Package netpolicy is the only place in ntbrowser that opens sockets on behalf
// of a page. JavaScript never sees it: jsvm hands it a Request through the
// loop and gets back a Response struct, both plain data. Everything a browser
// would call CORS, mixed-content, SSRF or cookie policy lives here.
package netpolicy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Policy is fixed per page load. The zero value denies everything, so a
// misconfigured host fails closed.
type Policy struct {
	// Origin is the page's own URL. Same-origin requests are allowed when it
	// is set; relative URLs resolve against it.
	Origin *url.URL
	// AllowHosts lists additional hostnames. A leading dot allows subdomains.
	AllowHosts []string
	// AllowPrivate permits loopback/RFC1918 targets (development only).
	AllowPrivate bool
	// AnyOrigin admits every http(s) host: the policy for top-level
	// navigation under -nav free/confirm (decision 11), never for a page's
	// fetch(); the subresource fetcher may carry it under subres.ModeAny.
	// Scheme, credentials, local names, literal and resolved addresses
	// (isForbiddenIP), cookies, redirects and body caps are enforced
	// exactly as without it.
	AnyOrigin bool
	// NoDowngradeFrom is the page's scheme. "https" refuses http: targets,
	// redirects included (no mixed content, subresources decision 14);
	// "" or "http" refuse nothing.
	NoDowngradeFrom string
	MaxBody         int64         // response bytes (default 2 MiB)
	Timeout         time.Duration // whole request (default 10s)
	MaxInFlight     int           // concurrent requests per page (default 4)
	MaxRedirects    int           // default 3
}

// Kind says what a request is for. It selects the Accept header, the
// admitted content types and, for everything but a document, a 2xx status
// gate. The zero value is KindDocument, so a page's fetch() is unchanged.
type Kind int

const (
	KindDocument Kind = iota // fetch() and top-level navigation: today's behaviour
	KindStyle                // a stylesheet: text/css (or octet-stream with a .css path), 2xx only
	KindImage                // an image: png, jpeg, gif, webp or svg+xml, 2xx only
	KindNavigate             // top-level navigation: any content type; the caller classifies (content.Classify)
)

type Request struct {
	Method  string
	URL     string
	Headers map[string]string // filtered against requestHeaderAllow
	Body    string
	Kind    Kind
}

type Response struct {
	Status     int
	StatusText string
	URL        string // final URL after vetted redirects
	Headers    map[string]string
	Body       string
}

var (
	ErrDenied   = errors.New("ntbrowser/netpolicy: request denied by policy")
	ErrTooLarge = errors.New("ntbrowser/netpolicy: response too large")
	ErrBusy     = errors.New("ntbrowser/netpolicy: too many requests in flight")
)

var requestHeaderAllow = map[string]bool{"accept": true, "accept-language": true, "content-type": true}
var responseHeaderAllow = []string{"Content-Type", "Content-Length", "Last-Modified", "Etag", "Cache-Control"}

type Fetcher struct {
	p      Policy
	client *http.Client
	sem    chan struct{}
}

func New(p Policy) *Fetcher {
	if p.MaxBody <= 0 {
		p.MaxBody = 2 << 20
	}
	if p.Timeout <= 0 {
		p.Timeout = 10 * time.Second
	}
	if p.MaxInFlight <= 0 {
		p.MaxInFlight = 4
	}
	if p.MaxRedirects <= 0 {
		p.MaxRedirects = 3
	}
	f := &Fetcher{p: p, sem: make(chan struct{}, p.MaxInFlight)}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil, // never consult HTTP_PROXY from the page's requests
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          8,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: p.Timeout,
		// Resolve, vet every address, then dial the vetted IP. Vetting the
		// hostname alone would allow DNS rebinding between check and connect.
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
				if !f.p.AllowPrivate && isForbiddenIP(net.IP(ip.Unmap().AsSlice())) {
					return nil, fmt.Errorf("%w: %s resolves to %s", ErrDenied, host, ip)
				}
			}
			return dialVetted(ctx, dialer.DialContext, network, ips, port)
		},
	}
	f.client = &http.Client{
		Transport: transport,
		Jar:       nil, // no cookies, ever
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= f.p.MaxRedirects {
				return fmt.Errorf("%w: too many redirects", ErrDenied)
			}
			return f.Allowed(req.URL) // every hop re-enters policy
		},
	}
	return f
}

// Allowed is the single admission rule, shared by fetch() and navigation.
func (f *Fetcher) Allowed(u *url.URL) error {
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("%w: scheme %q", ErrDenied, u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("%w: credentials in URL", ErrDenied)
	}
	if f.p.NoDowngradeFrom == "https" && u.Scheme == "http" {
		return fmt.Errorf("%w: mixed content %s", ErrDenied, u)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		if !f.p.AllowPrivate {
			return fmt.Errorf("%w: host %q", ErrDenied, host)
		}
	}
	if ip := net.ParseIP(host); ip != nil && !f.p.AllowPrivate && isForbiddenIP(ip) {
		return fmt.Errorf("%w: literal address %s", ErrDenied, host)
	}
	if f.p.AnyOrigin {
		return nil
	}
	// Same origin means scheme + host + port, as in browsers.
	if f.p.Origin != nil && f.p.Origin.Scheme == u.Scheme && strings.EqualFold(hostPort(f.p.Origin), hostPort(u)) {
		return nil
	}
	for _, a := range f.p.AllowHosts {
		a = strings.ToLower(a)
		if a == host || (strings.HasPrefix(a, ".") && strings.HasSuffix(host, a)) {
			return nil
		}
	}
	return fmt.Errorf("%w: host %q is not allowlisted", ErrDenied, host)
}

// Resolve turns a page-supplied reference into an admitted absolute URL.
func (f *Fetcher) Resolve(ref string) (*url.URL, error) {
	if len(ref) > 2048 || strings.ContainsAny(ref, "\x00\r\n\t ") {
		return nil, fmt.Errorf("%w: malformed reference", ErrDenied)
	}
	u, err := url.Parse(ref)
	if err != nil {
		return nil, err
	}
	if f.p.Origin != nil {
		u = f.p.Origin.ResolveReference(u)
	}
	u.Fragment = ""
	if err := f.Allowed(u); err != nil {
		return nil, err
	}
	return u, nil
}

// dialVetted connects to the first reachable address. All of them were vetted.
func dialVetted(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error), network string, ips []netip.Addr, port string) (net.Conn, error) {
	var firstErr error
	for _, ip := range ips {
		c, err := dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return c, nil
		}
		if firstErr == nil {
			firstErr = err
		}
		if ctx.Err() != nil {
			break
		}
	}
	return nil, firstErr
}

// OpenResponse is a response whose headers have passed every check and whose
// body has not been read. Body holds the request's in-flight slot until it is
// closed; closing twice is safe.
type OpenResponse struct {
	Status      int
	URL         string // final URL after vetted redirects
	ContentType string
	Length      int64 // -1 when the server did not say
	Headers     map[string]string
	Body        io.ReadCloser
}

type openBody struct {
	io.ReadCloser
	once sync.Once
	done func()
}

func (b *openBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.done)
	return err
}

// Open performs a request up to the response headers, under every check Do
// makes. It is safe for concurrent use.
func (f *Fetcher) Open(ctx context.Context, r Request) (*OpenResponse, error) {
	select {
	case f.sem <- struct{}{}:
	default:
		return nil, ErrBusy
	}
	release := func() { <-f.sem }
	u, err := f.Resolve(r.URL)
	if err != nil {
		release()
		return nil, err
	}
	method := strings.ToUpper(r.Method)
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodPost {
		release()
		return nil, fmt.Errorf("%w: method %s", ErrDenied, method)
	}
	if int64(len(r.Body)) > f.p.MaxBody {
		release()
		return nil, ErrTooLarge
	}
	// The timeout covers reaching the headers, and for everything but a
	// navigation the body too: a navigation's caller owns a possibly long read.
	ctx, cancel := context.WithCancel(ctx)
	deadline := time.AfterFunc(f.p.Timeout, cancel)
	done := func() { deadline.Stop(); cancel(); release() }
	req, err := http.NewRequestWithContext(ctx, method, u.String(), strings.NewReader(r.Body))
	if err != nil {
		done()
		return nil, err
	}
	req.Header.Set("User-Agent", "ntbrowser/0.1 (+https://nimbleterminal.dev)")
	req.Header.Set("Accept", acceptFor(r.Kind))
	for k, v := range r.Headers {
		if requestHeaderAllow[strings.ToLower(k)] && len(v) <= 1024 && !strings.ContainsAny(v, "\r\n") {
			req.Header.Set(k, v)
		}
	}
	resp, err := f.client.Do(req)
	if err != nil {
		done()
		return nil, err
	}
	if r.Kind == KindNavigate {
		deadline.Stop()
	}
	if r.Kind != KindDocument && r.Kind != KindNavigate && (resp.StatusCode < 200 || resp.StatusCode > 299) {
		resp.Body.Close()
		done()
		return nil, fmt.Errorf("%w: status %d", ErrDenied, resp.StatusCode)
	}
	if r.Kind != KindNavigate {
		if err := acceptableFor(r.Kind, resp.Request.URL.Path, resp.Header.Get("Content-Type")); err != nil {
			resp.Body.Close()
			done()
			return nil, err
		}
	}
	out := &OpenResponse{Status: resp.StatusCode, URL: resp.Request.URL.String(), ContentType: resp.Header.Get("Content-Type"), Length: resp.ContentLength, Headers: map[string]string{}, Body: &openBody{ReadCloser: resp.Body, done: done}}
	for _, h := range responseHeaderAllow {
		if v := resp.Header.Get(h); v != "" {
			out.Headers[strings.ToLower(h)] = v
		}
	}
	return out, nil
}

// Do performs one bounded request. It is safe for concurrent use; the loop
// calls it from a worker goroutine, never from the VM goroutine.
func (f *Fetcher) Do(ctx context.Context, r Request) (Response, error) {
	o, err := f.Open(ctx, r)
	if err != nil {
		return Response{}, err
	}
	defer o.Body.Close()
	body, err := io.ReadAll(io.LimitReader(o.Body, f.p.MaxBody+1))
	if err != nil {
		return Response{}, err
	}
	if int64(len(body)) > f.p.MaxBody {
		return Response{}, ErrTooLarge
	}
	return Response{Status: o.Status, StatusText: http.StatusText(o.Status), URL: o.URL, Headers: o.Headers, Body: string(body)}, nil
}

func acceptFor(k Kind) string {
	switch k {
	case KindStyle:
		return "text/css"
	case KindImage:
		return "image/png, image/jpeg, image/gif, image/webp, image/svg+xml"
	case KindNavigate:
		return "text/html, text/*;q=0.9, */*;q=0.8"
	}
	return "text/*, application/json;q=0.9"
}

// acceptableFor gates the response content type per kind. Every branch
// parses the header with mime.ParseMediaType, so parameters such as
// "charset=utf-8" never take part in the comparison.
func acceptableFor(k Kind, path, ct string) error {
	switch k {
	case KindStyle:
		mt, _, err := mime.ParseMediaType(ct)
		if err != nil {
			return fmt.Errorf("%w: unreadable content type", ErrDenied)
		}
		if mt == "text/css" || (mt == "application/octet-stream" && strings.HasSuffix(strings.ToLower(path), ".css")) {
			return nil
		}
		return fmt.Errorf("%w: content type %q is not a stylesheet", ErrDenied, mt)
	case KindImage:
		mt, _, err := mime.ParseMediaType(ct)
		if err != nil {
			return fmt.Errorf("%w: unreadable content type", ErrDenied)
		}
		switch mt {
		case "image/png", "image/jpeg", "image/gif", "image/webp", "image/svg+xml":
			return nil
		}
		return fmt.Errorf("%w: content type %q is not a decodable image", ErrDenied, mt)
	}
	return acceptableContentType(ct)
}

func acceptableContentType(ct string) error {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return fmt.Errorf("%w: unreadable content type", ErrDenied)
	}
	switch {
	case strings.HasPrefix(mt, "text/"), mt == "application/json", strings.HasSuffix(mt, "+json"), mt == "application/xml", mt == "application/xhtml+xml", mt == "application/javascript", mt == "application/ecmascript":
		return nil
	}
	return fmt.Errorf("%w: content type %q", ErrDenied, mt)
}

// isForbiddenIP rejects everything that is not a public unicast address:
// loopback, RFC1918, link-local (169.254/16 holds cloud metadata), CGNAT,
// multicast, unspecified, and the IPv6 transition encodings that embed an
// IPv4 address a resolver or a page can choose (NAT64 64:ff9b::/96 and
// 64:ff9b:1::/48, 6to4 2002::/16, Teredo 2001::/32, IPv4-compatible ::/96).
// A DNS64 answer for an allowlisted name can embed 169.254.169.254; it is
// vetted through its embedded address, as is 6to4. Teredo and the local-use
// NAT64 range are rejected outright. Only IPv4-mapped addresses are unmapped
// first (netip.Addr.Unmap), so no mapped form can skip a case.
func isForbiddenIP(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	return forbiddenAddr(a.Unmap(), 0)
}

var (
	nat64WellKnown = netip.MustParsePrefix("64:ff9b::/96")
	nat64Local     = netip.MustParsePrefix("64:ff9b:1::/48")
	sixToFour      = netip.MustParsePrefix("2002::/16")
	teredo         = netip.MustParsePrefix("2001::/32")
	v4Compatible   = netip.MustParsePrefix("::/96")
	cgnat          = netip.MustParsePrefix("100.64.0.0/10")
	thisNetwork    = netip.MustParsePrefix("0.0.0.0/8")
	ietfProtocol   = netip.MustParsePrefix("192.0.0.0/24")
)

func forbiddenAddr(a netip.Addr, depth int) bool {
	if depth > 1 {
		return true
	}
	if a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsMulticast() || a.IsUnspecified() || a.IsInterfaceLocalMulticast() || !a.IsValid() {
		return true
	}
	if a.Is4() {
		return cgnat.Contains(a) || thisNetwork.Contains(a) || ietfProtocol.Contains(a)
	}
	switch {
	case nat64Local.Contains(a), teredo.Contains(a):
		return true
	case nat64WellKnown.Contains(a), v4Compatible.Contains(a):
		b := a.As16()
		return forbiddenAddr(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), depth+1)
	case sixToFour.Contains(a):
		b := a.As16()
		return forbiddenAddr(netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}), depth+1)
	}
	return false
}

func hostPort(u *url.URL) string {
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return u.Hostname() + ":" + port
}
