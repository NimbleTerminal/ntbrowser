package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"nimbleterminal.dev/ntbrowser/profile"
)

// The tests re-run this binary as a child that locks itself down and then
// tries each escape. A control child without the sandbox proves the attempts
// work when nothing stops them.
func TestMain(m *testing.M) {
	if tmp := os.Getenv("NT_SANDBOX_CHILD"); tmp != "" {
		os.Exit(child(tmp, os.Getenv("NT_SANDBOX_MODE") == "locked"))
	}
	os.Exit(m.Run())
}

func denied(err error) bool {
	return errors.Is(err, os.ErrPermission)
}

func child(tmp string, locked bool) int {
	p, _ := profile.Builtin("locked")
	p.Files = profile.FilesTemp
	if locked {
		s, err := Apply(p, "", tmp, os.Getenv(childSandboxedEnv) != "")
		if err != nil {
			fmt.Println("apply:", err)
			return 2
		}
		if s["files"] != profile.StrengthOS || s["process"] != profile.StrengthOS {
			fmt.Println("strengths:", s)
			return 2
		}
	}
	_, readErr := os.ReadFile("/etc/passwd")
	writeErr := os.WriteFile(filepath.Join(os.Getenv("NT_SANDBOX_OUTSIDE"), "x"), []byte("x"), 0o600)
	execErr := exec.Command("/bin/ls", "/").Run()
	tempErr := os.WriteFile(filepath.Join(tmp, "ok"), []byte("x"), 0o600)
	if tempErr != nil {
		fmt.Println("temp write:", tempErr)
		return 3
	}
	if !locked {
		if readErr != nil || writeErr != nil || execErr != nil {
			fmt.Println("control failed:", readErr, writeErr, execErr)
			return 4
		}
		return 0
	}
	if !denied(readErr) {
		fmt.Println("read not denied:", readErr)
		return 5
	}
	if !denied(writeErr) {
		fmt.Println("write not denied:", writeErr)
		return 6
	}
	if execErr == nil {
		fmt.Println("exec not denied")
		return 7
	}
	return 0
}

func runChild(t *testing.T, mode string) (string, error) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	cmd := childCommand(t, exe, mode, tmp)
	cmd.Env = append(os.Environ(), cmd.Env...)
	cmd.Env = append(cmd.Env, "NT_SANDBOX_CHILD="+tmp, "NT_SANDBOX_MODE="+mode, "NT_SANDBOX_OUTSIDE="+t.TempDir())
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestControlEscapesWork(t *testing.T) {
	if out, err := runChild(t, "control"); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}

func TestLockedCannotEscape(t *testing.T) {
	if !Available() {
		t.Skip("OS enforcement unavailable here")
	}
	if out, err := runChild(t, "locked"); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}

func TestRequiredFailsWhenUnavailable(t *testing.T) {
	p, _ := profile.Builtin("locked")
	if err := checkRequired(p, false); err == nil {
		t.Fatal("required with nothing available must fail")
	}
	if err := checkRequired(p, true); err != nil {
		t.Fatal(err)
	}
	p.Enforcement = profile.EnforceBestEffort
	if err := checkRequired(p, false); err != nil {
		t.Fatal(err)
	}
}

func TestStrengthsReportWhatApplied(t *testing.T) {
	if s := strengths(false); len(s) != 0 {
		t.Fatalf("nothing applied but reported %v", s)
	}
	s := strengths(true)
	for _, gate := range []string{"files", "process", "native-code"} {
		if s[gate] != profile.StrengthOS {
			t.Errorf("%s: %q", gate, s[gate])
		}
	}
}
