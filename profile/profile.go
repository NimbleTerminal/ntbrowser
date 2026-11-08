// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

// Package profile is ntbrowser's security profile: one position for each gate,
// the complete list of ways out of the sandbox. A profile is built before the
// program starts, passed by value, and frozen; nothing a page can reach reads
// or changes it. The zero Profile has every gate closed. See
// docs/design/2026-10-31-ntbrowser-profiles-content-design.md §5.
package profile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

type (
	Navigate         string
	ScriptNavigation string
	Subresources     string
	Fetch            string
	Scripts          string
	ScriptSources    string
	DynamicCode      string
	Cookies          string
	SessionStorage   string
	LocalStorage     string
	Persist          string
	Files            string
	Media            string
	Clipboard        string
	Enforcement      string
)

const (
	ScriptNavOff       ScriptNavigation = "off"
	ScriptNavOrigin    ScriptNavigation = "origin"
	ScriptNavAllowlist ScriptNavigation = "allowlist"
	ScriptNavConfirm   ScriptNavigation = "confirm"

	NavOff       Navigate = "off"
	NavAllowlist Navigate = "allowlist"
	NavConfirm   Navigate = "confirm"
	NavFree      Navigate = "free"

	SubOff    Subresources = "off"
	SubOrigin Subresources = "origin"
	SubAny    Subresources = "any"

	FetchOff       Fetch = "off"
	FetchOrigin    Fetch = "origin"
	FetchAllowlist Fetch = "allowlist"

	SessionStorageOff SessionStorage = "off"
	SessionStorageOn  SessionStorage = "on"

	LocalStorageOff LocalStorage = "off"
	LocalStorageOn  LocalStorage = "on" // localStorage over a second origin-isolated store

	PersistOff  Persist = "off"  // every store is memory-only for the run
	PersistFile Persist = "file" // cookies with an expiry and localStorage survive restarts in owner-only files

	CookiesOff     Cookies = "off"
	CookiesSession Cookies = "session"

	DynamicOff DynamicCode = "off"
	DynamicOn  DynamicCode = "on"

	ScriptSourcesGrants ScriptSources = "grants"
	ScriptSourcesAny    ScriptSources = "any"

	ScriptsOff      Scripts = "off"
	ScriptsInline   Scripts = "inline"
	ScriptsExternal Scripts = "external"

	FilesOff       Files = "off"
	FilesStartPage Files = "start-page"
	FilesTemp      Files = "temp"

	MediaOff       Media = "off"
	MediaImage     Media = "image"
	MediaAnimation Media = "animation"
	MediaVideo     Media = "video"

	ClipboardOff  Clipboard = "off"
	ClipboardCopy Clipboard = "copy"

	EnforceRequired   Enforcement = "required"
	EnforceBestEffort Enforcement = "best-effort"
)

// DefaultMaxMediaBytes caps a media download unless a profile says otherwise.
const DefaultMaxMediaBytes int64 = 64 << 20

// defaultClockMillis is the script clock's resolution in the built-ins.
const defaultClockMillis = 10

