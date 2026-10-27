// Command ntbrowser runs a sandboxed page in the terminal.
//
//	ntbrowser https://example.com/page.html
//	ntbrowser -allow .example.com -nav confirm -subresources origin -width 800 file:///path/to/page.html
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"nimbleterminal.dev/ntbrowser/app"
	"nimbleterminal.dev/ntbrowser/jsvm"
	"nimbleterminal.dev/ntbrowser/loop"
	"nimbleterminal.dev/ntbrowser/netpolicy"
	"nimbleterminal.dev/ntbrowser/subres"
)

func main() {
	allow := flag.String("allow", "", "comma-separated extra hosts fetch() may reach (leading dot = subdomains)")
	private := flag.Bool("allow-private", false, "permit loopback/private addresses (development)")
	width := flag.Int("width", 800, "document pixel width")
	slice := flag.Duration("slice", 50*time.Millisecond, "JS time slice per callback")
	external := flag.Bool("external-scripts", false, "fetch <script src> through the allowlist")
	navFlag := flag.String("nav", "free", "link navigation: free (any http(s) origin), allowlist (origin + -allow), confirm (ask before leaving the origin)")
	subresFlag := flag.String("subresources", "any", "stylesheets and images: any (any public origin), origin (page origin + -allow), off")
	seed := flag.Uint64("seed", 0, "PRNG seed for Math.random, for reproducible runs (0 = time-based); the clock is never fixed, timers depend on it")
	flag.Parse()
	nav, err := app.ParseNavMode(*navFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	mode, err := subres.ParseMode(*subresFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: ntbrowser [flags] <url|file://path>")
		os.Exit(2)
	}
	// Process-wide soft heap cap: Goja has no per-runtime memory limit, so
	// the GC is told to work harder past this point and dom/nthtml limits
	// keep individual allocations bounded.
	debug.SetMemoryLimit(512 << 20)

	policy := netpolicy.Policy{AllowPrivate: *private}
	if *allow != "" {
		policy.AllowHosts = strings.Split(*allow, ",")
	}
	// The only os.Open in the program; installed into app, never jsvm.
	app.SetLocalReader(func(path string) (io.ReadCloser, error) { return os.Open(path) })

	var program *tea.Program
	sink := sinkFunc(func(msg tea.Msg) { program.Send(msg) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	script := jsvm.Config{Slice: *slice}
	if *seed != 0 {
		script.Rand = seededRand(*seed)
	}
	subresCfg := subres.Config{Mode: mode}
	l := loop.New(ctx, loop.Config{Width: *width, Script: script, ExternalScripts: *external, Subresources: subresCfg, SubresourceTransport: app.SubresourceTransport(policy, subresCfg)}, sink)
	go l.Run()

	m := app.New(l, policy, nav, flag.Arg(0))
	program = tea.NewProgram(m)
	if _, err := program.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
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
