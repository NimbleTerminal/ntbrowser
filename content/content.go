// Package content is what incoming content is, apart from where it will be
// shown. Navigation classifies a response from its headers into a Node; the
// core checks the profile; a Surface then chooses how to present what is
// allowed. For a page the DOM stays the tree: a Page node only refers to it.
package content

import (
	"net/url"
	"path"
	"strings"

	"nimbleterminal.dev/ntbrowser/profile"
)

// Kind is what a response is.
type Kind int

const (
	Unknown Kind = iota
	Page
	Text
	Image
	Animation
	Video
)

func (k Kind) String() string {
	return [...]string{"unknown", "page", "text", "image", "animation", "video"}[k]
}

// Node is one piece of content. Size is -1 when unknown.
type Node struct {
	Kind          Kind
	MIME          string
	URL           string
	Poster        string
	Size          int64
	Width, Height int
	Children      []*Node
}

// mediaKinds are the extensions believed when the server names no usable
// type. Page is deliberately absent: only a server's type makes a page.
var mediaKinds = map[string]Kind{
	".png": Image, ".jpg": Image, ".jpeg": Image, ".svg": Image,
	".gif": Animation, ".webp": Animation,
	".mp4": Video, ".m4v": Video, ".webm": Video, ".mov": Video,
}

// Classify decides a Kind from a Content-Type and the URL.
func Classify(mime, rawURL string) Kind {
	mt, _, _ := strings.Cut(mime, ";")
	mt = strings.ToLower(strings.TrimSpace(mt))
	switch {
	case mt == "text/html", mt == "application/xhtml+xml":
		return Page
	case mt == "image/gif", mt == "image/webp":
		return Animation
	case mt == "image/png", mt == "image/jpeg", mt == "image/svg+xml":
		return Image
	case strings.HasPrefix(mt, "video/"):
		return Video
	case strings.HasPrefix(mt, "text/"), mt == "application/json", strings.HasSuffix(mt, "+json"):
		return Text
	case mt == "", mt == "application/octet-stream":
		if u, err := url.Parse(rawURL); err == nil {
			return mediaKinds[strings.ToLower(path.Ext(u.Path))]
		}
	}
	return Unknown
}

// Allowed reports whether the media gate permits presenting k. Pages and text
// are not media; unknown content is never presented.
func Allowed(k Kind, m profile.Media) bool {
	switch k {
	case Page, Text:
		return true
	case Image:
		return m.Allows(profile.MediaImage)
	case Animation:
		return m.Allows(profile.MediaAnimation)
	case Video:
		return m.Allows(profile.MediaVideo)
	}
	return false
}

// Capabilities is what a surface can draw.
type Capabilities struct {
	KittyGraphics bool // terminal: Kitty placements
	Composited    bool // native: composited into a frame
	HTMLVideo     bool // wasm browser: a real <video> element
}

// Presentation is how a surface will show a node.
type Presentation int

const (
	PresentRefused   Presentation = iota // the profile refused it; no surface saw it
	PresentLine                          // one status line
	PresentPage                          // laid out as a page
	PresentImage                         // a still picture
	PresentAnimation                     // a moving picture
	PresentPlayer                        // a video player
)

// Surface is what a host implements. It may choose a lesser presentation
// than the node could have, never a greater one.
type Surface interface {
	Capabilities() Capabilities
	Present(n *Node) Presentation
}

// Decide is the one path from a node to a presentation: the profile first,
// so a gate applies the same way on every surface.
func Decide(s Surface, n *Node, m profile.Media) Presentation {
	if !Allowed(n.Kind, m) {
		if n.Kind == Unknown {
			return PresentLine
		}
		return PresentRefused
	}
	return s.Present(n)
}
