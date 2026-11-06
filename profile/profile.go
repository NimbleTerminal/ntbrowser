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
	Navigate     string
	Subresources string
	Fetch        string
	Scripts      string
	DynamicCode  string
	Files        string
	Media        string
	Clipboard    string
	Enforcement  string
)

const (
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

	DynamicOff DynamicCode = "off"
	DynamicOn  DynamicCode = "on"

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
	Name           string       `json:"name,omitempty"`
	Navigate       Navigate     `json:"navigate,omitempty"`
	Subresources   Subresources `json:"subresources,omitempty"`
	Fetch          Fetch        `json:"fetch,omitempty"`
	Hosts          []string     `json:"hosts,omitempty"`
	PrivateNetwork bool         `json:"private-network,omitempty"`
	Scripts        Scripts      `json:"scripts,omitempty"`
	DynamicCode    DynamicCode  `json:"dynamic-code,omitempty"`
	ClockMillis    int          `json:"clock,omitempty"`
	Files          Files        `json:"files,omitempty"`
	NativeCode     bool         `json:"native-code,omitempty"`
	Media          Media        `json:"media,omitempty"`
	MaxMediaBytes  int64        `json:"max-media-bytes,omitempty"`
	Clipboard      Clipboard    `json:"clipboard,omitempty"`
	Enforcement    Enforcement  `json:"enforcement,omitempty"`
}

// Builtin returns a built-in profile: locked, reader or browse.
func Builtin(name string) (Profile, bool) {
	switch name {
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
	if p.ClockMillis < 0 || int64(p.ClockMillis) > (1<<63-1)/int64(time.Millisecond) {
		return fmt.Errorf("profile: clock resolution is negative or exceeds the supported duration")
	}
	for _, err := range []error{
		oneOf("navigate", p.Navigate, NavOff, NavAllowlist, NavConfirm, NavFree),
		oneOf("subresources", p.Subresources, SubOff, SubOrigin, SubAny),
		oneOf("fetch", p.Fetch, FetchOff, FetchOrigin, FetchAllowlist),
		oneOf("scripts", p.Scripts, ScriptsOff, ScriptsInline, ScriptsExternal),
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
		{"navigate", orOff(p.Navigate), "top-level links; off = only the start URL"},
		{"subresources", orOff(p.Subresources), "stylesheets and images inside a page"},
		{"fetch", orOff(p.Fetch), "fetch() from page script"},
		{"hosts", strings.Join(p.Hosts, ","), "what allowlist means"},
		{"private-network", onOff(p.PrivateNetwork), "loopback and private addresses"},
		{"scripts", orOff(p.Scripts), "page script"},
		{"dynamic-code", orOff(p.DynamicCode), "eval and Function-family string compilation"},
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
