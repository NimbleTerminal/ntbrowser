// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

//go:build darwin || linux

package browsercmd

import (
	"net"
	"nimbleterminal.dev/ntbrowser/profile"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestResumedMediaConnectionSupportsDeadlines(t *testing.T) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	peer := os.NewFile(uintptr(fds[1]), "peer")
	defer peer.Close()
	p, _ := profile.Builtin("browse")
	st := state{Config: config{Profile: p.WithVideo()}, HelperFD: fds[0]}
	conn, cleanup, err := startMedia(&st, true)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	c, ok := conn.(net.Conn)
	if !ok {
		t.Fatal("resumed connection has no deadlines")
	}
	if err := c.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, err = c.Read(make([]byte, 1))
	if e, ok := err.(net.Error); !ok || !e.Timeout() {
		t.Fatalf("read = %v, want timeout", err)
	}
	cleanup()
	cleanup()
	if _, err := conn.Write([]byte{1}); err == nil {
		t.Fatal("connection remained open")
	}
}