// Profile is one position for each gate. Code compares against the open
// values, so an empty field is closed.
type Profile struct {
	ScriptSources ScriptSources `json:"script-sources,omitempty"`
	FrameDOM      string        `json:"frame-dom,omitempty"`
	Frames        Frames        `json:"frames,omitempty"`
	FrameGrants   []ScriptGrant `json:"frame-grants,omitempty"`
	ScriptGrants  []ScriptGrant `json:"script-grants,omitempty"`
	ScriptPrompts bool          `json:"script-prompts,omitempty"`

	SessionStorage           SessionStorage   `json:"session-storage,omitempty"`
	SessionStorageBytes      int              `json:"session-storage-bytes,omitempty"`
	SessionStorageTotalBytes int              `json:"session-storage-total-bytes,omitempty"`
	LocalStorage             LocalStorage     `json:"local-storage,omitempty"`
	Persist                  Persist          `json:"persist,omitempty"`
	FrameMillis              int              `json:"frame-ms,omitempty"`
	FrameCallbacks           int              `json:"frame-callbacks,omitempty"`
	FrameBudgetMillis        int              `json:"frame-budget-ms,omitempty"`
	ScriptNavigation         ScriptNavigation `json:"script-navigation,omitempty"`
	ScriptNavigationLimit    int              `json:"script-navigation-limit,omitempty"`
	Cookies                  Cookies          `json:"cookies,omitempty"`
	DevTools                 bool             `json:"devtools,omitempty"`
	Diagnostics              bool             `json:"diagnostics,omitempty"`
	Name                     string           `json:"name,omitempty"`
	Navigate                 Navigate         `json:"navigate,omitempty"`
	Subresources             Subresources     `json:"subresources,omitempty"`
	Fetch                    Fetch            `json:"fetch,omitempty"`
	Hosts                    []string         `json:"hosts,omitempty"`
	PrivateNetwork           bool             `json:"private-network,omitempty"`
	Scripts                  Scripts          `json:"scripts,omitempty"`
	DynamicCode              DynamicCode      `json:"dynamic-code,omitempty"`
	ClockMillis              int              `json:"clock,omitempty"`
	Files                    Files            `json:"files,omitempty"`
	NativeCode               bool             `json:"native-code,omitempty"`
	Media                    Media            `json:"media,omitempty"`
	MaxMediaBytes            int64            `json:"max-media-bytes,omitempty"`
	Clipboard                Clipboard        `json:"clipboard,omitempty"`
	Enforcement              Enforcement      `json:"enforcement,omitempty"`
}

// Builtin returns a built-in profile: locked, reader, browse or glynx.
func Builtin(name string) (Profile, bool) {
	switch name {
	case "glynx":
		p, _ := Builtin("browse")
		p.Name = name
		p.Scripts = ScriptsExternal
		p.ScriptSources = ScriptSourcesAny
		p.DynamicCode = DynamicOn
		p.Cookies = CookiesSession
		p.SessionStorage = SessionStorageOn
		p.LocalStorage = LocalStorageOn
		p.Persist = PersistFile // a daily driver keeps its logins and site settings
		p.Frames = FramesAny
		p.FrameDOM = "same-origin"
		p.Fetch = FetchOrigin
		p.ScriptNavigation = ScriptNavConfirm
		return p, true
	case "locked":
		return Profile{Name: name, Navigate: NavOff, Subresources: SubOff, Fetch: FetchOff, Scripts: ScriptsOff, Files: FilesOff, Media: MediaOff, Clipboard: ClipboardOff, ClockMillis: defaultClockMillis, MaxMediaBytes: DefaultMaxMediaBytes, Enforcement: EnforceRequired}, true
	case "reader":
		return Profile{Name: name, Navigate: NavConfirm, Subresources: SubOrigin, Fetch: FetchOff, Scripts: ScriptsOff, Files: FilesOff, Media: MediaImage, Clipboard: ClipboardCopy, ClockMillis: defaultClockMillis, MaxMediaBytes: DefaultMaxMediaBytes, Enforcement: EnforceBestEffort}, true
	case "browse":
		return Profile{Name: name, Navigate: NavFree, Subresources: SubAny, Fetch: FetchAllowlist, Scripts: ScriptsInline, Files: FilesStartPage, Media: MediaAnimation, Clipboard: ClipboardCopy, ClockMillis: defaultClockMillis, MaxMediaBytes: DefaultMaxMediaBytes, Enforcement: EnforceBestEffort}, true
	}
	return Profile{}, false
}

