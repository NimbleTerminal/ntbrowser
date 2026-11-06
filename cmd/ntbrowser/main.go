// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

// Command ntbrowser runs a sandboxed page in the terminal.
//
//	ntbrowser https://example.com/page.html
//	ntbrowser -allow .example.com -nav confirm -subresources origin -width 800 file:///path/to/page.html
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	flag "github.com/spf13/pflag"

	"nimbleterminal.dev/ntbrowser/app"
	"nimbleterminal.dev/ntbrowser/cookies"
	"nimbleterminal.dev/ntbrowser/diagnostics"
	"nimbleterminal.dev/ntbrowser/jsvm"
	"nimbleterminal.dev/ntbrowser/loop"
	"nimbleterminal.dev/ntbrowser/profile"
	"nimbleterminal.dev/ntbrowser/sandbox"
	"nimbleterminal.dev/ntbrowser/subres"
)

// config is what the command line decides. Profile is the effective profile:
// the chosen one with the gate flags applied, validated and then frozen.
type config struct {
	Diagnostics string
	Profile     profile.Profile
	Start       string
	Width       int
	Slice       time.Duration
	Seed        uint64
	ShowProfile bool
	Chunked     bool
}

func parseArgs(args []string) (config, error) {
	var c config
	fs := flag.NewFlagSet("ntbrowser", flag.ContinueOnError)
	fs.StringVar(&c.Diagnostics, "diagnostics", "", "write bounded JSONL diagnostics to a new owner-only file")
	name := fs.String("profile", "browse", "built-in profile: locked, reader, browse")
	file := fs.String("profile-file", "", "JSON profile file (overrides --profile)")
	fs.BoolVar(&c.ShowProfile, "show-profile", false, "print the effective profile and exit")
	allow := fs.String("allow", "", "comma-separated hosts for allowlist gates (leading dot = subdomains)")
	private := fs.Bool("allow-private", false, "permit loopback/private addresses (development)")
	external := fs.Bool("external-scripts", false, "fetch <script src> through the allowlist (same as --scripts external)")
	nav := fs.String("nav", "", "link navigation: off, free, allowlist, confirm")
	subres := fs.String("subresources", "", "stylesheets and images: off, origin, any")
	fetch := fs.String("fetch", "", "fetch() from page script: off, origin, allowlist")
	scripts := fs.String("scripts", "", "page script: off, inline, external")
	storageMode := fs.String("session-storage", "", "sessionStorage: off, on (default off; memory only)")
	storageBytes := fs.Int("session-storage-bytes", 0, "per-origin storage byte quota (default 1 MiB; max 8 MiB)")
	storageTotal := fs.Int("session-storage-total-bytes", 0, "session storage byte quota (default 8 MiB; max 64 MiB)")
	frameMS := fs.Int("frame-ms", 0, "minimum frame interval in ms (default 33; max 1000)")
	frameCallbacks := fs.Int("frame-callbacks", 0, "pending animation callbacks (default 256; max 1024)")
	frameBudget := fs.Int("frame-budget-ms", 0, "shared animation callback budget in ms (default 8; max 50)")
	scriptNav := fs.String("script-navigation", "", "Location navigation: off, origin, allowlist, confirm")
	scriptNavLimit := fs.Int("script-navigation-limit", 0, "automatic navigations per reader-initiated chain (default 8; max 100)")
	cookieMode := fs.String("cookies", "", "cookies: off, session (default off; memory only)")
	dynamic := fs.String("dynamic-code", "", "eval and Function-family compilation: off, on (default off)")
	clipboard := fs.String("clipboard", "", "selected-text copy: off, copy")
	media := fs.String("media", "", "most a response may be shown as: off, image, animation, video (video also opens native-code and files=temp)")
	maxMedia := fs.Int64("max-media-bytes", 0, "download cap for media")
	fs.BoolVar(&c.Chunked, "chunked", false, "read long pages in bounded semantic chunks (reduced CSS support)")
	fs.IntVar(&c.Width, "width", 800, "document pixel width")
	fs.DurationVar(&c.Slice, "slice", 50*time.Millisecond, "JS time slice per callback")
	fs.Uint64Var(&c.Seed, "seed", 0, "PRNG seed for Math.random, for reproducible runs (0 = time-based); the clock is never fixed, timers depend on it")
	if err := fs.Parse(legacyLongFlags(fs, args)); err != nil {
		return c, err
	}
	p, ok := profile.Builtin(*name)
	if !ok {
		return c, fmt.Errorf("unknown profile %q (locked|reader|browse)", *name)
	}
	if *file != "" {
		data, err := os.ReadFile(*file)
		if err != nil {
			return c, err
		}
		if p, err = profile.Parse(data); err != nil {
			return c, err
		}
	}
	chosen := p
	if c.Diagnostics != "" {
		p.Diagnostics = true
	}
	if p.Diagnostics && c.Diagnostics == "" && !c.ShowProfile {
		return c, errors.New("diagnostics requires --diagnostics FILE")
	}
	chosen.Hosts = slices.Clone(p.Hosts)
	if *nav != "" {
		p.Navigate = profile.Navigate(*nav)
	}
	if *subres != "" {
		p.Subresources = profile.Subresources(*subres)
	}
	if *fetch != "" {
		p.Fetch = profile.Fetch(*fetch)
	}
	if *external {
		p.Scripts = profile.ScriptsExternal
	}
	if *scripts != "" {
		p.Scripts = profile.Scripts(*scripts)
	}
	if *storageMode != "" {
		p.SessionStorage = profile.SessionStorage(*storageMode)
	}
	if fs.Changed("session-storage-bytes") {
		p.SessionStorageBytes = *storageBytes
	}
	if fs.Changed("session-storage-total-bytes") {
		p.SessionStorageTotalBytes = *storageTotal
	}
	if fs.Changed("frame-ms") {
		p.FrameMillis = *frameMS
	}
	if fs.Changed("frame-callbacks") {
		p.FrameCallbacks = *frameCallbacks
	}
	if fs.Changed("frame-budget-ms") {
		p.FrameBudgetMillis = *frameBudget
	}
	if *scriptNav != "" {
		p.ScriptNavigation = profile.ScriptNavigation(*scriptNav)
	}
	if fs.Changed("script-navigation-limit") {
		p.ScriptNavigationLimit = *scriptNavLimit
	}
	if *cookieMode != "" {
		p.Cookies = profile.Cookies(*cookieMode)
	}
	if *dynamic != "" {
		p.DynamicCode = profile.DynamicCode(*dynamic)
	}
	if *clipboard != "" {
		p.Clipboard = profile.Clipboard(*clipboard)
	}
	if *allow != "" {
		p.Hosts = strings.Split(*allow, ",")
	}
	if *private {
		p.PrivateNetwork = true
	}
	if *maxMedia > 0 {
		p.MaxMediaBytes = *maxMedia
	}
	switch *media {
	case "":
	case "video":
		p = p.WithVideo()
	default:
		p.Media = profile.Media(*media)
	}
	if err := p.Validate(); err != nil {
		return c, err
	}
	if !reflect.DeepEqual(p, chosen) {
		p.Name += "+flags" // the name must not outlive what it promised
	}
	c.Profile = p
	if c.ShowProfile {
		return c, nil
	}
	if fs.NArg() != 1 {
		return c, errors.New("usage: ntbrowser [flags] <url|file://path>")
	}
	c.Start = fs.Arg(0)
	return c, p.ValidateStart(c.Start)
}

