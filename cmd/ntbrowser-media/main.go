// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

// Command ntbrowser-media decodes media for ntbrowser in its own sandboxed
// process. ntbrowser starts it; it is not meant to be run by hand. The
// connection is the socket on fd 3 and the only argument is the media
// directory. All native code in ntbrowser lives here, in its own Go module.
package main

import (
	"errors"
	"fmt"
	"os"

	"nimbleterminal.dev/ntmediabox"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "ntbrowser-media is started by ntbrowser")
		os.Exit(2)
	}
	conn := ntmediabox.WorkerConn()
	dir := os.Args[1]
	err := ntmediabox.Serve(conn, func(kind string) (ntmediabox.Decoder, error) {
		if kind != "video" {
			return nil, errors.New("unknown media kind")
		}
		return newVideoDecoder(dir), nil
	})
	if err != nil {
		os.Exit(1)
	}
}
