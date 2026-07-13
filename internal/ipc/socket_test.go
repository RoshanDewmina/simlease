package ipc

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// shortTempDir returns a short path under /tmp. macOS sun_path is ~104 bytes;
// t.TempDir() paths are often too long for Unix sockets.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "smg-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestListenCreatesSocketMode0600(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "sub", "simurghd.sock")

	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat socket: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("socket mode = %04o, want 0600", perm)
	}

	parent, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("Stat parent: %v", err)
	}
	if perm := parent.Mode().Perm(); perm != 0o700 {
		t.Fatalf("parent mode = %04o, want 0700", perm)
	}
}

func TestListenReclaimsStaleSocket(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "simurghd.sock")

	// Create a real stale unix socket (listen then close; file remains).
	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("stale Listen: %v", err)
	}
	if err := stale.Close(); err != nil {
		t.Fatalf("stale Close: %v", err)
	}

	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen reclaim: %v", err)
	}
	defer ln.Close()

	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatalf("Dial after reclaim: %v", err)
	}
	_ = conn.Close()
}

func TestListenDoesNotUnlinkLiveSocket(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "simurghd.sock")

	live, err := Listen(path)
	if err != nil {
		t.Fatalf("first Listen: %v", err)
	}
	defer live.Close()

	_, err = Listen(path)
	if err == nil {
		t.Fatal("second Listen succeeded; must not unlink a live socket")
	}

	// Original listener must still accept.
	done := make(chan error, 1)
	go func() {
		c, err := live.Accept()
		if err != nil {
			done <- err
			return
		}
		_ = c.Close()
		done <- nil
	}()
	c, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatalf("Dial live socket: %v", err)
	}
	_ = c.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Accept: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Accept timed out")
	}
}