// state is what the run decided before lockdown. The macOS re-exec carries it
// across as data: a profile is read once, never again inside the sandbox.
type state struct {
	DiagnosticFD int
	Config       config
	TempDir      string
	HelperFD     int
	HelperPID    int
}

func encodeState(st state) (string, error) {
	data, err := json.Marshal(st)
	if err != nil {
		return "", err
	}
	return sandbox.SandboxedArg + "=" + base64.StdEncoding.EncodeToString(data), nil
}

func decodeState(args []string) (state, bool, error) {
	var st state
	if len(args) != 1 {
		return st, false, nil
	}
	v, ok := strings.CutPrefix(args[0], sandbox.SandboxedArg+"=")
	if !ok {
		return st, false, nil
	}
	data, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return st, true, err
	}
	return st, true, json.Unmarshal(data, &st)
}

func main() { os.Exit(runMain()) }

func runMain() int {
	if len(os.Args) == 3 && os.Args[1] == sandbox.StageArg {
		// Linux: this static binary locks down, then becomes the helper.
		helper, err := sandbox.HelperPath()
		if err == nil {
			err = sandbox.MediaStage(helper, os.Args[2])
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	st, sandboxed, err := decodeState(os.Args[1:])
	if err == nil && !sandboxed {
		st.HelperFD = -1
		st.Config, err = parseArgs(os.Args[1:])
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	c := st.Config
	if c.ShowProfile {
		fmt.Print(c.Profile.Table(sandbox.Probe()))
		fmt.Println("strengths are what this machine should achieve; a running ntbrowser says if it did not")
		return 0
	}
	if !sandboxed && c.Profile.UsesTemp() {
		if st.TempDir, err = mediaTemp(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	if st.TempDir != "" {
		defer os.Remove(st.TempDir)
	}
	conn, closeMedia, err := startMedia(&st, sandboxed)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer closeMedia()
	diagnostic, err := diagnosticFile(&st, sandboxed)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if diagnostic != nil {
		defer diagnostic.Close()
	}
	strengths, err := lockDown(st, sandboxed)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var trace *diagnostics.Recorder
	if diagnostic != nil {
		trace = diagnostics.New(diagnostic)
		defer func() {
			if err := trace.Close(); err != nil {
				fmt.Fprintln(os.Stderr, "diagnostics:", err)
			}
		}()
		trace.Record("session", map[string]any{"profile": c.Profile, "strengths": strengths})
	}
	// Process-wide soft heap cap: Goja has no per-runtime memory limit, so
	// the GC is told to work harder past this point and dom/nthtml limits
	// keep individual allocations bounded.
	debug.SetMemoryLimit(512 << 20)

	if c.Profile.ReadsStartFile() {
		// The only os.Open in the program; installed into app, never jsvm.
		app.SetLocalReader(func(path string) (io.ReadCloser, error) { return os.Open(path) })
	}

	var program *tea.Program
	sink := sinkFunc(func(msg tea.Msg) { program.Send(msg) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	script := jsvm.Config{Slice: c.Slice}
	if c.Seed != 0 {
		script.Rand = seededRand(c.Seed)
	}
	mode, err := subres.ParseMode(string(c.Profile.Subresources))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	subresCfg := subres.Config{Mode: mode}
	var cookieStore *cookies.Store
	if c.Profile.Cookies == profile.CookiesSession {
		cookieStore = cookies.New()
	}
	policy := app.PolicyFor(c.Profile)
	policy.Cookies = cookieStore
	policy.Trace = trace
	l := loop.New(ctx, loop.Config{Trace: trace, Chunked: c.Chunked, Profile: c.Profile, Width: c.Width, Script: script, Subresources: subresCfg, SubresourceTransport: app.SubresourceTransport(policy, subresCfg)}, sink)
	go l.Run()

	m := app.New(l, c.Profile, c.Start)
	m.SetDiagnostics(trace)
	m.SetCookies(cookieStore)
	defer m.Cleanup()
	program = tea.NewProgram(m)
	m.SetEnforced(strengths["files"] == profile.StrengthOS)
	if conn != nil {
		bridge, err := app.NewMediaBridge(conn, program.Send, closeMedia)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		m.SetMedia(bridge, st.TempDir)
	}
	_, err = program.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// mediaTemp creates the private media directory. Its path is resolved because
// sandbox policies match real paths, and macOS's temp area is behind a symlink.
func mediaTemp() (string, error) {
	dir, err := os.MkdirTemp("", "ntbrowser-media-")
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(dir)
}

// needsHelper is true only for a profile that opened video.
func needsHelper(p profile.Profile) bool { return p.Media == profile.MediaVideo }

// startMedia starts the media helper before lockdown, when the profile
// allows video; afterwards the main process cannot start anything. The macOS
// re-exec comes back with the socket's descriptor instead.
func startMedia(st *state, sandboxed bool) (io.ReadWriteCloser, func(), error) {
	c := st.Config
	if !needsHelper(c.Profile) {
		return nil, func() {}, nil
	}
	if sandboxed {
		// FileConn restores nonblocking, deadline-capable I/O after re-exec.
		file := os.NewFile(uintptr(st.HelperFD), "media-helper")
		conn, err := net.FileConn(file)
		file.Close()
		if err != nil {
			sandbox.KillHelper(st.HelperPID)
			return nil, nil, err
		}
		return conn, sync.OnceFunc(func() {
			conn.Close()
			sandbox.KillHelper(st.HelperPID)
			if p, err := os.FindProcess(st.HelperPID); st.HelperPID > 0 && err == nil {
				_, _ = p.Wait()
			}
		}), nil
	}
	path, err := sandbox.HelperPath()
	if err != nil {
		return nil, nil, err
	}
	h, err := sandbox.StartHelper(c.Profile, path, st.TempDir)
	if err != nil {
		return nil, nil, err
	}
	st.HelperPID = h.PID()
	if runtime.GOOS == "darwin" {
		if st.HelperFD, err = h.InheritableFD(); err != nil {
			h.Close()
			return nil, nil, err
		}
	}
	var inherited *os.File
	if runtime.GOOS == "darwin" {
		inherited = os.NewFile(uintptr(st.HelperFD), "media-helper-inherited")
	}
	return h.Conn, sync.OnceFunc(func() {
		if inherited != nil {
			inherited.Close()
		}
		h.Close()
	}), nil
}

// startPath is the real path of a file: start URL, "" otherwise. Sandbox
// policies match real paths.
func startPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(u.Path); err == nil {
		return real
	}
	return u.Path
}

// lockDown applies OS enforcement before any page content is touched. On
// macOS the first run replaces itself here and comes back sandboxed.
func lockDown(st state, sandboxed bool) (profile.Strengths, error) {
	c := st.Config
	start := startPath(c.Start)
	if !sandboxed {
		arg, err := encodeState(st)
		if err != nil {
			return nil, err
		}
		if err := sandbox.Reexec(c.Profile, start, st.TempDir, []string{arg}, c.Diagnostics); err != nil {
			return nil, err
		}
	}
	return sandbox.Apply(c.Profile, start, st.TempDir, sandboxed)
}

type sinkFunc func(tea.Msg)

func (f sinkFunc) Send(msg tea.Msg) { f(msg) }

// seededRand is the same xorshift64* generator jsvm uses by default, seeded
// explicitly so a run can be replayed. A zero seed would never leave zero.
func seededRand(seed uint64) func() float64 {
	state := seed
	if state == 0 {
		state = 0x9E3779B97F4A7C15
	}
	return func() float64 {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		return float64(state>>11) / float64(1<<53)
	}
}
