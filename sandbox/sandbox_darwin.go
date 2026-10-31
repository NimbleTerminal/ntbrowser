package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"nimbleterminal.dev/ntbrowser/profile"
)

const sandboxExec = "/usr/bin/sandbox-exec"

// SandboxedArg marks the re-executed process.
const SandboxedArg = "--sandboxed"

// Available reports whether sandbox-exec is present. It is deprecated but
// shipped; sandbox_init itself would need cgo.
func Available() bool {
	_, err := os.Stat(sandboxExec)
	return err == nil
}

func q(s string) string { return strconv.Quote(s) }

// Policy is the sandbox-exec profile: deny by default, then only what a
// terminal Go program needs. No process may be started but this executable.
func Policy(p profile.Profile, start, tempDir, exe string) string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	w("(version 1)")
	w("(deny default)")
	w("(import \"system.sb\")") // what dyld and libSystem need to start
	// system.sb reads more than ntbrowser needs; take that back. Later rules win.
	w("(deny file-read* (subpath \"/private/etc\") (subpath \"/etc\") (subpath \"/Users\") (subpath \"/private/var/root\"))")
	w("(allow process-exec (literal %s))", q(exe))
	w("(allow signal (target self))")
	w("(allow sysctl-read)")
	w("(allow file-read-metadata)")
	w("(allow mach-lookup (global-name \"com.apple.dnssd.service\") (global-name \"com.apple.SystemConfiguration.configd\") (global-name \"com.apple.trustd.agent\") (global-name \"com.apple.trustd\") (global-name \"com.apple.ocspd\"))")
	w("(allow file-read* (literal %s) (subpath \"/usr/lib\") (subpath \"/System/Library\") (subpath \"/private/var/db/dyld\") (subpath \"/private/var/db/timezone\") (subpath \"/usr/share\") (subpath \"/Library/Keychains\") (subpath \"/System/Volumes/Preboot/Cryptexes\")", q(exe))
	w("  (literal \"/private/etc/hosts\") (literal \"/private/etc/resolv.conf\") (literal \"/private/var/run/resolv.conf\") (literal \"/private/etc/ssl/cert.pem\") (literal \"/private/etc/services\") (literal \"/private/etc/localtime\"))")
	w("(allow file-read* file-write* file-ioctl (literal \"/dev/null\") (literal \"/dev/tty\") (regex #\"^/dev/ttys[0-9]+$\"))")
	w("(allow file-read* (literal \"/dev/urandom\") (literal \"/dev/random\"))")
	w("(allow network-outbound)")
	w("(allow system-socket)")
	w("(allow network-inbound (local unix))")
	if start != "" && p.ReadsStartFile() {
		w("(allow file-read* (literal %s))", q(start))
	}
	if tempDir != "" {
		w("(allow file-read* file-write* (subpath %s))", q(tempDir))
	}
	return b.String()
}

// Reexec replaces the process with itself under sandbox-exec. args are the
// program's arguments; it returns only if that cannot happen. The sandboxed
// process sees SandboxedArg first, so this happens once.
func Reexec(p profile.Profile, start, tempDir string, args []string) error {
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
	argv := append([]string{"sandbox-exec", "-p", Policy(p, start, tempDir, exe), exe, SandboxedArg + "=" + tempDir}, args...)
	return syscall.Exec(sandboxExec, argv, os.Environ())
}

// Apply reports what Reexec achieved: on macOS the policy is applied from
// outside, so here it is only recorded.
func Apply(p profile.Profile, _, _ string, reexeced bool) (profile.Strengths, error) {
	return strengths(reexeced), checkRequired(p, reexeced)
}
