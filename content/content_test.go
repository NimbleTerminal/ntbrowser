package content

import (
	"testing"

	"nimbleterminal.dev/ntbrowser/profile"
)

func TestClassifyTable(t *testing.T) {
	for _, c := range []struct {
		mime, url string
		want      Kind
	}{
		{"text/html", "https://a.test/", Page},
		{"text/html; charset=utf-8", "https://a.test/", Page},
		{"TEXT/HTML", "https://a.test/", Page},
		{"application/xhtml+xml", "https://a.test/", Page},
		{"image/gif", "https://a.test/a", Animation},
		{"image/webp", "https://a.test/a", Animation},
		{"image/png", "https://a.test/a", Image},
		{"image/jpeg", "https://a.test/a", Image},
		{"image/svg+xml", "https://a.test/a", Image},
		{"image/x-unknown", "https://a.test/a", Unknown},
		{"video/mp4", "https://a.test/a", Video},
		{"video/webm", "https://a.test/a", Video},
		{"text/plain", "https://a.test/a", Text},
		{"text/css", "https://a.test/a", Text},
		{"application/json", "https://a.test/a", Text},
		{"application/ld+json", "https://a.test/a", Text},
		{"application/pdf", "https://a.test/a", Unknown},
		{"", "https://a.test/a.mp4", Video},
		{"", "https://a.test/a.gif?x=1", Animation},
		{"application/octet-stream", "https://a.test/a.webm", Video},
		{"application/octet-stream", "https://a.test/a.png", Image},
		{"application/octet-stream", "https://a.test/a.html", Unknown},
		{"", "https://a.test/a.html", Unknown},
		{"", "https://a.test/a", Unknown},
		{"not a type;;;", "https://a.test/a", Unknown},
	} {
		if got := Classify(c.mime, c.url); got != c.want {
			t.Errorf("Classify(%q, %q) = %v, want %v", c.mime, c.url, got, c.want)
		}
	}
}

func TestAllowedMatrix(t *testing.T) {
	type row struct {
		media profile.Media
		want  map[Kind]bool
	}
	for _, r := range []row{
		{profile.MediaOff, map[Kind]bool{Page: true, Text: true}},
		{profile.MediaImage, map[Kind]bool{Page: true, Text: true, Image: true}},
		{profile.MediaAnimation, map[Kind]bool{Page: true, Text: true, Image: true, Animation: true}},
		{profile.MediaVideo, map[Kind]bool{Page: true, Text: true, Image: true, Animation: true, Video: true}},
	} {
		for _, k := range []Kind{Page, Text, Image, Animation, Video, Unknown} {
			if got := Allowed(k, r.media); got != r.want[k] {
				t.Errorf("Allowed(%v, %s) = %v", k, r.media, got)
			}
		}
	}
}

// A surface may present less than a node could be, never more.
type lineSurface struct{}

func (lineSurface) Capabilities() Capabilities { return Capabilities{} }
func (lineSurface) Present(*Node) Presentation { return PresentLine }

func TestFakeSurfaceLesserPresentation(t *testing.T) {
	var s Surface = lineSurface{}
	if got := Decide(s, &Node{Kind: Video}, profile.MediaVideo); got != PresentLine {
		t.Fatalf("%v", got)
	}
}

func TestRefusedNodeNeverReachesSurface(t *testing.T) {
	seen := false
	s := surfaceFunc(func(*Node) Presentation { seen = true; return PresentPlayer })
	if got := Decide(s, &Node{Kind: Video}, profile.MediaImage); got != PresentRefused || seen {
		t.Fatalf("%v seen=%v", got, seen)
	}
}

type surfaceFunc func(*Node) Presentation

func (surfaceFunc) Capabilities() Capabilities     { return Capabilities{} }
func (f surfaceFunc) Present(n *Node) Presentation { return f(n) }
