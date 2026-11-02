// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"nimbleterminal.dev/ntbrowser/profile"
)

// Landlock, from linux/landlock.h. The numbers are the same on every
// architecture.
const (
	sysLandlockCreateRuleset = 444
	sysLandlockAddRule       = 445
	sysLandlockRestrictSelf  = 446

	landlockCreateRulesetVersion = 1
	landlockRulePathBeneath      = 1

	accessExecute    = 1 << 0
	accessWriteFile  = 1 << 1
	accessReadFile   = 1 << 2
	accessReadDir    = 1 << 3
	accessRemoveDir  = 1 << 4
	accessRemoveFile = 1 << 5
	accessMakeChar   = 1 << 6
	accessMakeDir    = 1 << 7
	accessMakeReg    = 1 << 8
	accessMakeSock   = 1 << 9
	accessMakeFifo   = 1 << 10
	accessMakeBlock  = 1 << 11
	accessMakeSym    = 1 << 12

	accessRefer    = 1 << 13 // ABI 2
	accessTruncate = 1 << 14 // ABI 3

	accessV1     = accessMakeSym<<1 - 1
	accessRead   = accessReadFile | accessReadDir
	accessFileRW = accessReadFile | accessWriteFile | accessTruncate
	accessDirRW  = (accessV1 | accessRefer | accessTruncate) &^ accessExecute
	accessFile   = accessExecute | accessReadFile | accessWriteFile | accessTruncate // all a rule on a file may hold

	netBindTCP    = 1 << 0 // ABI 4
	netConnectTCP = 1 << 1

	scopeAbstractUnix = 1 << 0 // ABI 6
	scopeSignal       = 1 << 1
)

// Missing from package syscall on some architectures.
const (
	oPath           = 0x200000 // O_PATH
	prSetNoNewPrivs = 38       // PR_SET_NO_NEW_PRIVS
)

type landlockRulesetAttr struct {
	HandledAccessFS, HandledAccessNet, Scoped uint64
}

type landlockPathBeneath struct {
	AllowedAccess uint64
	ParentFD      int32
}

// landlockABI is the kernel's landlock version, 0 without it.
func landlockABI() int {
	v, _, errno := syscall.Syscall(sysLandlockCreateRuleset, 0, 0, landlockCreateRulesetVersion)
	if errno != 0 {
		return 0
	}
	return int(v)
}

// Available reports whether the kernel has landlock.
func Available() bool { return landlockABI() > 0 }

// Reexec is not needed on Linux: Apply restricts the running process.
func Reexec(profile.Profile, string, string, []string) error { return nil }

// readPaths are what a terminal Go program reads at run time: name
// resolution, certificates and terminal descriptions. Missing ones are
// skipped.
var readPaths = []string{
	"/etc/hosts", "/etc/resolv.conf", "/etc/nsswitch.conf", "/etc/services", "/etc/localtime",
	"/etc/ssl", "/etc/pki", "/etc/ca-certificates", "/usr/share/ca-certificates", "/usr/lib/ssl",
	"/usr/share/terminfo", "/lib/terminfo", "/etc/terminfo", "/usr/share/zoneinfo",
}

// ruleset is the landlock policy for the given paths.
type ruleset struct {
	fd      int
	handled uint64
}

// newRuleset handles everything this kernel's landlock can.
func newRuleset(isolated bool) (*ruleset, error) {
	abi := landlockABI()
	attr := landlockRulesetAttr{HandledAccessFS: accessV1}
	size := uintptr(8)
	if abi >= 2 {
		attr.HandledAccessFS |= accessRefer
	}
	if abi >= 3 {
		attr.HandledAccessFS |= accessTruncate
	}
	if isolated && abi >= 4 {
		attr.HandledAccessNet, size = netBindTCP|netConnectTCP, 16
	}
	if isolated && abi >= 6 {
		attr.Scoped, size = scopeAbstractUnix|scopeSignal, 24
	}
	fd, _, errno := syscall.Syscall(sysLandlockCreateRuleset, uintptr(unsafe.Pointer(&attr)), size, 0)
	if errno != 0 {
		return nil, fmt.Errorf("landlock: %w", errno)
	}
	return &ruleset{fd: int(fd), handled: attr.HandledAccessFS}, nil
}

// add allows access beneath path; a missing path is skipped.
func (r *ruleset) add(path string, access uint64) error {
	f, err := os.OpenFile(path, oPath|syscall.O_CLOEXEC, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && !st.IsDir() {
		access &= accessFile // file rules take file rights only
	}
	rule := landlockPathBeneath{AllowedAccess: access & r.handled, ParentFD: int32(f.Fd())}
	_, _, errno := syscall.Syscall6(sysLandlockAddRule, uintptr(r.fd), landlockRulePathBeneath, uintptr(unsafe.Pointer(&rule)), 0, 0, 0)
	runtime.KeepAlive(f)
	if errno != 0 {
		return fmt.Errorf("landlock add %s: %w", path, errno)
	}
	return nil
}

// restrict applies the ruleset to every thread; the process cannot undo it.
func (r *ruleset) restrict() error {
	if _, _, errno := syscall.AllThreadsSyscall(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0); errno != 0 {
		return fmt.Errorf("no_new_privs: %w", errno)
	}
	if _, _, errno := syscall.AllThreadsSyscall(sysLandlockRestrictSelf, uintptr(r.fd), 0, 0); errno != 0 {
		return fmt.Errorf("landlock restrict: %w", errno)
	}
	syscall.Close(r.fd)
	return nil
}

func mainRuleset(start, tempDir string, canRead bool) (*ruleset, error) {
	r, err := newRuleset(false)
	if err != nil {
		return nil, err
	}
	for _, p := range readPaths {
		if err := r.add(p, accessRead); err != nil {
			return nil, err
		}
	}
	for _, p := range []string{"/dev/null", "/dev/tty", "/dev/urandom"} {
		if err := r.add(p, accessFileRW); err != nil {
			return nil, err
		}
	}
	if err := r.add("/dev/pts", accessFileRW|accessReadDir); err != nil {
		return nil, err
	}
	if start != "" && canRead {
		if err := r.add(start, accessReadFile); err != nil {
			return nil, err
		}
	}
	if tempDir != "" {
		if err := r.add(tempDir, accessDirRW); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// Apply restricts this process with landlock and seccomp, before any page
// content is touched.
func Apply(p profile.Profile, start, tempDir string, _ bool) (profile.Strengths, error) {
	if !Available() {
		return strengths(false), checkRequired(p, false)
	}
	r, err := mainRuleset(start, tempDir, p.ReadsStartFile())
	if err == nil {
		err = r.restrict()
	}
	if err == nil {
		err = denySyscalls(deniedMain)
	}
	if err != nil {
		return strengths(false), err
	}
	return strengths(true), nil
}
