package crew

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestReadSidecarRefusesAFifoWithoutBlocking(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "a"+SidecarSuffix), 0o600); err != nil {
		t.Skip("no fifo here:", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := ReadSidecar(dir, realPath(dir), "a", []byte("x"))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a fifo sidecar was read")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReadSidecar blocked on a fifo")
	}
}
