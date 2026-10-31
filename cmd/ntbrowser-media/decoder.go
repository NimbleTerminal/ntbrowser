package main

import (
	"errors"
	"image"
	"image/draw"
	"path/filepath"
	"strings"
	"time"

	"nimbleterminal.dev/ntbrowser/mediaproc"
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
func (d *videoDecoder) Load(name string) error {
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return errors.New("bad media name")
	}
	return d.p.Load(filepath.Join(d.dir, name))
}

func (d *videoDecoder) Play() error                { return d.p.Play() }
func (d *videoDecoder) Pause() error               { return d.p.Pause() }
func (d *videoDecoder) Seek(t time.Duration) error { return d.p.Seek(t) }
func (d *videoDecoder) Close() error               { return d.p.Close() }

func (d *videoDecoder) Status() (mediaproc.State, time.Duration, time.Duration, error) {
	s, err := d.p.Status()
	if err == nil {
		err = s.Err
	}
	return stateOf(s.State), s.CurrentTime, s.Duration, err
}

func stateOf(s ntvideo.State) mediaproc.State {
	switch s {
	case ntvideo.Loading, ntvideo.Buffering:
		return mediaproc.Loading
	case ntvideo.Playing:
		return mediaproc.Playing
	case ntvideo.Paused:
		return mediaproc.Paused
	case ntvideo.Ended:
		return mediaproc.Ended
	case ntvideo.Failed:
		return mediaproc.Failed
	}
	return mediaproc.Idle
}

func (d *videoDecoder) Frame(w, h int) ([]byte, int, int, error) {
	img, err := d.p.Frame(w, h)
	if err != nil || img == nil {
		return nil, 0, 0, err
	}
	b := img.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), img, b.Min, draw.Src)
	return rgba.Pix, b.Dx(), b.Dy(), nil
}
