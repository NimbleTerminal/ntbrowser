// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"nimbleterminal.dev/ntbrowser/profile"
	"nimbleterminal.dev/ntbrowser/sandbox"
	"nimbleterminal.dev/ntmediabox"
)

// TestMain hands the Linux stage, which re-runs this binary, to the real
// ntbrowser built beside the helper: only a binary without goffi can restrict
// all its threads.
func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == sandbox.StageArg {
		main := filepath.Join(os.Args[2], "ntbrowser")
		fmt.Fprintln(os.Stderr, syscall.Exec(main, []string{main, sandbox.StageArg, os.Args[2]}, nil))
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// TestRealHelperDecodes runs the real binary under its real sandbox and
// decodes a real file. Opt-in, like ntvideo's libmpv tests.
func TestRealHelperDecodes(t *testing.T) {
	source := os.Getenv("NTVIDEO_TEST_FILE")
	if source == "" {
		t.Skip("set NTVIDEO_TEST_FILE")
	}
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	bin := filepath.Join(dir, "ntbrowser-media")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	build := exec.Command("go", "build", "-o", filepath.Join(dir, "ntbrowser"), "./cmd/ntbrowser")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "clip"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	p, _ := profile.Builtin("browse")
	p.Enforcement = profile.EnforceRequired
	h, err := sandbox.StartHelper(p.WithVideo(), bin, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	frames := make(chan ntmediabox.Frame, 4)
	statuses := make(chan ntmediabox.Status, 64)
	c := ntmediabox.NewClientWithKill(h.Conn, func(s ntmediabox.Status) {
		select {
		case statuses <- s:
		default:
		}
	}, func(frame ntmediabox.Frame) {
		select {
		case frames <- frame:
		default:
		}
	}, func() { h.Close() })
	defer c.Close()
	for _, err := range []error{c.Handshake("video"), c.Resize(320, 240)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	wait := func(wantFailure bool) {
		t.Helper()
		gen := c.Gen()
		deadline := time.After(10 * time.Second)
		for {
			select {
			case frame := <-frames:
				if frame.Gen != gen {
					continue
				}
				if wantFailure {
					t.Fatal("over-limit media produced a frame")
				}
				t.Logf("generation %d frame %v", gen, frame.Image.Bounds())
				return
			case s := <-statuses:
				if s.Gen != gen {
					continue
				}
				if s.State == ntmediabox.Failed {
					if wantFailure && strings.Contains(s.Err, "size limit") {
						return
					}
					t.Fatalf("failed: %s", s.Err)
				}
			case <-c.Dead():
				t.Fatalf("helper died: %v (%s)", c.Err(), h.Stderr())
			case <-deadline:
				t.Fatal("no expected response within 10s")
			}
		}
	}
	// The worker must honor the host's file budget, then recover on a new Open.
	if len(data) < 2 {
		t.Fatal("test clip is too small")
	}
	if err := c.OpenMedia(ntmediabox.OpenRequest{Name: "clip", Limits: ntmediabox.Limits{MaxFileBytes: int64(len(data) - 1)}}); err != nil {
		t.Fatal(err)
	}
	wait(true)
	for range 2 {
		if err := c.OpenMedia(ntmediabox.OpenRequest{Name: "clip", Limits: ntmediabox.Limits{MaxFileBytes: int64(len(data))}}); err != nil {
			t.Fatal(err)
		}
		if err := c.Play(); err != nil {
			t.Fatal(err)
		}
		wait(false)
		if err := c.Stop(); err != nil {
			t.Fatal(err)
		}
	}
}
