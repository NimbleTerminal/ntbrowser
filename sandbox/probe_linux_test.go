package sandbox

import (
	"syscall"
	"testing"
)

// platformProbe tries the socket route that bypasses the socket() filter.
func platformProbe() error {
	var params [120]byte
	fd, _, errno := syscall.Syscall(sysIOUringSetup, 4, uintptrOf(&params), 0)
	if errno != 0 {
		return errno
	}
	syscall.Close(int(fd))
	return nil
}

func TestSeccompTSyncFailureIsAnError(t *testing.T) {
	if err := tsyncResult(1234, 0); err == nil {
		t.Fatal("a thread that did not sync must be an error")
	}
	if err := tsyncResult(0, 0); err != nil {
		t.Fatal(err)
	}
}