// Parse reads a profile file: the Profile fields plus an optional "base"
// built-in, which the other fields then override. Without a base everything
// not stated is closed. Unknown fields and values are errors.
func Parse(data []byte) (Profile, error) {
	var head struct {
		Base string `json:"base"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return Profile{}, fmt.Errorf("profile: %w", err)
	}
	var p Profile
	if head.Base != "" {
		var ok bool
		if p, ok = Builtin(head.Base); !ok {
			return Profile{}, fmt.Errorf("profile: unknown base %q", head.Base)
		}
	}
	file := struct {
		Base string `json:"base"`
		*Profile
	}{Profile: &p}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return Profile{}, fmt.Errorf("profile: %w", err)
	}
	if err := p.check(); err != nil {
		return Profile{}, err
	}
	return p, nil
}

func oneOf[T ~string](gate string, v T, allowed ...T) error {
	if v == "" || slices.Contains(allowed, v) {
		return nil
	}
	return fmt.Errorf("profile: %s: unknown value %q", gate, string(v))
}

func (p Profile) check() error {
	if len(p.FrameGrants) > 128 {
		return fmt.Errorf("profile: at most 128 frame grants")
	}
	for _, g := range p.FrameGrants {
		if _, err := g.Normalized(); err != nil {
			return fmt.Errorf("frame grant: %w", err)
		}
	}

	if len(p.ScriptGrants) > 128 {
		return fmt.Errorf("profile: at most 128 script grants")
	}
	for _, g := range p.ScriptGrants {
		if _, err := g.Normalized(); err != nil {
			return err
		}
	}

	if p.SessionStorageBytes < 0 || p.SessionStorageBytes > 8<<20 || p.SessionStorageTotalBytes < 0 || p.SessionStorageTotalBytes > 64<<20 {
		return fmt.Errorf("profile: session-storage-bytes must be 0..8388608 and session-storage-total-bytes 0..67108864 (zero selects defaults)")
	}
	if p.FrameMillis < 0 || p.FrameMillis > 1000 || p.FrameCallbacks < 0 || p.FrameCallbacks > 1024 || p.FrameBudgetMillis < 0 || p.FrameBudgetMillis > 50 {
		return fmt.Errorf("profile: frame-ms must be 0..1000, frame-callbacks 0..1024, frame-budget-ms 0..50 (zero uses defaults)")
	}
	if p.ScriptNavigationLimit < 0 || p.ScriptNavigationLimit > 100 {
		return fmt.Errorf("profile: script-navigation-limit must be 0 (default) or 1..100")
	}
	if p.ClockMillis < 0 || int64(p.ClockMillis) > (1<<63-1)/int64(time.Millisecond) {
		return fmt.Errorf("profile: clock resolution is negative or exceeds the supported duration")
	}
	for _, err := range []error{
		oneOf("script-navigation", p.ScriptNavigation, ScriptNavOff, ScriptNavOrigin, ScriptNavAllowlist, ScriptNavConfirm),
		oneOf("navigate", p.Navigate, NavOff, NavAllowlist, NavConfirm, NavFree),
		oneOf("subresources", p.Subresources, SubOff, SubOrigin, SubAny),
		oneOf("fetch", p.Fetch, FetchOff, FetchOrigin, FetchAllowlist),
		oneOf("script-sources", p.ScriptSources, ScriptSourcesGrants, ScriptSourcesAny),
		oneOf("scripts", p.Scripts, ScriptsOff, ScriptsInline, ScriptsExternal),
		oneOf("session-storage", p.SessionStorage, SessionStorageOff, SessionStorageOn),
		oneOf("local-storage", p.LocalStorage, LocalStorageOff, LocalStorageOn),
		oneOf("persist", p.Persist, PersistOff, PersistFile),
		oneOf("cookies", p.Cookies, CookiesOff, CookiesSession),
		oneOf("frames", p.Frames, FramesOff, FramesOrigin, FramesAllowlist, FramesAny),
		oneOf("frame-dom", p.FrameDOM, "off", "same-origin"),
		oneOf("dynamic-code", p.DynamicCode, DynamicOff, DynamicOn),
		oneOf("files", p.Files, FilesOff, FilesStartPage, FilesTemp),
		oneOf("media", p.Media, MediaOff, MediaImage, MediaAnimation, MediaVideo),
		oneOf("clipboard", p.Clipboard, ClipboardOff, ClipboardCopy),
		oneOf("enforcement", p.Enforcement, EnforceRequired, EnforceBestEffort),
	} {
		if err != nil {
			return err
		}
	}
	return nil
}

// Validate checks values and dependencies; a failure stops the program.
func (p Profile) Validate() error {
	if err := p.check(); err != nil {
		return err
	}
	if p.Media == MediaVideo {
		if !p.NativeCode {
			return fmt.Errorf("profile: media=video requires native-code=on")
		}
		if p.Files != FilesTemp {
			return fmt.Errorf("profile: media=video requires files=temp")
		}
	}
	return nil
}

// ValidateStart checks the URL given at launch against the profile.
func (p Profile) ValidateStart(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("profile: start URL: %w", err)
	}
	switch u.Scheme {
	case "http", "https":
		return nil
	case "file":
		if !p.ReadsStartFile() {
			return fmt.Errorf("profile: a file: start URL requires files=start-page")
		}
		return nil
	}
	return fmt.Errorf("profile: start URL scheme %q", u.Scheme)
}

// WithVideo is the -media video shorthand: a person typing it has asked for
// video, so it also opens the two gates video depends on. Profile files get
// no shorthand.
func (p Profile) WithVideo() Profile {
	p.Media, p.NativeCode, p.Files = MediaVideo, true, FilesTemp
	return p
}

// NavigationOpen reports whether links may navigate at all.
func (p Profile) NavigationOpen() bool {
	return p.Navigate == NavAllowlist || p.Navigate == NavConfirm || p.Navigate == NavFree
}

// ScriptsOn reports whether page script runs.
func (p Profile) ScriptsOn() bool { return p.Scripts == ScriptsInline || p.Scripts == ScriptsExternal }

// AllowsDynamicCode requires both script execution and an explicit dynamic-code grant.
func (p Profile) AllowsDynamicCode() bool { return p.ScriptsOn() && p.DynamicCode == DynamicOn }

// AllowsSubresources reports whether stylesheets and images may load.
func (p Profile) AllowsSubresources() bool {
	return p.Subresources == SubOrigin || p.Subresources == SubAny
}

// ReadsStartFile reports whether the one file: URL given at launch may be read.
func (p Profile) ReadsStartFile() bool { return p.Files == FilesStartPage || p.Files == FilesTemp }

// UsesTemp reports whether the private media directory exists.
func (p Profile) UsesTemp() bool { return p.Files == FilesTemp }

func mediaRank(m Media) int {
	switch m {
	case MediaImage:
		return 1
	case MediaAnimation:
		return 2
	case MediaVideo:
		return 3
	}
	return 0
}

// Allows reports whether the gate position m permits presenting as want.
func (m Media) Allows(want Media) bool { return mediaRank(want) > 0 && mediaRank(m) >= mediaRank(want) }

// Strength is how a gate is held.
type Strength string

const (
	StrengthCompiledOut Strength = "compiled-out" // the code is not in this binary
	StrengthOS          Strength = "os"           // the kernel refuses
	StrengthChecked     Strength = "checked"      // our code refuses
)

// Strengths gives a gate's strength on this machine; unlisted gates are
// checked. It comes from what sandbox.Apply actually achieved.
type Strengths map[string]Strength

func orOff[T ~string](v T) string {
	if v == "" {
		return "off"
	}
	return string(v)
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

// Table prints the profile as plain rows: gate, position, strength, note.
func (p Profile) Table(s Strengths) string {
	name := p.Name
	if name == "" {
		name = "(unnamed)"
	}
	rows := [][3]string{
		{"script-navigation", orOff(p.ScriptNavigation), "Location requests; intersects navigate gate"},
		{"script-navigation-limit", strconv.Itoa(p.ScriptNavBudget()), "automatic navigations per reader-initiated chain"},
		{"navigate", orOff(p.Navigate), "top-level links; off = only the start URL"},
		{"subresources", orOff(p.Subresources), "stylesheets and images inside a page"},
		{"fetch", orOff(p.Fetch), "fetch() from page script"},
		{"hosts", strings.Join(p.Hosts, ","), "fetch/navigation allowlists; does not authorize scripts"},
		{"script-prompts", onOff(p.ScriptPrompts), "ask for one-request or session script grants"},
		{"script-sources", p.ScriptSourceMode(), "external script origins; independent of fetch/navigation"},
		{"script-grants", fmt.Sprint(p.ScriptGrants), "source origin to destination origin; scripts only"},
		{"private-network", onOff(p.PrivateNetwork), "loopback and private addresses"},
		{"scripts", orOff(p.Scripts), "page script"},
		{"devtools", onOff(p.DevTools), "host-only read-only inspection; no remote listener or evaluation"},
		{"diagnostics", onOff(p.Diagnostics), "host-only diagnostic file; may contain page data"},
		{"session-storage", orOff(p.SessionStorage), "origin-isolated memory-only session storage"},
		{"session-storage-bytes", strconv.Itoa(p.StorageBytes()), "per-origin key/value bytes"},
		{"session-storage-total-bytes", strconv.Itoa(p.StorageTotalBytes()), "session-wide key/value bytes"},
		{"local-storage", orOff(p.LocalStorage), "origin-isolated localStorage with the session quotas"},
		{"persist", orOff(p.Persist), "file: cookies with an expiry and localStorage survive restarts in owner-only files opened before lockdown"},
		{"cookies", orOff(p.Cookies), "session cookie store, memory-only unless persist=file keeps cookies with an expiry; cross-origin subrequests excluded"},
		{"frame-dom", orOff(p.FrameDOM), "bounded same-origin DOM bridge; no shared JavaScript globals"},
		{"frames", orOff(p.Frames), "isolated child pages; origin, explicit grants, or any public origin"},
		{"frame-grants", fmt.Sprint(p.FrameGrants), "frame source/destination origins; no script or fetch grants"},
		{"dynamic-code", orOff(p.DynamicCode), "eval and Function-family string compilation"},
		{"frame-ms", strconv.Itoa(p.FrameInterval()), "minimum frame interval"},
		{"frame-callbacks", strconv.Itoa(p.FrameCallbackLimit()), "pending animation callback cap"},
		{"frame-budget-ms", strconv.Itoa(p.FrameBudget()), "shared animation callback execution budget"},
		{"clock", strconv.Itoa(p.ClockMillis) + "ms", "script clock resolution"},
		{"files", orOff(p.Files), "start-page: the file: URL given at launch; temp adds the media directory"},
		{"native-code", onOff(p.NativeCode), "only ever in the ntbrowser-media helper"},
		{"media", orOff(p.Media), "top-level responses; embedded images: subresources"},
		{"max-media-bytes", strconv.FormatInt(p.MaxMediaBytes, 10), "download cap for media"},
		{"clipboard", orOff(p.Clipboard), "selected-text copy"},
		{"environment", "off", "nothing in the sandbox reads the environment"},
		{"process", "off", "no process is started after launch"},
		{"enforcement", string(p.Enforcement), "required: exit if the OS cannot enforce"},
	}
	if p.Media == MediaVideo {
		rows = append(rows, [3]string{"media-helper", "on", "started before lockdown; no network, terminal or environment; reads only its libraries and the media directory"})
	}
	var b strings.Builder
	fmt.Fprintf(&b, "profile: %s\n", name)
	for _, r := range rows {
		st, ok := s[r[0]]
		if !ok {
			st = StrengthChecked
		}
		fmt.Fprintf(&b, "  %-16s %-12s %-13s %s\n", r[0], r[1], st, r[2])
	}
	return b.String()
}

func (p Profile) ScriptNavBudget() int {
	if p.ScriptNavigationLimit > 0 {
		return p.ScriptNavigationLimit
	}
	return 8
}

func (p Profile) FrameInterval() int {
	if p.FrameMillis > 0 {
		return p.FrameMillis
	}
	return 33
}
func (p Profile) FrameCallbackLimit() int {
	if p.FrameCallbacks > 0 {
		return p.FrameCallbacks
	}
	return 256
}
func (p Profile) FrameBudget() int {
	if p.FrameBudgetMillis > 0 {
		return p.FrameBudgetMillis
	}
	return 8
}

func (p Profile) StorageBytes() int {
	if p.SessionStorageBytes > 0 {
		return p.SessionStorageBytes
	}
	return 1 << 20
}
func (p Profile) StorageTotalBytes() int {
	if p.SessionStorageTotalBytes > 0 {
		return p.SessionStorageTotalBytes
	}
	return 8 << 20
}

// ScriptSourceMode reports the restrictive default explicitly.
func (p Profile) ScriptSourceMode() string {
	if p.ScriptSources == ScriptSourcesAny {
		return "any"
	}
	return "grants"
}
