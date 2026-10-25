package netpolicy

import (
	"net/url"
	"testing"
)

// fetch("//other.host/x"), javascript:, data: and file: all resolve against
// the page and are refused unless allowlisted (plan Review Focus 2).
func TestResolveRelativeAndProtocolRelative(t *testing.T) {
	origin, _ := url.Parse("https://page.test/dir/index.html")
	f := New(Policy{Origin: origin, AllowHosts: []string{"cdn.test"}})
	cases := map[string]bool{ // ref -> allowed
		"data.json": true, "/api": true, "//page.test/x": true, "//cdn.test/x": true,
		"//evil.test/x": false, "javascript:alert(1)": false, "data:text/html,x": false, "file:///etc/passwd": false,
		"http://page.test/x": false, // scheme differs: not same-origin
	}
	for ref, want := range cases {
		u, err := f.Resolve(ref)
		got := err == nil && f.Allowed(u) == nil
		if got != want {
			t.Errorf("%q: allowed=%v want %v (err=%v)", ref, got, want, err)
		}
	}
}

func FuzzResolve(f *testing.F) {
	origin, _ := url.Parse("https://page.test/")
	fx := New(Policy{Origin: origin})
	f.Add("//x")
	f.Add("\x00")
	f.Add("http://[::1]:80/")
	f.Fuzz(func(t *testing.T, ref string) {
		u, err := fx.Resolve(ref)
		if err != nil {
			return
		}
		if fx.Allowed(u) == nil && (u.Scheme != "https" || u.Hostname() != "page.test") {
			t.Fatalf("%q resolved to admitted off-origin %s", ref, u)
		}
	})
}
