// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

// Package browsercmd shares startup and sandbox re-exec between ntbrowser and glynx.
//
//	ntbrowser https://example.com/page.html
//	ntbrowser -allow .example.com -nav confirm -subresources origin -width 800 file:///path/to/page.html
package browsercmd

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
	"nimbleterminal.dev/ntbrowser/devtools"
	"nimbleterminal.dev/ntbrowser/diagnostics"
	"nimbleterminal.dev/ntbrowser/dom"
	"nimbleterminal.dev/ntbrowser/jsvm"
	"nimbleterminal.dev/ntbrowser/loop"
	"nimbleterminal.dev/ntbrowser/profile"
	"nimbleterminal.dev/ntbrowser/sandbox"
	"nimbleterminal.dev/ntbrowser/subres"
	"nimbleterminal.dev/nthtml/document"
)

// config is what the command line decides. Profile is the effective profile:
// the chosen one with the gate flags applied, validated and then frozen.
type config struct {
	Diagnostics                                        string
	StateDir                                           string // persist=file directory (--state-dir)
	LocalStoreFile, CookieFile                         string // resolved state file paths, set when opened before lockdown
	Safe                                               bool   // --safe: persist=off, so nothing new reaches the disk this run
	Profile                                            profile.Profile
	Start                                              string
	Width                                              int
	ColorScheme                                        string // --color-scheme auto|light|dark: auto follows the terminal background
	Slice                                              time.Duration
	HeapBudgetMiB                                      uint64
	StylesheetBytes, StylesheetTotalBytes, Stylesheets int
	ImageBytes                                         int
	CSSRules                                           int
	Seed                                               uint64
	ShowProfile                                        bool
	Chunked                                            bool
}

func parseArgs(args []string) (config, error) { return parseCommandArgs("ntbrowser", args) }

// maxImageBytes is the --image-bytes ceiling (the subresource loader's own).
const maxImageBytes = subres.MaxImageBytesLimit

