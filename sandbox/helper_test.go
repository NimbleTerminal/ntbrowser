package sandbox

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nimbleterminal.dev/ntbrowser/profile"
)

const probeMarker = "nt-helper-probe"

// helperProbe runs as the helper: fd 3 is the socket, argv[1] the media
// directory, and it reports what it could do.
func helperProbe(tmp string) {
	conn := os.NewFile(3, "socket")
	report := func(name string, err error) { fmt.Fprintf(conn, "%s=%v\n", name, err == nil) }
	fmt.Fprintf(conn, "env=%d\n", len(os.Environ()))
	c, err := net.DialTimeout("tcp", "1.1.1.1:443", 500*time.Millisecond)
	if c != nil {
		c.Close()
	}
	report("network", err)
	_, err = os.ReadFile("/etc/passwd")
	report("passwd", err)
	_, err = os.ReadFile(filepath.Join(tmp, "clip"))
	report("readtemp", err)
	report("writetemp", os.WriteFile(filepath.Join(tmp, "new"), []byte("x"), 0o600))
	_, err = os.Stat("/dev/tty")
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if f != nil {
		f.Close()
	}
	report("tty", err)
	fmt.Fprintln(conn, "done")
	conn.Close()
	os.Exit(0)
}

func runProbe(t *testing.T) map[string]string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		t.Fatal(err)
	}
	tmp, err := os.MkdirTemp("", probeMarker)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	if tmp, err = filepath.EvalSymlinks(tmp); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "clip"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, _ := profile.Builtin("browse")
	h, err := StartHelper(p.WithVideo(), exe, tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	h.Conn.(*os.File).SetDeadline(time.Now().Add(10 * time.Second))
	got := map[string]string{}
	r := bufio.NewReader(h.Conn)
	for {
		line, err := r.ReadString('\n')
		if err == io.EOF || err != nil || strings.TrimSpace(line) == "done" {
			break
		}
		k, v, _ := strings.Cut(strings.TrimSpace(line), "=")
		got[k] = v
	}
	return got
}

func TestHelperIsConfined(t *testing.T) {
	if !Available() {
		t.Skip("OS enforcement unavailable here")
	}
	got := runProbe(t)
	want := map[string]string{"env": "0", "network": "false", "passwd": "false", "readtemp": "true", "writetemp": "false", "tty": "false"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
}
