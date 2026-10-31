// Package mediaproc is the protocol between ntbrowser and a decoder running in
// its own sandboxed process. It is generic: a handshake names the kind of
// media, and a Decoder behind Serve does the work, so another media type is
// another Decoder. The main process treats the far side as hostile: every
// length is checked before anything is allocated, and any violation ends the
// connection.
package mediaproc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"io"
	"sync"
	"time"
)

// MaxFrameSide bounds a frame's width and height.
const MaxFrameSide = 4096

const (
	maxControlLen = 4096 // any message but a frame
	frameHeader   = 8 + 4 + 4
	serveInterval = time.Second / 30
)

// Message types.
const (
	msgHello = iota + 1
	msgOpen
	msgPlay
	msgPause
	msgSeek
	msgResize
	msgClose
	msgStatus
	msgFrame
)

// State is what a decoder is doing.
type State int

const (
	Idle State = iota
	Loading
	Playing
	Paused
	Ended
	Failed
)

// Status is a decoder's report. Err is the decoder's own text.
type Status struct {
	State              State
	Position, Duration time.Duration
	Err                string
}

// Decoder is one kind of media, in the helper process. Frame returns nil rgba
// when there is no new frame.
type Decoder interface {
	Load(name string) error
	Play() error
	Pause() error
	Seek(time.Duration) error
	Status() (State, time.Duration, time.Duration, error)
	Frame(w, h int) (rgba []byte, fw, fh int, err error)
	Close() error
}

// ErrProtocol is why a client ended a connection.
var ErrProtocol = errors.New("mediaproc: protocol violation")

func writeMsg(w io.Writer, typ byte, payload ...[]byte) error {
	n := 1
	for _, p := range payload {
		n += len(p)
	}
	buf := make([]byte, 5, 5+n)
	binary.BigEndian.PutUint32(buf, uint32(n))
	buf[4] = typ
	for _, p := range payload {
		buf = append(buf, p...)
	}
	_, err := w.Write(buf)
	return err
}

func u64(v uint64) []byte { return binary.BigEndian.AppendUint64(nil, v) }

// Client is the main process's end.
type Client struct {
	conn     io.ReadWriteCloser
	onStatus func(Status)
	onFrame  func(*image.RGBA)
	dead     chan struct{}
	once     sync.Once

	wmu  sync.Mutex
	smu  sync.Mutex
	maxW int // the largest size requested since Open: frames in flight may
	maxH int // still be at an earlier size, never at a larger one
}

// NewClient starts reading from conn. The callbacks run on the reader
// goroutine and must not block.
func NewClient(conn io.ReadWriteCloser, onStatus func(Status), onFrame func(*image.RGBA)) *Client {
	c := &Client{conn: conn, onStatus: onStatus, onFrame: onFrame, dead: make(chan struct{})}
	go c.read()
	return c
}

// Dead is closed when the connection ends, whatever the reason.
func (c *Client) Dead() <-chan struct{} { return c.dead }

func (c *Client) die() {
	c.once.Do(func() {
		c.conn.Close()
		close(c.dead)
	})
}

func (c *Client) send(typ byte, payload ...[]byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	select {
	case <-c.dead:
		return io.ErrClosedPipe
	default:
	}
	return writeMsg(c.conn, typ, payload...)
}

// Handshake names the kind of media this connection is for.
func (c *Client) Handshake(kind string) error { return c.send(msgHello, []byte(kind)) }

// Open loads a file by its name inside the helper's media directory.
func (c *Client) Open(name string) error { return c.send(msgOpen, []byte(name)) }
func (c *Client) Play() error            { return c.send(msgPlay) }
func (c *Client) Pause() error           { return c.send(msgPause) }
func (c *Client) Seek(t time.Duration) error {
	return c.send(msgSeek, u64(uint64(t)))
}

// Resize sets the size frames should fit.
func (c *Client) Resize(w, h int) error {
	if w < 1 || h < 1 || w > MaxFrameSide || h > MaxFrameSide {
		return fmt.Errorf("mediaproc: bad size %dx%d", w, h)
	}
	c.smu.Lock()
	c.maxW, c.maxH = max(c.maxW, w), max(c.maxH, h)
	c.smu.Unlock()
	return c.send(msgResize, binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(nil, uint32(w)), uint32(h)))
}

// Stop releases the media but keeps the helper for the next one.
func (c *Client) Stop() error { return c.send(msgClose) }

// Close ends the connection, and with it the helper.
func (c *Client) Close() error {
	c.die()
	return nil
}