func parseCommandArgs(command string, args []string) (config, error) {
	var c config
	defaultProfile := "browse"
	defaultHeapMiB := uint64(256)
	sheetBytes, sheetTotal, sheetCount := 256<<10, 0, 16
	cssRules := 4096
	imageBytes := 1 << 20
	if command == "glynx" {
		defaultProfile = "glynx"
		defaultHeapMiB = 512
		sheetBytes, sheetTotal, sheetCount = 1<<20, 4<<20, 64
		cssRules = 32768
		imageBytes = 8 << 20 // README demo GIFs; decoded-size caps are unchanged
	}
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.StringVarP(&c.Diagnostics, "diagnostics", "d", "", "write bounded JSONL diagnostics to a new owner-only file")
	debugTools := fs.Bool("devtools", false, "enable host-only read-only devtools (F12); no remote listener")
	name := fs.String("profile", defaultProfile, "built-in profile: locked, reader, browse, glynx")
	file := fs.String("profile-file", "", "JSON profile file (overrides --profile)")
	fs.BoolVar(&c.ShowProfile, "show-profile", false, "print the effective profile and exit")
	frameDOM := fs.String("frame-dom", "", "shared frame DOM: off, same-origin (default from profile)")
	frames := fs.String("frames", "", "isolated iframes: off, origin, allowlist, any (default from profile)")
	frameGrants := fs.StringArray("allow-frame", nil, "frame grant SOURCE_ORIGIN=DESTINATION_ORIGIN (repeatable)")
	scriptGrants := fs.StringArray("allow-script", nil, "script grant SOURCE_ORIGIN=DESTINATION_ORIGIN (repeatable)")
	scriptPrompts := fs.Bool("script-prompts", false, "ask before admitting an ungranted cross-origin script")
	allow := fs.String("allow", "", "comma-separated fetch/navigation allowlist hosts (leading dot = subdomains; scripts use --allow-script)")
	private := fs.Bool("allow-private", false, "permit loopback/private addresses (development)")
	external := fs.Bool("external-scripts", false, "fetch <script src> through source-scoped script grants (same as --scripts external)")
	nav := fs.String("nav", "", "link navigation: off, free, allowlist, confirm")
	subres := fs.String("subresources", "", "stylesheets and images: off, origin, any")
	fetch := fs.String("fetch", "", "fetch() from page script: off, origin, allowlist")
	scriptSources := fs.String("script-sources", "", "external script origins: grants, any (default from profile)")
	scripts := fs.String("scripts", "", "page script: off, inline, external")
	storageMode := fs.String("session-storage", "", "sessionStorage: off, on (default from profile; memory only)")
	storageBytes := fs.Int("session-storage-bytes", 0, "per-origin storage byte quota (default 1 MiB; max 8 MiB)")
	storageTotal := fs.Int("session-storage-total-bytes", 0, "session storage byte quota (default 8 MiB; max 64 MiB)")
	localMode := fs.String("local-storage", "", "localStorage: off, on (default from profile; same quotas as session storage)")
	persistMode := fs.String("persist", "", "keep cookies with an expiry and localStorage across runs: off, file (default from profile)")
	fs.StringVar(&c.StateDir, "state-dir", "", "directory for persist=file (default $XDG_DATA_HOME/<command>); owner-only files, created 0700")
	fs.BoolVar(&c.Safe, "safe", false, "memory-only run: same as --persist=off, nothing new reaches the disk")
	frameMS := fs.Int("frame-ms", 0, "minimum frame interval in ms (default 33; max 1000)")
	frameCallbacks := fs.Int("frame-callbacks", 0, "pending animation callbacks (default 256; max 1024)")
	frameBudget := fs.Int("frame-budget-ms", 0, "shared animation callback budget in ms (default 8; max 50)")
	scriptNav := fs.String("script-navigation", "", "Location navigation: off, origin, allowlist, confirm")
	scriptNavLimit := fs.Int("script-navigation-limit", 0, "automatic navigations per reader-initiated chain (default 8; max 100)")
	cookieMode := fs.String("cookies", "", "cookies: off, session (default from profile; memory only)")
	dynamic := fs.String("dynamic-code", "", "eval and Function-family compilation: off, on (default from profile)")
	clipboard := fs.String("clipboard", "", "selected-text copy: off, copy")
	media := fs.String("media", "", "most a response may be shown as: off, image, animation, video (video also opens native-code and files=temp)")
	maxMedia := fs.Int64("max-media-bytes", 0, "download cap for media")
	fs.BoolVar(&c.Chunked, "chunked", false, "read long pages in bounded semantic chunks (reduced CSS support)")
	fs.IntVar(&c.Width, "width", 800, "document pixel width")
	fs.StringVar(&c.ColorScheme, "color-scheme", "auto", "page color scheme for prefers-color-scheme and light-dark(): auto (follow the terminal background), light, dark")
	fs.DurationVar(&c.Slice, "slice", 50*time.Millisecond, "JS time slice per callback")
	fs.Uint64Var(&c.HeapBudgetMiB, "heap-budget-mib", defaultHeapMiB, "process Go heap budget after GC in MiB (64..2048; includes rendering)")
	fs.IntVar(&c.CSSRules, "css-rules", cssRules, "maximum CSS rules (256..32768; matching-work budget still applies)")
	fs.IntVar(&c.StylesheetBytes, "stylesheet-bytes", sheetBytes, "maximum bytes per stylesheet (1024..1048576)")
	fs.IntVar(&c.StylesheetTotalBytes, "stylesheet-total-bytes", sheetTotal, "separate host stylesheet allowance (0 shares DOM budget; max 4194304)")
	fs.IntVar(&c.Stylesheets, "stylesheets", sheetCount, "maximum stylesheets per page including imports (1..64)")
	fs.IntVar(&c.ImageBytes, "image-bytes", imageBytes, "maximum encoded bytes per page image (1024..16777216)")
	fs.Uint64Var(&c.Seed, "seed", 0, "seed for Math.random and crypto, for reproducible runs (0 = a fresh unpredictable generator per page); the clock is never fixed, timers depend on it")
	if err := fs.Parse(legacyLongFlags(fs, args)); err != nil {
		return c, err
	}
	switch c.ColorScheme {
	case "auto", "light", "dark":
	default:
		return c, fmt.Errorf("--color-scheme must be auto, light or dark")
	}
	if c.HeapBudgetMiB < 64 || c.HeapBudgetMiB > 2048 {
		return c, fmt.Errorf("--heap-budget-mib must be between 64 and 2048")
	}
	if c.CSSRules < 256 || c.CSSRules > document.MaxLimits.Rules {
		return c, fmt.Errorf("--css-rules must be between 256 and %d", document.MaxLimits.Rules)
	}
	if c.StylesheetBytes < 1024 || c.StylesheetBytes > 1<<20 || c.StylesheetTotalBytes < 0 || c.StylesheetTotalBytes > dom.MaxExternalStyleBytes || c.Stylesheets < 1 || c.Stylesheets > 64 {
		return c, fmt.Errorf("invalid stylesheet budget: bytes 1024..1048576, total 0..4194304, count 1..64")
	}
	if c.ImageBytes < 1024 || c.ImageBytes > maxImageBytes {
		return c, fmt.Errorf("--image-bytes must be between 1024 and %d", maxImageBytes)
	}
	p, ok := profile.Builtin(*name)
	if !ok {
		return c, fmt.Errorf("unknown profile %q (locked|reader|browse|glynx)", *name)
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
	if fs.Changed("devtools") {
		p.DevTools = *debugTools
	}
	if *scriptSources != "" {
		p.ScriptSources = profile.ScriptSources(*scriptSources)
	}
	if c.Diagnostics != "" {
		p.Diagnostics = true
	}
	if p.Diagnostics && c.Diagnostics == "" && !c.ShowProfile {
		return c, errors.New("diagnostics requires --diagnostics FILE")
	}
	chosen.Hosts = slices.Clone(p.Hosts)
	chosen.ScriptGrants = slices.Clone(p.ScriptGrants)
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
	if *frameDOM != "" {
		p.FrameDOM = *frameDOM
	}
	if *frames != "" {
		p.Frames = profile.Frames(*frames)
	}
	for _, raw := range *frameGrants {
		g, err := profile.ParseScriptGrant(raw)
		if err != nil {
			return c, err
		}
		p.FrameGrants = append(p.FrameGrants, g)
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
	if *localMode != "" {
		p.LocalStorage = profile.LocalStorage(*localMode)
	}
	if *persistMode != "" {
		p.Persist = profile.Persist(*persistMode)
	}
	if c.Safe {
		p.Persist = profile.PersistOff
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
	if fs.Changed("script-prompts") {
		p.ScriptPrompts = *scriptPrompts
	}
	for _, value := range *scriptGrants {
		grant, err := profile.ParseScriptGrant(value)
		if err != nil {
			return c, err
		}
		p.ScriptGrants = append(p.ScriptGrants, grant)
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
	if p.Persist == profile.PersistFile && c.StateDir == "" {
		dir, err := defaultStateDir(command)
		if err != nil {
			return c, fmt.Errorf("persist=file needs --state-dir: %v", err)
		}
		c.StateDir = dir
	}
	if c.ShowProfile {
		return c, nil
	}
	if fs.NArg() > 1 {
		return c, fmt.Errorf("usage: %s [flags] [url|file://path]", command)
	}
	if fs.NArg() == 0 {
		return c, nil
	}
	c.Start = fs.Arg(0)
	if command == "glynx" && !strings.HasPrefix(c.Start, "file:") {
		var err error
		c.Start, err = app.BrowsingURL(c.Start)
		if err != nil {
			return c, err
		}
	}
	return c, p.ValidateStart(c.Start)
}

// state is what the run decided before lockdown. The macOS re-exec carries it
// across as data: a profile is read once, never again inside the sandbox.
type state struct {
	DiagnosticFD int
	StorageFD    int // persist=file localStorage descriptor, opened before lockdown
	CookieFD     int // persist=file cookie descriptor, opened before lockdown
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

func runMain() int { return Run("ntbrowser") }

// Run starts one browser session using the named executable defaults.
func Run(command string) int {
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
		st.Config, err = parseCommandArgs(command, os.Args[1:])
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
		fmt.Printf("strengths are what this machine should achieve; a running %s says if it did not\n", command)
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
	storeFile, cookieFile, err := stateFiles(&st, sandboxed)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if storeFile != nil {
		defer storeFile.Close()
	}
	if cookieFile != nil {
		defer cookieFile.Close()
	}
	strengths, err := lockDown(st, sandboxed)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var browserLoop *loop.Loop
	var inspector *devtools.Session
	var observer func(string, any)
	if c.Profile.DevTools {
		inspector = devtools.New(true, func(ctx context.Context, req devtools.Request) (devtools.Response, error) {
			return browserLoop.Inspect(ctx, req)
		})
		defer inspector.Close()
		observer = inspector.Observe
	}
	var trace *diagnostics.Recorder
	if diagnostic != nil || inspector != nil {
		// A typed nil *os.File must not become a non-nil io.Writer.
		var writer io.Writer
		if diagnostic != nil {
			writer = diagnostic
		}
		trace = diagnostics.NewObserved(writer, observer)
		defer func() {
			if err := trace.Close(); err != nil {
				fmt.Fprintln(os.Stderr, "diagnostics:", err)
			}
		}()
		trace.Record("session", map[string]any{"profile": c.Profile, "strengths": strengths, "heap_budget_bytes": c.HeapBudgetMiB << 20, "stylesheet_bytes": c.StylesheetBytes, "stylesheet_total_bytes": c.StylesheetTotalBytes, "stylesheets": c.Stylesheets, "css_rules": c.CSSRules, "image_bytes": c.ImageBytes, "persist": string(c.Profile.Persist)})
	}
	// Process-wide soft heap cap: Goja has no per-runtime memory limit, so
	// the GC is told to work harder past this point and dom/nthtml limits
	// keep individual allocations bounded.
	debug.SetMemoryLimit(int64(max(uint64(512), c.HeapBudgetMiB)) << 20)

	if c.Profile.ReadsStartFile() {
		// The only os.Open in the program; installed into app, never jsvm.
		app.SetLocalReader(func(path string) (io.ReadCloser, error) { return os.Open(path) })
	}

	var program *tea.Program
	sink := sinkFunc(func(msg tea.Msg) { program.Send(msg) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	script := jsvm.Config{Slice: c.Slice, HeapBudget: c.HeapBudgetMiB << 20}
	if c.Seed != 0 {
		script.Rand = jsvm.RandomSource(c.Seed) // one replayable sequence for every page and frame
	}
	mode, err := subres.ParseMode(string(c.Profile.Subresources))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	subresCfg := subres.Config{Mode: mode, MaxSheetBytes: c.StylesheetBytes, MaxStylesheets: c.Stylesheets, MaxImageBytes: c.ImageBytes}
	cookieStore, cookieSaver := openCookieStore(c.Profile, cookieFile)
	defer cookieSaver.Close() // the final snapshot, after the program has stopped
	policy := app.PolicyFor(c.Profile)
	policy.Cookies = cookieStore
	policy.Trace = trace
	localStore, localSink := openLocalStore(c.Profile, storeFile)
	l := loop.New(ctx, loop.Config{RenderLimit: document.Limits{Rules: c.CSSRules}, DOMLimits: dom.Limits{ExternalStyleBytes: c.StylesheetTotalBytes}, Trace: trace, Chunked: c.Chunked, Profile: c.Profile, Width: c.Width, ColorScheme: startScheme(c.ColorScheme), Script: script, Subresources: subresCfg, SubresourceTransport: app.SubresourceTransport(policy, subresCfg), LocalStorage: localStore, LocalStorageSink: localSink}, sink)
	browserLoop = l
	go l.Run()

	m := app.New(l, c.Profile, c.Start)
	if command == "glynx" {
		m.SetGlynx()
	}
	m.SetColorSchemeAuto(c.ColorScheme == "auto")
	m.SetDevTools(inspector)
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
	// Stop the loop and wait for it: its exit flush writes the last
	// localStorage snapshot, which must land before the process ends.
	cancel()
	select {
	case <-l.Done():
	case <-time.After(2 * time.Second):
	}
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
		if err := sandbox.Reexec(c.Profile, start, st.TempDir, []string{arg}, c.Diagnostics, c.LocalStoreFile, c.CookieFile); err != nil {
			return nil, err
		}
	}
	return sandbox.Apply(c.Profile, start, st.TempDir, sandboxed)
}

type sinkFunc func(tea.Msg)

func (f sinkFunc) Send(msg tea.Msg) { f(msg) }

// startScheme is the scheme the first layout uses: an explicit choice, or
// light until the terminal reports a dark background in auto mode.
func startScheme(flag string) string {
	if flag == "dark" {
		return "dark"
	}
	return "light"
}
