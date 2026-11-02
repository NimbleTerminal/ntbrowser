// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"nimbleterminal.dev/ntmediabox"
	"nimbleterminal.dev/ntpicture"

	"nimbleterminal.dev/ntbrowser/content"
)

const (
	helperStopped = "media helper stopped; restart ntbrowser"
	seekStep      = 5 * time.Second
	progressEvery = 250 * time.Millisecond
)

type (
	videoFrameMsg    struct{}
	videoStatusMsg   struct{ status ntmediabox.Status }
	videoDeadMsg     struct{}
	videoProgressMsg struct{ bytes int64 }
)

// MediaBridge turns a media helper's connection into Bubble Tea messages.
// Frames are latest-wins: a slow terminal drops frames, it does not queue them.
type MediaBridge struct {
	client  *ntmediabox.Client
	send    func(tea.Msg)
	latest  atomic.Pointer[image.RGBA]
	pending atomic.Bool
	dead    atomic.Bool
	kill    func() // ends the helper process; a violation must stop it, not just the socket
}

// NewMediaBridge starts talking to the helper on conn; send must not block.
func NewMediaBridge(conn io.ReadWriteCloser, send func(tea.Msg), kill func()) (*MediaBridge, error) {
	b := &MediaBridge{send: send, kill: kill}
	b.client = ntmediabox.NewClient(conn, func(s ntmediabox.Status) { send(videoStatusMsg{s}) }, func(img *image.RGBA) {
		b.latest.Store(img)
		if b.pending.CompareAndSwap(false, true) {
			send(videoFrameMsg{})
		}
	})
	if err := b.client.Handshake("video"); err != nil {
		b.client.Close()
		return nil, err
	}
	go func() {
		<-b.client.Dead()
		b.dead.Store(true)
		if b.kill != nil {
			b.kill()
		}
		send(videoDeadMsg{})
	}()
	return b, nil
}

// SetMedia connects the helper, when the profile opened video.
func (m *Model) SetMedia(b *MediaBridge, dir string) { m.bridge, m.mediaDir = b, dir }

// Cleanup removes everything the session wrote. Call it after the program ends.
func (m *Model) Cleanup() {
	if m.navCancel != nil {
		m.navCancel()
	}
	if m.bridge != nil {
		m.bridge.client.Close()
		if m.bridge.kill != nil {
			m.bridge.kill()
		}
	}
	if m.mediaDir == "" {
		return
	}
	// By name, not RemoveAll: under landlock the process cannot open the
	// directory's parent. For the same reason the empty directory may stay.
	entries, _ := os.ReadDir(m.mediaDir)
	for _, e := range entries {
		os.Remove(filepath.Join(m.mediaDir, e.Name()))
	}
	os.Remove(m.mediaDir)
}

// SetEnforced records whether the OS actually holds the profile.
func (m *Model) SetEnforced(on bool) { m.notEnforced = !on }

// videoTarget is what load needs to download a video.
type videoTarget struct {
	dir      string
	cap      int64
	active   *atomic.Bool
	dead     bool
	progress func(int64)
}

func randomName() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// download streams a video into the media directory under a random name. It
// leaves no file behind unless it succeeds.
func download(ctx context.Context, body io.Reader, n *content.Node, t *videoTarget, msg *pageLoadedMsg) {
	if t.dead {
		msg.line = helperStopped
		return
	}
	if n.Size > t.cap {
		msg.line = describe(n, "over the media limit")
		return
	}
	t.active.Store(true)
	defer t.active.Store(false)
	name, err := randomName()
	if err != nil {
		msg.err = err
		return
	}
	path := filepath.Join(t.dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		msg.err = err
		return
	}
	done := false
	defer func() {
		f.Close()
		if !done {
			os.Remove(path)
		}
	}()
	var got int64
	last := time.Now()
	buf := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			msg.err = err
			return
		}
		r, rerr := body.Read(buf)
		got += int64(r)
		if got > t.cap {
			msg.line = describe(&content.Node{MIME: n.MIME, Size: got}, "over the media limit")
			return
		}
		if _, err := f.Write(buf[:r]); err != nil {
			msg.err = err
			return
		}
		if t.progress != nil && time.Since(last) > progressEvery {
			last = time.Now()
			t.progress(got)
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			msg.err = rerr
			return
		}
	}
	done = true
	msg.video = name
}

