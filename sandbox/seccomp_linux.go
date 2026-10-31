package sandbox

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

// BPF and seccomp constants from linux/filter.h and linux/seccomp.h.
const (
	bpfLD  = 0x00
	bpfW   = 0x00
	bpfABS = 0x20
	bpfJMP = 0x05
	bpfJEQ = 0x10
	bpfK   = 0x00
	bpfRET = 0x06

	seccompSetModeFilter = 1
	seccompFilterTSync   = 1
	seccompRetAllow      = 0x7fff0000
	seccompRetErrno      = 0x00050000
	seccompRetKill       = 0x80000000

	auditArchX8664   = 0xc000003e
	auditArchAArch64 = 0xc00000b7
)

type sockFilter struct {
	Code uint16
	JT   uint8
	JF   uint8
	K    uint32
}

type sockFprog struct {
	Len    uint16
	Filter *sockFilter
}

// deniedMain are calls no ntbrowser process makes after lockdown.
var deniedMain = []uintptr{
	syscall.SYS_EXECVE, sysExecveat, syscall.SYS_PTRACE, syscall.SYS_MOUNT, syscall.SYS_UMOUNT2,
	sysInitModule, sysFinitModule, sysBPF, sysKexecLoad, sysProcessVMReadv, sysProcessVMWritev,
}

func auditArch() uint32 {
	if runtime.GOARCH == "arm64" {
		return auditArchAArch64
	}
	return auditArchX8664
}

// denySyscalls installs a filter on every thread: a listed call fails with
// EPERM, a call from another architecture kills the process.
func denySyscalls(calls []uintptr) error {
	prog := []sockFilter{
		{Code: bpfLD | bpfW | bpfABS, K: 4}, // seccomp_data.arch
		{Code: bpfJMP | bpfJEQ | bpfK, JT: 1, K: auditArch()},
		{Code: bpfRET | bpfK, K: seccompRetKill},
		{Code: bpfLD | bpfW | bpfABS, K: 0}, // seccomp_data.nr
	}
	for _, nr := range calls {
		prog = append(prog,
			sockFilter{Code: bpfJMP | bpfJEQ | bpfK, JF: 1, K: uint32(nr)},
			sockFilter{Code: bpfRET | bpfK, K: seccompRetErrno | uint32(syscall.EPERM)})
	}
	prog = append(prog, sockFilter{Code: bpfRET | bpfK, K: seccompRetAllow})
	fprog := sockFprog{Len: uint16(len(prog)), Filter: &prog[0]}
	if _, _, errno := syscall.RawSyscall(sysSeccomp, seccompSetModeFilter, seccompFilterTSync, uintptr(unsafe.Pointer(&fprog))); errno != 0 {
		return fmt.Errorf("seccomp: %w", errno)
	}
	return nil
}
