//go:build darwin || linux

package sandbox

import (
	"os"
	"syscall"

	"nimbleterminal.dev/ntbrowser/profile"
)

// StartHelper starts the decoder process, before the main process locks
// itself down, so the main process never needs permission to start anything.
// The helper gets the socket as fd 3, an empty environment, no terminal, and a
// sandbox narrower than the main process's own.
func StartHelper(p profile.Profile, helper, tempDir string) (*Helper, error) {
	if !Available() {
		if err := checkRequired(p, false); err != nil {
			return nil, err
		}
	}
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, err
	}
	syscall.CloseOnExec(fds[0]) // the helper must not hold our end: EOF is how it learns we are gone
	ours := os.NewFile(uintptr(fds[0]), "media-helper")
	theirs := os.NewFile(uintptr(fds[1]), "media-helper-child")
	defer theirs.Close()
	cmd, err := helperCommand(helper, tempDir)
	if err != nil {
		ours.Close()
		return nil, err
	}
	cmd.Env = []string{}
	cmd.ExtraFiles = []*os.File{theirs}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		ours.Close()
		return nil, err
	}
	return &Helper{Conn: ours, cmd: cmd, file: ours}, nil
}

// InheritableFD duplicates our end for the macOS re-exec: a dup is not
// close-on-exec, so it survives into the sandboxed process.
func (h *Helper) InheritableFD() (int, error) { return syscall.Dup(int(h.file.Fd())) }
