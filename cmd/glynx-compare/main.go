// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
// glynx-compare is a developer tool, separate from the confined browsing binaries.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"nimbleterminal.dev/ntbrowser/internal/compare"
)

func main() { os.Exit(run()) }
func run() int {
	var o compare.Options
	flag.StringVar(&o.Out, "out", filepath.Join(os.TempDir(), "glynx-compare-"+time.Now().Format("20060102-150405")), "new output directory (must not exist)")
	flag.StringVar(&o.Chrome, "chrome", "", "Chrome/Chromium executable (or CHROME_BIN)")
	flag.IntVar(&o.Width, "width", 1200, "viewport width in CSS pixels (320..2048)")
	flag.IntVar(&o.Height, "height", 900, "viewport height in CSS pixels (200..2048)")
	flag.StringVar(&o.Scheme, "scheme", "light", "light or dark")
	flag.BoolVar(&o.Scripts, "scripts", true, "enable scripts in both engines")
	flag.DurationVar(&o.Settle, "settle", 2*time.Second, "capture delay after first glynx frame / Chrome load (100ms..15s)")
	timeout := flag.Duration("timeout", 60*time.Second, "overall capture deadline (maximum 5m)")
	flag.Parse()
	if flag.NArg() != 1 || *timeout <= 0 || *timeout > 5*time.Minute {
		fmt.Fprintln(os.Stderr, "usage: glynx-compare [flags] https://example.com/page (flags precede URL)")
		return 2
	}
	o.URL = flag.Arg(0)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	_, err := compare.Run(ctx, o)
	if err != nil {
		fmt.Fprintf(os.Stderr, "comparison failed: %q (output %q)\n", err.Error(), o.Out)
		return 1
	}
	fmt.Printf("Comparison written to %q\n", filepath.Join(o.Out, "index.html"))
	return 0
}