func (c *Client) read() {
	defer c.die()
	var head [5]byte
	for {
		if _, err := io.ReadFull(c.conn, head[:]); err != nil {
			return
		}
		n := binary.BigEndian.Uint32(head[:4])
		switch typ := head[4]; {
		case n < 1:
			return
		case typ == msgStatus:
			if n > maxControlLen {
				return
			}
			body := make([]byte, n-1)
			if _, err := io.ReadFull(c.conn, body); err != nil {
				return
			}
			s, ok := parseStatus(body)
			if !ok {
				return
			}
			if c.onStatus != nil {
				c.onStatus(s)
			}
		case typ == msgFrame:
			if n < 1+frameHeader || n > 1+frameHeader+MaxFrameSide*MaxFrameSide*4 {
				return
			}
			var fh [frameHeader]byte
			if _, err := io.ReadFull(c.conn, fh[:]); err != nil {
				return
			}
			w := binary.BigEndian.Uint32(fh[8:12])
			h := binary.BigEndian.Uint32(fh[12:16])
			c.smu.Lock()
			maxW, maxH := c.maxW, c.maxH
			c.smu.Unlock()
			// Checked before the pixels are allocated.
			if w < 1 || h < 1 || w > uint32(maxW) || h > uint32(maxH) || uint64(n) != 1+frameHeader+uint64(w)*uint64(h)*4 {
				return
			}
			img := image.NewRGBA(image.Rect(0, 0, int(w), int(h)))
			if _, err := io.ReadFull(c.conn, img.Pix); err != nil {
				return
			}
			if c.onFrame != nil {
				c.onFrame(img)
			}
		default:
			return
		}
	}
}

func parseStatus(b []byte) (Status, bool) {
	if len(b) < 1+8+8 {
		return Status{}, false
	}
	s := Status{State: State(b[0]), Position: time.Duration(binary.BigEndian.Uint64(b[1:9])), Duration: time.Duration(binary.BigEndian.Uint64(b[9:17])), Err: string(b[17:])}
	if s.State < Idle || s.State > Failed || s.Position < 0 || s.Duration < 0 {
		return Status{}, false
	}
	return s, true
}

// Serve runs the helper's end until the connection closes. open gives the
// Decoder for the kind named in the handshake.
func Serve(conn io.ReadWriter, open func(kind string) (Decoder, error)) error {
	var head [5]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(head[:4])
	if head[4] != msgHello || n < 1 || n > maxControlLen {
		return ErrProtocol
	}
	kind := make([]byte, n-1)
	if _, err := io.ReadFull(conn, kind); err != nil {
		return err
	}
	d, err := open(string(kind))
	if err != nil {
		return err
	}
	defer d.Close()

	type command struct {
		typ  byte
		body []byte
	}
	commands := make(chan command, 16)
	readErr := make(chan error, 1)
	go func() {
		for {
			if _, err := io.ReadFull(conn, head[:]); err != nil {
				readErr <- err
				return
			}
			n := binary.BigEndian.Uint32(head[:4])
			if n < 1 || n > maxControlLen {
				readErr <- ErrProtocol
				return
			}
			body := make([]byte, n-1)
			if _, err := io.ReadFull(conn, body); err != nil {
				readErr <- err
				return
			}
			commands <- command{head[4], body}
		}
	}()

	w, h := 0, 0
	loaded := false
	var seq uint64
	var last Status
	lastErr := ""
	fail := func(err error) {
		if err != nil {
			lastErr = err.Error()
		}
	}
	tick := time.NewTicker(serveInterval)
	defer tick.Stop()
	for {
		select {
		case err := <-readErr:
			return err
		case c := <-commands:
			switch c.typ {
			case msgOpen:
				lastErr = ""
				fail(d.Load(string(c.body)))
				loaded = true
			case msgPlay:
				fail(d.Play())
			case msgPause:
				fail(d.Pause())
			case msgSeek:
				if len(c.body) == 8 {
					fail(d.Seek(time.Duration(binary.BigEndian.Uint64(c.body))))
				}
			case msgResize:
				if len(c.body) == 8 {
					w, h = int(binary.BigEndian.Uint32(c.body)), int(binary.BigEndian.Uint32(c.body[4:]))
				}
			case msgClose:
				fail(d.Pause())
				loaded = false
			default:
				return ErrProtocol
			}
		case <-tick.C:
			state, pos, dur, err := d.Status()
			fail(err)
			s := Status{State: state, Position: pos, Duration: dur, Err: lastErr}
			if lastErr != "" {
				s.State = Failed
			}
			if s != last {
				last = s
				buf := append([]byte{byte(s.State)}, u64(uint64(s.Position))...)
				buf = append(append(buf, u64(uint64(s.Duration))...), []byte(trim(s.Err))...)
				if err := writeMsg(conn, msgStatus, buf); err != nil {
					return err
				}
			}
			if !loaded || w == 0 {
				continue
			}
			rgba, fw, fh, err := d.Frame(w, h)
			fail(err)
			if rgba == nil {
				continue
			}
			seq++
			head := append(u64(seq), binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(nil, uint32(fw)), uint32(fh))...)
			if err := writeMsg(conn, msgFrame, head, rgba); err != nil {
				return err
			}
		}
	}
}

func trim(s string) string {
	if len(s) > 512 {
		return s[:512]
	}
	return s
}
