package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

const (
	CodeDaemonNotRunning = "DAEMON_NOT_RUNNING"
	FixDaemonNotRunning  = "simurgh daemon run"

	dialRetryWindow = 2 * time.Second
	dialRetryTick   = 50 * time.Millisecond
)

// DaemonNotRunningError is returned when Dial cannot connect even after one
// auto-start attempt and one retry.
type DaemonNotRunningError struct {
	ErrorEnvelope *Error
}

func (e *DaemonNotRunningError) Error() string {
	if e == nil || e.ErrorEnvelope == nil {
		return "ipc: daemon not running"
	}
	return fmt.Sprintf("ipc: %s: %s", e.ErrorEnvelope.Code, e.ErrorEnvelope.Message)
}

func (e *DaemonNotRunningError) Unwrap() error { return errDaemonNotRunning }

var errDaemonNotRunning = errors.New("ipc: daemon not running")

// Test seams — production uses os.Executable and a real detached spawn.
var (
	executablePath = os.Executable
	startDetached  = startDetachedDaemon
)

// Dial connects to socketPath. On ENOENT/ECONNREFUSED it spawns
// `os.Executable() daemon run` once (stdout/stderr → logPath), waits briefly
// for the socket, and retries the dial exactly once. No launchctl.
func Dial(ctx context.Context, socketPath, logPath string) (net.Conn, error) {
	if socketPath == "" {
		return nil, fmt.Errorf("ipc: empty socket path")
	}
	conn, err := dialOnce(ctx, socketPath)
	if err == nil {
		return conn, nil
	}
	if !isConnUnavailable(err) {
		return nil, err
	}

	bin, err := executablePath()
	if err != nil {
		return nil, fmt.Errorf("ipc: resolve executable: %w", err)
	}
	if err := startDetached(bin, logPath); err != nil {
		return nil, fmt.Errorf("ipc: auto-start daemon: %w", err)
	}

	if err := waitForSocket(ctx, socketPath, dialRetryWindow); err != nil {
		return nil, daemonNotRunning(err)
	}
	conn, err = dialOnce(ctx, socketPath)
	if err != nil {
		return nil, daemonNotRunning(err)
	}
	return conn, nil
}

func dialOnce(ctx context.Context, socketPath string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", socketPath)
}

func waitForSocket(ctx context.Context, socketPath string, window time.Duration) error {
	deadline := time.Now().Add(window)
	ticker := time.NewTicker(dialRetryTick)
	defer ticker.Stop()

	for {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "unix", socketPath)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func startDetachedDaemon(bin, logPath string) error {
	if logPath == "" {
		return fmt.Errorf("ipc: empty log path")
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()

	cmd := exec.Command(bin, "daemon", "run")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	// Detached: do not Wait. Release process handle.
	_ = cmd.Process.Release()
	return nil
}

func daemonNotRunning(cause error) error {
	msg := "daemon is not running"
	if cause != nil {
		msg = fmt.Sprintf("daemon is not running (%v)", cause)
	}
	env, err := NewError(CodeDaemonNotRunning, msg, FixDaemonNotRunning)
	if err != nil {
		return errDaemonNotRunning
	}
	return &DaemonNotRunningError{ErrorEnvelope: env}
}