// player is a video playing in the page's place. It is an overlay, not a
// history entry; the page keeps running beneath it.
type player struct {
	pic     *ntpicture.Picture
	name    string
	status  ntmediabox.Status
	playing bool
	dead    bool
	frames  int
	sized   image.Point
}

func (m *Model) framePixels() image.Point {
	if m.player != nil {
		// Glyph geometry (before Kitty is detected) is about a pixel per cell.
		if px := m.player.pic.RasterGeometry().Pixels; px.X > m.cols && px.Y > m.viewRows()*2 {
			return image.Pt(min(px.X, ntmediabox.MaxFrameSide), min(px.Y, ntmediabox.MaxFrameSide))
		}
	}
	return image.Pt(min(m.cols*10, ntmediabox.MaxFrameSide), min(m.viewRows()*20, ntmediabox.MaxFrameSide))
}

func (m *Model) sendSize() {
	if m.player == nil {
		return
	}
	if px := m.framePixels(); px != m.player.sized {
		m.player.sized = px
		m.bridge.client.Resize(px.X, px.Y)
	}
}

func (m *Model) startPlayer(name string) tea.Cmd {
	closing := m.closePlayer() // never two
	pic := ntpicture.New(m.pictureOptions())
	m.player = &player{pic: pic, name: name, playing: true, dead: m.bridge.dead.Load()}
	m.status = ""
	cmd := tea.Batch(closing, pic.Init(), pic.SetSize(m.cols, m.viewRows()))
	m.sendSize()
	m.bridge.client.Open(name)
	m.bridge.client.Play()
	return cmd
}

// closePlayer stops playback and removes the file.
func (m *Model) closePlayer() tea.Cmd {
	if m.player == nil {
		return nil
	}
	p := m.player
	m.player = nil
	m.bridge.client.Stop()
	os.Remove(filepath.Join(m.mediaDir, p.name))
	return p.pic.Close()
}

// playerKey handles a key while a video plays; ctrl+c and q still quit.
func (m *Model) playerKey(k tea.KeyPressMsg) (handled bool, cmd tea.Cmd) {
	p, c := m.player, m.bridge.client
	switch k.String() {
	case "ctrl+c", "q":
		return false, nil
	case "esc":
		if m.current == nil { // a video start URL: no page beneath
			return true, m.quit()
		}
		return true, m.closePlayer()
	case "space":
		if p.playing {
			c.Pause()
		} else {
			c.Play()
		}
		p.playing = !p.playing
	case "left":
		c.Seek(max(p.status.Position-seekStep, 0))
	case "right":
		c.Seek(min(p.status.Position+seekStep, p.status.Duration))
	}
	return true, nil
}

// onVideo handles the helper's messages.
func (m *Model) onVideo(msg tea.Msg) tea.Cmd {
	switch v := msg.(type) {
	case videoProgressMsg:
		m.status = "downloading · " + humanSize(v.bytes)
	case videoDeadMsg:
		if m.player != nil {
			m.player.dead = true
		}
	case videoStatusMsg:
		if m.player != nil {
			m.player.status = v.status
		}
	case videoFrameMsg:
		m.bridge.pending.Store(false)
		img := m.bridge.latest.Swap(nil)
		if m.player == nil || img == nil {
			return nil
		}
		m.player.frames++
		m.sendSize()
		return m.player.pic.SetImageImmutable(img)
	}
	return nil
}

func clock(d time.Duration) string {
	s := int(d / time.Second)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// playerStatus is the status line while a video plays.
func (m *Model) playerStatus() string {
	p := m.player
	switch {
	case p.dead:
		return helperStopped
	case p.status.State == ntmediabox.Failed:
		return clean("can't play: " + p.status.Err)
	case p.status.State == ntmediabox.Loading || p.status.State == ntmediabox.Buffering || p.status.Duration == 0:
		return "loading video"
	}
	mark := "▶"
	if !p.playing {
		mark = "⏸"
	}
	return mark + " " + clock(p.status.Position) + " / " + clock(p.status.Duration) + " · no audio in this profile"
}
