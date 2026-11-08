// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"nimbleterminal.dev/ntbrowser/profile"
	"nimbleterminal.dev/ntconfine"
)

const sandboxExec = "/usr/bin/sandbox-exec"

// Available reports whether sandbox-exec works here. It is deprecated but
// shipped; sandbox_init itself would need cgo. Inside another sandbox it
// cannot apply a policy, so it is tried, once.
var Available = sync.OnceValue(func() bool { return applies(sandboxExec) })

func applies(tool string) bool {
	return exec.Command(tool, "-p", "(version 1)(allow default)", "/usr/bin/true").Run() == nil
}

func q(s string) string { return strconv.Quote(s) }

// Policy is the sandbox-exec profile: deny by default, then only what a
// terminal Go program needs. No process may be started but this executable.
// files are the host's writable files, by position: [0] the diagnostics
// file, granted under the diagnostics gate; [1] the localStorage file,
// granted under local-storage=file. Each grant is one literal path.
func Policy(p profile.Profile, start, tempDir, exe string, files ...string) string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	w("(version 1)")
	w("(deny default)")
	w("(import \"system.sb\")") // what dyld and libSystem need to start
	// system.sb reads more than ntbrowser needs; take that back. Later rules win.
	w("(deny file-read* (subpath \"/private/etc\") (subpath \"/etc\") (subpath \"/Users\") (subpath \"/private/var/root\"))")
	w("(allow process-exec (literal %s))", q(exe))
	w("(allow signal (target self) (target children))")
	w("(allow sysctl-read)")
	w("(allow file-read-metadata)")
	w("(allow mach-lookup (global-name \"com.apple.dnssd.service\") (global-name \"com.apple.SystemConfiguration.configd\") (global-name \"com.apple.trustd.agent\") (global-name \"com.apple.trustd\") (global-name \"com.apple.ocspd\"))")
	w("(allow file-read* (literal %s) (subpath \"/usr/lib\") (subpath \"/System/Library\") (subpath \"/private/var/db/dyld\") (subpath \"/private/var/db/timezone\") (subpath \"/usr/share\") (subpath \"/Library/Keychains\") (subpath \"/System/Volumes/Preboot/Cryptexes\")", q(exe))
	w("  (literal \"/private/etc/hosts\") (literal \"/private/etc/resolv.conf\") (literal \"/private/var/run/resolv.conf\") (literal \"/private/etc/ssl/cert.pem\") (literal \"/private/etc/services\") (literal \"/private/etc/localtime\"))")
	w("(allow file-read* file-write* file-ioctl (literal \"/dev/null\") (literal \"/dev/tty\") (regex #\"^/dev/ttys[0-9]+$\"))")
	w("(allow file-read* (literal \"/dev/urandom\") (literal \"/dev/random\"))")
	w("(allow network-outbound (remote ip \"*:*\") (literal \"/private/var/run/mDNSResponder\"))") // no other Unix socket
	w("(allow system-socket)")
	if start != "" && p.ReadsStartFile() {
		w("(allow file-read* (literal %s))", q(start))
	}
	if tempDir != "" {
		w("(allow file-read* file-write* (subpath %s))", q(tempDir))
	}
	if p.Diagnostics && len(files) > 0 && files[0] != "" {
		w("(allow file-write-data (literal %s))", q(files[0]))
	}
	if p.LocalStorage == profile.LocalStorageFile && len(files) > 1 && files[1] != "" {
		w("(allow file-write-data (literal %s))", q(files[1]))
	}
	return b.String()
}

// HelperPolicy confines the media helper: no network, no terminal, no file
// writes; it reads libmpv and its libraries and the media directory.
func HelperPolicy(helper, tempDir string) string {
	return ntconfine.HelperPolicy(ntconfine.DefaultPolicy(helper, tempDir))
}

// Reexec replaces the process with itself under sandbox-exec. args are the
// program's arguments, which must mark the run as sandboxed so this happens
// once; it returns only if that cannot happen. Without sandbox-exec, under
// best-effort, it returns nil and the program goes on unconfined.
func Reexec(p profile.Profile, start, tempDir string, args []string, files ...string) error {
	if !Available() {
		return checkRequired(p, false)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	argv := append([]string{"sandbox-exec", "-p", Policy(p, start, tempDir, exe, files...), exe}, args...)
	return syscall.Exec(sandboxExec, argv, os.Environ())
}

// Apply reports what Reexec achieved. The policy is applied from outside, so
// the re-exec marker alone proves nothing: a read the policy must refuse does.
func Apply(p profile.Profile, _, _ string, reexeced bool) (profile.Strengths, error) {
	if reexeced {
		_, err := os.ReadFile("/etc/passwd")
		reexeced = err != nil
	}
	return strengths(reexeced), checkRequired(p, reexeced)
}
