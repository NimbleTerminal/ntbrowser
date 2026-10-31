package sandbox

import (
	"os/exec"
	"path/filepath"
	"testing"

	"nimbleterminal.dev/ntbrowser/profile"
)

const childSandboxedEnv = "NT_SANDBOX_REEXECED"

// On macOS the sandbox is applied from outside, as the program does it.
func childCommand(t *testing.T, exe, mode, tmp string) *exec.Cmd {
	t.Helper()
	if mode != "locked" {
		return exec.Command(exe)
	}
	p, _ := profile.Builtin("locked")
	p.Files = profile.FilesTemp
	real, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(sandboxExec, "-p", Policy(p, "", real, exe), exe)
	cmd.Env = []string{childSandboxedEnv + "=1"}
	return cmd
}
