package sandbox

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"nimbleterminal.dev/ntbrowser/profile"
)

// The tests re-run this binary as a child that locks itself down and then
// tries each escape. A control child without the sandbox proves the attempts
// work when nothing stops them.
func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == StageArg {
		fmt.Println(MediaStage(os.Args[0], os.Args[2])) // the helper stage: never returns on success
		os.Exit(9)
	}
	if len(os.Args) == 2 && strings.Contains(os.Args[1], probeMarker) {
		helperProbe(os.Args[1])
	}
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
	if !locked {
		if c, err := net.Dial("unix", os.Getenv("NT_SANDBOX_SOCK")); err != nil {
			fmt.Println("control socket:", err)
			return 4
		} else {
			c.Close()
		}
	}
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
	victim := os.Getenv("NT_SANDBOX_VICTIM")
	if err := os.Truncate(victim, 0); !denied(err) {
		fmt.Println("truncate not denied:", err)
		return 8
	}
	if err := os.Chmod(victim, 0o777); !denied(err) {
		fmt.Println("chmod not denied:", err)
		return 8
	}
	// The sandbox must still let the program reach the web.
	c, err := net.DialTimeout("tcp", os.Getenv("NT_SANDBOX_TCP"), 2*time.Second)
	if err != nil {
		fmt.Println("tcp refused:", err)
		return 10
	}
	c.Close()
	if runtime.GOOS == "darwin" {
		if c, err := net.Dial("unix", os.Getenv("NT_SANDBOX_SOCK")); err == nil {
			c.Close()
			fmt.Println("unix socket not denied")
			return 9
		}
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
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim")
	if err := os.WriteFile(victim, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	sock, err := os.MkdirTemp("/tmp", "nts")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sock) })
	l, err := net.Listen("unix", filepath.Join(sock, "s"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	cmd.Env = append(cmd.Env, "NT_SANDBOX_TCP="+tcp.Addr().String())
	cmd.Env = append(cmd.Env, "NT_SANDBOX_CHILD="+tmp, "NT_SANDBOX_MODE="+mode, "NT_SANDBOX_OUTSIDE="+outside, "NT_SANDBOX_VICTIM="+victim, "NT_SANDBOX_SOCK="+filepath.Join(sock, "s"))
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
	want := profile.Strengths{"native-code": profile.StrengthCompiledOut}
	if s := strengths(false); len(s) != 1 || s["native-code"] != want["native-code"] {
		t.Fatalf("nothing applied but reported %v", s)
	}
	s := strengths(true)
	for _, gate := range []string{"files", "process"} {
		if s[gate] != profile.StrengthOS {
			t.Errorf("%s: %q", gate, s[gate])
		}
	}
	if s["native-code"] != profile.StrengthCompiledOut {
		t.Errorf("native-code: %q", s["native-code"])
	}
}
