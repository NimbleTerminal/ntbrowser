package subres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"nimbleterminal.dev/ntbrowser/netpolicy"
)

// Mode is the library user's subresource policy (decision 1).
type Mode int

const (
	ModeAny    Mode = iota // browser-like: any public http(s) origin
	ModeOrigin             // the page origin plus the allowlist, like fetch()
	ModeOff                // nothing is fetched (v1 behaviour)
)

func (m Mode) String() string {
	switch m {
	case ModeAny:
		return "any"
	case ModeOrigin:
		return "origin"
	case ModeOff:
		return "off"
	}
	return fmt.Sprintf("Mode(%d)", int(m))
}

// ParseMode reads the -subresources flag value.
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "any":
		return ModeAny, nil
	case "origin":
		return ModeOrigin, nil
	case "off":
		return ModeOff, nil
	}
	return ModeOff, fmt.Errorf("subres: unknown mode %q", s)
}

// Transport performs one bounded request. *netpolicy.Fetcher satisfies it; a
// browser build would implement it over the platform fetch (decision 2).
type Transport interface {
	Do(ctx context.Context, r netpolicy.Request) (netpolicy.Response, error)
}

// Config bounds one page's subresource loading (decision 9).
type Config struct {
	Mode           Mode
	MaxStylesheets int           // distinct sheets per page, @import included (16)
	MaxImages      int           // distinct images per page, backgrounds included (64)
	MaxSheetBytes  int           // bytes per sheet (256 KiB)
	MaxImagePixels int           // decoded pixels per page (16 Mpx)
	MaxInFlight    int           // concurrent requests (6)
	StyleTimeout   time.Duration // first-frame wait for blocking sheets (1.5 s)
	Timeout        time.Duration // per request (10 s)
}

// WithDefaults fills every zero field with its default.
func (c Config) WithDefaults() Config {
	if c.MaxStylesheets <= 0 {
		c.MaxStylesheets = 16
	}
	if c.MaxImages <= 0 {
		c.MaxImages = 64
	}
	if c.MaxSheetBytes <= 0 {
		c.MaxSheetBytes = 256 << 10
	}
	if c.MaxImagePixels <= 0 {
		c.MaxImagePixels = 16 << 20
	}
	if c.MaxInFlight <= 0 {
		c.MaxInFlight = 6
	}
	if c.StyleTimeout <= 0 {
		c.StyleTimeout = 1500 * time.Millisecond
	}
	if c.Timeout <= 0 {
		c.Timeout = 10 * time.Second
	}
	return c
}
