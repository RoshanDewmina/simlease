package ipc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Listen creates a Unix-domain socket listener at path. The parent directory
// is created with mode 0700 when missing. The socket file is chmod'd to 0600
// before this function returns. path must be injected by the caller (tests use
// t.TempDir()); Listen never resolves ~/.simurgh itself.
//
// If bind fails with EADDRINUSE, Listen probes whether a live process still
// owns the socket. A dead/stale socket file is removed and bind is retried
// once. A live socket is never unlinked.
func Listen(path string) (net.Listener, error) {
	if path == "" {
		return nil, fmt.Errorf("ipc: empty socket path")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("ipc: mkdir socket dir: %w", err)
	}

	ln, err := net.Listen("unix", path)
	if err != nil {
		if !isAddrInUse(err) {
			return nil, fmt.Errorf("ipc: listen %s: %w", path, err)
		}
		if live, probeErr := socketHeld(path); probeErr != nil {
			return nil, fmt.Errorf("ipc: probe %s: %w", path, probeErr)
		} else if live {
			return nil, fmt.Errorf("ipc: listen %s: address already in use", path)
		}
		if remErr := os.Remove(path); remErr != nil && !errors.Is(remErr, os.ErrNotExist) {
			return nil, fmt.Errorf("ipc: remove stale socket %s: %w", path, remErr)
		}
		ln, err = net.Listen("unix", path)
		if err != nil {
			return nil, fmt.Errorf("ipc: listen %s after stale reclaim: %w", path, err)
		}
	}

	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("ipc: chmod socket %s: %w", path, err)
	}
	return ln, nil
}

func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}

// socketHeld reports whether a live process currently accepts connections on path.
func socketHeld(path string) (bool, error) {
	conn, err := net.DialTimeout("unix", path, 200*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return true, nil
	}
	if isConnUnavailable(err) {
		return false, nil
	}
	// Unexpected probe failure: treat as live to avoid unlinking a socket we
	// cannot prove is stale.
	return true, nil
}

func isConnUnavailable(err error) bool {
	return errors.Is(err, syscall.ENOENT) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, os.ErrNotExist)
}
