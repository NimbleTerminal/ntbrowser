package mediaproc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"net"
	"sync"
	"testing"
	"time"
)

type fakeDecoder struct {
	mu      sync.Mutex
	ops     []string
	state   State
	pos     time.Duration
	frameFn func(w, h int) ([]byte, int, int, error)
	closed  bool
}

func (d *fakeDecoder) op(s string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ops = append(d.ops, s)
	return nil
}
func (d *fakeDecoder) Load(name string) error { return d.op("load " + name) }
func (d *fakeDecoder) Play() error {
	d.mu.Lock()
	d.state = Playing
	d.mu.Unlock()
	return d.op("play")
}
func (d *fakeDecoder) Pause() error               { return d.op("pause") }
func (d *fakeDecoder) Seek(t time.Duration) error { d.pos = t; return d.op("seek") }
func (d *fakeDecoder) Status() (State, time.Duration, time.Duration, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state, d.pos, time.Minute, nil
}
func (d *fakeDecoder) Frame(w, h int) ([]byte, int, int, error) {
	d.mu.Lock()
	fn := d.frameFn
	d.mu.Unlock()
	if fn == nil {
		return nil, 0, 0, nil
	}
	return fn(w, h)
}
func (d *fakeDecoder) Close() error { d.closed = true; return d.op("close") }

func (d *fakeDecoder) has(s string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, o := range d.ops {
		if o == s {
			return true
		}
	}
	return false
}

// pair connects a client to a served fake decoder.
func pair(t *testing.T, d *fakeDecoder) (*Client, chan Status, chan *image.RGBA) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })
	go Serve(b, func(kind string) (Decoder, error) {
		if kind != "video" {
			return nil, errors.New("unknown kind")
		}
		return d, nil
	})
	statuses := make(chan Status, 64)
	frames := make(chan *image.RGBA, 64)
	c := NewClient(a, func(s Status) {
		select {
		case statuses <- s:
		default:
		}
	}, func(img *image.RGBA) {
		select {
		case frames <- img:
		default:
		}
	})
	if err := c.Handshake("video"); err != nil {
		t.Fatal(err)
	}
	return c, statuses, frames
}

func waitDecoder(t *testing.T, d *fakeDecoder, op string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !d.has(op) {
		if time.Now().After(deadline) {
			t.Fatalf("decoder never saw %q: %v", op, d.ops)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPlaybackRoundTrip(t *testing.T) {
	d := &fakeDecoder{frameFn: func(w, h int) ([]byte, int, int, error) {
		return bytes.Repeat([]byte{1, 2, 3, 4}, 2*2), 2, 2, nil
	}}
	c, _, frames := pair(t, d)
	for _, step := range []func() error{
		func() error { return c.Resize(4, 4) },
		func() error { return c.Open("abc") },
		c.Play, c.Pause,
		func() error { return c.Seek(3 * time.Second) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	for _, op := range []string{"load abc", "play", "pause", "seek"} {
		waitDecoder(t, d, op)
	}
	select {
	case img := <-frames:
		if img.Bounds() != image.Rect(0, 0, 2, 2) || img.Pix[0] != 1 || img.Pix[15] != 4 {
			t.Fatalf("%v %v", img.Bounds(), img.Pix)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no frame")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStatusFlows(t *testing.T) {
	d := &fakeDecoder{}
	c, statuses, _ := pair(t, d)
	c.Resize(4, 4)
	c.Open("abc")
	c.Play()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case s := <-statuses:
			if s.State == Playing && s.Duration == time.Minute {
				return
			}
		case <-deadline:
			t.Fatal("no playing status")
		}
	}
}

func TestUnknownKindFails(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	done := make(chan error, 1)
	go func() {
		done <- Serve(b, func(string) (Decoder, error) { return nil, errors.New("no") })
	}()
	c := NewClient(a, nil, nil)
	c.Handshake("audio")
	if err := <-done; err == nil {
		t.Fatal("served an unknown kind")
	}
}

// raw lets a test play a hostile helper.
func rawClient(t *testing.T, w, h int) (*Client, net.Conn, chan *image.RGBA) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })
	frames := make(chan *image.RGBA, 4)
	c := NewClient(a, nil, func(img *image.RGBA) { frames <- img })
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := b.Read(buf); err != nil {
				return
			}
		}
	}()
	if w > 0 {
		if err := c.Resize(w, h); err != nil {
			t.Fatal(err)
		}
	}
	return c, b, frames
}

func frameMsg(seq uint64, w, h uint32, pix []byte) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, uint32(1+8+4+4+len(pix)))
	b.WriteByte(msgFrame)
	binary.Write(&b, binary.BigEndian, seq)
	binary.Write(&b, binary.BigEndian, w)
	binary.Write(&b, binary.BigEndian, h)
	b.Write(pix)
	return b.Bytes()
}

func waitDead(t *testing.T, c *Client) {
	t.Helper()
	select {
	case <-c.Dead():
	case <-time.After(2 * time.Second):
		t.Fatal("hostile message did not kill the client")
	}
}

func TestFrameSizeAcrossResize(t *testing.T) {
	c, b, frames := rawClient(t, 4, 4)
	if err := c.Resize(2, 2); err != nil { // smaller now; a 4×4 frame is still in flight
		t.Fatal(err)
	}
	go b.Write(frameMsg(1, 4, 4, make([]byte, 4*4*4)))
	select {
	case img := <-frames:
		if img.Bounds().Dx() != 4 {
			t.Fatal(img.Bounds())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight frame at the earlier size refused")
	}
	go b.Write(frameMsg(2, 5, 4, make([]byte, 5*4*4))) // larger than ever requested
	waitDead(t, c)
}

func TestOversizeFrameKills(t *testing.T) {
	c, b, _ := rawClient(t, 4, 4)
	go b.Write(frameMsg(1, MaxFrameSide+1, 1, nil))
	waitDead(t, c)
}

func TestLengthLieKills(t *testing.T) {
	c, b, _ := rawClient(t, 4, 4)
	go b.Write(frameMsg(1, 2, 2, make([]byte, 3))) // 2×2 needs 16 bytes
	waitDead(t, c)
}

func TestHugeLengthKillsWithoutAllocating(t *testing.T) {
	c, b, _ := rawClient(t, 4, 4)
	go b.Write([]byte{0xff, 0xff, 0xff, 0xff, msgFrame})
	waitDead(t, c)
}

func TestBadMessageKills(t *testing.T) {
	c, b, _ := rawClient(t, 4, 4)
	go b.Write([]byte{0, 0, 0, 1, 99})
	waitDead(t, c)
}

func TestDeadClosesOnEOF(t *testing.T) {
	c, b, _ := rawClient(t, 0, 0)
	b.Close()
	waitDead(t, c)
	if err := c.Play(); err == nil {
		t.Fatal("write after death succeeded")
	}
}

func FuzzClientRead(f *testing.F) {
	f.Add(frameMsg(1, 2, 2, make([]byte, 16)))
	f.Add([]byte{0, 0, 0, 1, 99})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})
	f.Add([]byte{0, 0, 0, 3, msgStatus, 1, 2})
	f.Fuzz(func(t *testing.T, data []byte) {
		a, b := net.Pipe()
		defer a.Close()
		defer b.Close()
		c := NewClient(a, func(Status) {}, func(*image.RGBA) {})
		go func() {
			buf := make([]byte, 64)
			for {
				if _, err := b.Read(buf); err != nil {
					return
				}
			}
		}()
		c.Resize(8, 8)
		go b.Write(data)
		select {
		case <-c.Dead():
		case <-time.After(5 * time.Millisecond):
		}
	})
}
