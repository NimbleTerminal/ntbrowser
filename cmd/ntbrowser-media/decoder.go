// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package main

import (
	"errors"
	"image"
	"image/draw"
	"path/filepath"
	"strings"
	"time"

	"nimbleterminal.dev/ntmediabox"
	"nimbleterminal.dev/ntvideo"
)

// videoDecoder plays files from the media directory: software decoding, no
// audio (forms the helper's minimal sandbox, design decision 24).
type videoDecoder struct {
	dir string
	p   *ntvideo.Player
}

func newVideoDecoder(dir string) *videoDecoder {
	return &videoDecoder{dir: dir, p: ntvideo.NewPlayer(ntvideo.PlayerOptions{DisableHardwareDecoding: true, DisableAudio: true})}
}

// Load opens a file by its bare name; the main process chooses names.
func (d *videoDecoder) Load(req ntmediabox.OpenRequest) error {
	name := req.Name
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return errors.New("bad media name")
	}
	return d.p.Load(filepath.Join(d.dir, name))
}

func (d *videoDecoder) Play() error                      { return d.p.Play() }
func (d *videoDecoder) Pause() error                     { return d.p.Pause() }
func (d *videoDecoder) Seek(t time.Duration) error       { return d.p.Seek(t) }
func (d *videoDecoder) SelectTrack(string, string) error { return nil }
func (d *videoDecoder) SetCaptionOverlay(bool) error     { return nil }
func (d *videoDecoder) Unload() error {
	err := d.p.Close()
	d.p = ntvideo.NewPlayer(ntvideo.PlayerOptions{DisableHardwareDecoding: true, DisableAudio: true})
	return err
}
func (d *videoDecoder) Close() error { return d.p.Close() }

func (d *videoDecoder) Status() (ntmediabox.Status, error) {
	s, err := d.p.Status()
	if err == nil {
		err = s.Err
	}
	return ntmediabox.Status{
		State:    stateOf(s.State),
		Position: s.CurrentTime,
		Duration: s.Duration,
	}, err
}

func stateOf(s ntvideo.State) ntmediabox.State {
	switch s {
	case ntvideo.Loading, ntvideo.Buffering:
		return ntmediabox.Loading
	case ntvideo.Playing:
		return ntmediabox.Playing
	case ntvideo.Paused:
		return ntmediabox.Paused
	case ntvideo.Ended:
		return ntmediabox.Ended
	case ntvideo.Failed:
		return ntmediabox.Failed
	}
	return ntmediabox.Idle
}

func (d *videoDecoder) Frame(w, h int) ([]byte, int, int, error) {
	img, err := d.p.Frame(w, h)
	if err != nil || img == nil {
		return nil, 0, 0, err
	}
	pix, fw, fh := pixels(img)
	return pix, fw, fh, nil
}

// pixels returns img as RGBA bytes. Player frames are opaque, so their NRGBA
// bytes are already RGBA and go out without a copy.
func pixels(img image.Image) ([]byte, int, int) {
	b := img.Bounds()
	if n, ok := img.(*image.NRGBA); ok && n.Stride == 4*b.Dx() && len(n.Pix) == 4*b.Dx()*b.Dy() {
		return n.Pix, b.Dx(), b.Dy()
	}
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), img, b.Min, draw.Src)
	return rgba.Pix, b.Dx(), b.Dy()
}
