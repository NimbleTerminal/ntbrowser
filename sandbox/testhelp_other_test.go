//go:build !darwin

package sandbox

import (
	"os/exec"
	"testing"
)

const childSandboxedEnv = "NT_SANDBOX_UNUSED"

func childCommand(t *testing.T, exe, mode, tmp string) *exec.Cmd {
	t.Helper()
	return exec.Command(exe)
}
