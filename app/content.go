package app

import (
	"bytes"
	"context"
	"errors"
	"html"
	"image"
	_ "image/jpeg" // still images
	_ "image/png"
	"io"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/nthtml/document"
	"nimbleterminal.dev/ntpicture"
	"nimbleterminal.dev/ntpicture/animation"
	"nimbleterminal.dev/ntpicture/playback"

	"nimbleterminal.dev/ntbrowser/content"
	"nimbleterminal.dev/ntbrowser/netpolicy"
	"nimbleterminal.dev/ntbrowser/profile"
)

const (
	pageReadTimeout = 15 * time.Second
	navTimeout      = 15 * time.Second // reaching the response headers
)

// navIdle ends a media download that has stopped making progress.
var navIdle = 30 * time.Second

// termSurface presents content in the terminal. player says whether a media
// helper is connected.
type termSurface struct{ player bool }

func (termSurface) Capabilities() content.Capabilities {
	return content.Capabilities{KittyGraphics: true}
}

func (s termSurface) Present(n *content.Node) content.Presentation {
	switch n.Kind {
	case content.Page, content.Text:
		return content.PresentPage
	case content.Image:
		return content.PresentImage
	case content.Animation:
		return content.PresentAnimation
	case content.Video:
		if s.player {
			return content.PresentPlayer
		}
	}
	return content.PresentLine
}

// describe is the one-line summary of content ntbrowser will not present.
func describe(n *content.Node, what string) string {
	mime, _, _ := strings.Cut(n.MIME, ";")
	if mime = strings.TrimSpace(mime); mime == "" {
		mime = "no type"
	}
	if n.Size < 0 {
		return mime + " · " + what
	}
	return mime + " · " + humanSize(n.Size) + " · " + what
}

func humanSize(n int64) string {
	switch {
	case n < 1<<10:
		return strconv.FormatInt(n, 10) + " B"
	case n < 1<<20:
		return strconv.FormatInt(n>>10, 10) + " KB"
	}
	return strconv.FormatInt(n>>20, 10) + " MB"
}

// load fetches u and turns the response into a pageLoadedMsg: classified from
// its headers, checked against the profile, and only then read, by kind.
func load(ctx context.Context, stop context.CancelFunc, f *netpolicy.Fetcher, u string, p profile.Profile, video *videoTarget, msg pageLoadedMsg) pageLoadedMsg {
	o, err := f.Open(ctx, netpolicy.Request{Method: "GET", URL: u, Kind: netpolicy.KindNavigate})
	if err != nil {
		msg.err = err
		return msg
	}
	defer o.Body.Close()
	idle := time.AfterFunc(navIdle, stop)
	defer idle.Stop()
	body := &idleReader{r: o.Body, idle: idle}
	n := &content.Node{Kind: content.Classify(o.ContentType, o.URL), MIME: o.ContentType, URL: o.URL, Size: o.Length}
	msg.finalURL = o.URL
	switch pres := content.Decide(termSurface{player: video != nil}, n, p.Media); pres {
	case content.PresentPlayer:
		download(ctx, body, n, video, &msg)
	case content.PresentRefused:
		msg.line = n.Kind.String() + " is off in this profile"
	case content.PresentLine:
		msg.line = describe(n, "can't display")
	case content.PresentPage:
		timer := time.AfterFunc(pageReadTimeout, func() { o.Body.Close() })
		defer timer.Stop()
		page, err := readCapped(body, 1<<20)
		if err != nil {
			msg.err = err
			return msg
		}
		msg.html = string(page)
		if n.Kind == content.Text {
			msg.html = "<!doctype html><pre>" + html.EscapeString(msg.html) + "</pre>"
		}
	case content.PresentImage, content.PresentAnimation:
		if o.Length > p.MaxMediaBytes {
			msg.line = describe(n, "over the media limit")
			return msg
		}
		data, err := readCapped(body, p.MaxMediaBytes)
		if errors.Is(err, netpolicy.ErrTooLarge) {
			msg.line = describe(n, "over the media limit")
			return msg
		}
		if err != nil {
			msg.err = err
			return msg
		}
		if pres == content.PresentImage {
			msg.img, err = decodeStill(data, n.MIME)
		} else {
			msg.img, msg.player, err = animation.DecodeContext(ctx, data, animation.Options{Animate: true})
		}
		if err != nil {
			msg.line = describe(n, "can't decode")
		}
	}
	return msg
}

// idleReader restarts its timer on every read that makes progress.
type idleReader struct {
	r    io.Reader
	idle *time.Timer
}

func (i *idleReader) Read(p []byte) (int, error) {
	n, err := i.r.Read(p)
	if n > 0 {
		i.idle.Reset(navIdle)
	}
	return n, err
}

// maxImagePixels bounds a decoded still; animation has its own limits.
const maxImagePixels = 32 << 20

func decodeStill(data []byte, mime string) (image.Image, error) {
	if strings.HasPrefix(strings.ToLower(mime), "image/svg") {
		return document.DecodeImage(bytes.NewReader(data)) // bounded SVG through ntsvg
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxImagePixels/cfg.Height {
		return nil, errors.New("image too large")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

func readCapped(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, netpolicy.ErrTooLarge
	}
	return body, nil
}

// mediaView is a full-view picture in place of the page.
type mediaView struct {
	pic      *ntpicture.Picture   // a still
	ctrl     *playback.Controller // a moving picture
	animated bool
	closed   bool
}

func newMediaView(img image.Image, player *animation.Player, opts ntpicture.Options, cols, rows int) (*mediaView, tea.Cmd) {
	if player != nil {
		c := playback.New(player, playback.Options{Picture: opts})
		return &mediaView{ctrl: c, animated: true}, tea.Batch(c.Init(), c.SetSize(cols, rows))
	}
	p := ntpicture.New(opts)
	return &mediaView{pic: p}, tea.Batch(p.Init(), p.SetSize(cols, rows), p.SetImage(img))
}

func (v *mediaView) view() string {
	if v.ctrl != nil {
		return v.ctrl.View()
	}
	return v.pic.View()
}

func (v *mediaView) update(msg tea.Msg) tea.Cmd {
	if v.ctrl != nil {
		return v.ctrl.Update(msg)
	}
	return v.pic.Update(msg)
}

func (v *mediaView) setSize(cols, rows int) tea.Cmd {
	if v.ctrl != nil {
		return v.ctrl.SetSize(cols, rows)
	}
	return v.pic.SetSize(cols, rows)
}

func (v *mediaView) close() tea.Cmd {
	v.closed = true
	if v.ctrl != nil {
		return v.ctrl.Close()
	}
	return v.pic.Close()
}

// pictureOptions are for pictures shown in the page's place. They share the
// viewer's terminal session, so Kitty discovery need not happen again.
func (m *Model) pictureOptions() ntpicture.Options {
	opts := viewerOptions.Picture
	opts.AutoMode = true
	if m.viewer != nil {
		opts.Session = m.viewer.Picture().Session()
	}
	return opts
}

// closeMedia releases a shown picture, if any.
func (m *Model) closeMedia() tea.Cmd {
	if m.media == nil {
		return nil
	}
	cmd := m.media.close()
	m.media = nil
	return cmd
}
