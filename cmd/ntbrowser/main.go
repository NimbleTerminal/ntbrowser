// Command ntbrowser runs a sandboxed page in the terminal.
//
//	ntbrowser https://example.com/page.html
//	ntbrowser -allow .example.com -nav confirm -subresources origin -width 800 file:///path/to/page.html
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"nimbleterminal.dev/ntbrowser/app"
	"nimbleterminal.dev/ntbrowser/jsvm"
	"nimbleterminal.dev/ntbrowser/loop"
	"nimbleterminal.dev/ntbrowser/profile"
	"nimbleterminal.dev/ntbrowser/sandbox"
	"nimbleterminal.dev/ntbrowser/subres"
)

// config is what the command line decides. Profile is the effective profile:
// the chosen one with the gate flags applied, validated and then frozen.
type config struct {
	Profile     profile.Profile
	Start       string
	Width       int
	Slice       time.Duration
	Seed        uint64
	ShowProfile bool
}

func parseArgs(args []string) (config, error) {
	var c config
	fs := flag.NewFlagSet("ntbrowser", flag.ContinueOnError)
	name := fs.String("profile", "browse", "built-in profile: locked, reader, browse")
	file := fs.String("profile-file", "", "JSON profile file (overrides -profile)")
	fs.BoolVar(&c.ShowProfile, "show-profile", false, "print the effective profile and exit")
	allow := fs.String("allow", "", "comma-separated hosts for allowlist gates (leading dot = subdomains)")
	private := fs.Bool("allow-private", false, "permit loopback/private addresses (development)")
	external := fs.Bool("external-scripts", false, "fetch <script src> through the allowlist (same as -scripts external)")
	nav := fs.String("nav", "", "link navigation: off, free, allowlist, confirm")
	subres := fs.String("subresources", "", "stylesheets and images: off, origin, any")
	fetch := fs.String("fetch", "", "fetch() from page script: off, origin, allowlist")
	scripts := fs.String("scripts", "", "page script: off, inline, external")
	clipboard := fs.String("clipboard", "", "selected-text copy: off, copy")
	media := fs.String("media", "", "most a response may be shown as: off, image, animation, video (video also opens native-code and files=temp)")
	maxMedia := fs.Int64("max-media-bytes", 0, "download cap for media")
	fs.IntVar(&c.Width, "width", 800, "document pixel width")
	fs.DurationVar(&c.Slice, "slice", 50*time.Millisecond, "JS time slice per callback")
	fs.Uint64Var(&c.Seed, "seed", 0, "PRNG seed for Math.random, for reproducible runs (0 = time-based); the clock is never fixed, timers depend on it")
	if err := fs.Parse(args); err != nil {
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

func main() {
	if len(os.Args) == 3 && os.Args[1] == sandbox.StageArg {
		// Linux: this static binary locks down, then becomes the helper.
		helper, err := sandbox.HelperPath()
		if err == nil {
			err = sandbox.MediaStage(helper, os.Args[2])
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	args, tempDir, helperFD, sandboxed := sandboxArgs(os.Args[1:])
	c, err := parseArgs(args)
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(2)
	}
	if c.ShowProfile {
		fmt.Print(c.Profile.Table(sandbox.Probe()))
		return
	}
	if !sandboxed && c.Profile.UsesTemp() {
		if tempDir, err = mediaTemp(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	conn, err := startMedia(c, tempDir, helperFD, sandboxed, &args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	strengths, err := lockDown(c, tempDir, args, sandboxed)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = strengths // reported by -show-profile (sandbox.Probe)
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
		os.Exit(2)
	}
	subresCfg := subres.Config{Mode: mode}
	l := loop.New(ctx, loop.Config{Profile: c.Profile, Width: c.Width, Script: script, Subresources: subresCfg, SubresourceTransport: app.SubresourceTransport(app.PolicyFor(c.Profile), subresCfg)}, sink)
	go l.Run()

	m := app.New(l, c.Profile, c.Start)
	program = tea.NewProgram(m)
	if conn != nil {
		bridge, err := app.NewMediaBridge(conn, program.Send)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		m.SetMedia(bridge, tempDir)
	}
	_, err = program.Run()
	m.Cleanup()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// sandboxArgs strips the markers the macOS re-exec adds.
func sandboxArgs(args []string) (rest []string, tempDir string, helperFD int, sandboxed bool) {
	helperFD = -1
	if len(args) > 0 {
		if v, ok := strings.CutPrefix(args[0], sandbox.SandboxedArg+"="); ok {
			tempDir, sandboxed, args = v, true, args[1:]
		}
	}
	if len(args) > 0 {
		if v, ok := strings.CutPrefix(args[0], mediaFDArg+"="); ok {
			if fd, err := strconv.Atoi(v); err == nil {
				helperFD, args = fd, args[1:]
			}
		}
	}
	return args, tempDir, helperFD, sandboxed
}

const mediaFDArg = "--media-fd"

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
func startMedia(c config, tempDir string, helperFD int, sandboxed bool, args *[]string) (io.ReadWriteCloser, error) {
	if !needsHelper(c.Profile) {
		return nil, nil
	}
	if sandboxed && helperFD >= 0 {
		return os.NewFile(uintptr(helperFD), "media-helper"), nil
	}
	path, err := sandbox.HelperPath()
	if err != nil {
		return nil, err
	}
	h, err := sandbox.StartHelper(c.Profile, path, tempDir)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS == "darwin" {
		fd, err := h.InheritableFD()
		if err != nil {
			return nil, err
		}
		*args = append([]string{mediaFDArg + "=" + strconv.Itoa(fd)}, *args...)
	}
	return h.Conn, nil
}

// lockDown applies OS enforcement before any page content is touched. On
// macOS the first run replaces itself here and comes back sandboxed.
func lockDown(c config, tempDir string, args []string, sandboxed bool) (profile.Strengths, error) {
	start := ""
	if u, err := url.Parse(c.Start); err == nil && u.Scheme == "file" {
		start = u.Path
	}
	if !sandboxed {
		if err := sandbox.Reexec(c.Profile, start, tempDir, args); err != nil {
			return nil, err
		}
	}
	return sandbox.Apply(c.Profile, start, tempDir, sandboxed)
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
