package netpolicy

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// Transition-prefix forms of forbidden IPv4 addresses are forbidden too; the
// same forms of public addresses are not (NAT64-only networks must work).
func TestForbiddenIPCoversTransitionPrefixes(t *testing.T) {
	deny := []string{
		"64:ff9b::a9fe:a9fe",   // NAT64 of 169.254.169.254
		"64:ff9b::a00:1",       // NAT64 of 10.0.0.1
		"64:ff9b::7f00:1",      // NAT64 of 127.0.0.1
		"64:ff9b:1::a9fe:a9fe", // local-use NAT64, any payload
		"64:ff9b:1::808:808",
		"2002:a9fe:a9fe::1",                    // 6to4 of 169.254.169.254
		"2002:0a00:0001::1",                    // 6to4 of 10.0.0.1
		"2001:0:4136:e378:8000:63bf:3fff:fdd2", // Teredo
		"::a9fe:a9fe",                          // IPv4-compatible 169.254.169.254
		"::ffff:169.254.169.254",
		"169.254.169.254", "10.0.0.1", "100.64.0.1", "127.0.0.1", "0.0.0.0", "192.0.0.1", "::1", "fe80::1", "fc00::1",
	}
	allow := []string{"64:ff9b::808:808", "2002:808:808::1", "::ffff:8.8.8.8", "8.8.8.8", "2606:4700::1111"}
	for _, s := range deny {
		if !isForbiddenIP(net.ParseIP(s)) {
			t.Errorf("%s should be forbidden", s)
		}
	}
	for _, s := range allow {
		if isForbiddenIP(net.ParseIP(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
	if !isForbiddenIP(nil) || !isForbiddenIP(net.IP{1, 2, 3}) {
		t.Error("malformed addresses must be forbidden")
	}
}

// AnyOrigin (top-level navigation) admits other hosts and follows redirects
// across scheme and host, but keeps every other check.
func TestAnyOriginFollowsRedirectsButStillVetsAddresses(t *testing.T) {
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<p>landed</p>"))
	}))
	defer final.Close()
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL+"/landing", http.StatusFound)
	}))
	defer first.Close()
	origin, _ := url.Parse(first.URL)

	// Same-origin policy (a page's fetch): the cross-port redirect is denied.
	strict := New(Policy{Origin: origin, AllowPrivate: true})
	if _, err := strict.Do(t.Context(), Request{Method: "GET", URL: first.URL + "/go"}); err == nil {
		t.Fatal("same-origin fetcher followed a redirect to another origin")
	}
	// Navigation policy: followed.
	nav := New(Policy{Origin: origin, AllowPrivate: true, AnyOrigin: true})
	resp, err := nav.Do(t.Context(), Request{Method: "GET", URL: first.URL + "/go"})
	if err != nil || resp.Body != "<p>landed</p>" {
		t.Fatalf("navigation did not follow the redirect: %v %q", err, resp.Body)
	}
	if u, _ := url.Parse(resp.URL); u.Host != origin.Host && u.Path != "/landing" {
		t.Fatalf("final URL not reported: %s", resp.URL)
	}

	// Without AllowPrivate, AnyOrigin still refuses forbidden targets and
	// everything that is not http(s).
	pub := New(Policy{AnyOrigin: true})
	for _, raw := range []string{"http://[64:ff9b::a9fe:a9fe]/", "http://10.0.0.1/", "http://localhost/", "http://user:pw@example.com/", "file:///etc/passwd", "javascript:alert(1)"} {
		u, _ := url.Parse(raw)
		if err := pub.Allowed(u); err == nil {
			t.Errorf("AnyOrigin admitted %s", raw)
		}
	}
	u, _ := url.Parse("https://example.com/anything")
	if err := pub.Allowed(u); err != nil {
		t.Errorf("AnyOrigin refused a public host: %v", err)
	}
}
