package ipc

import (
	"context"
	"errors"
	"fmt"
	"io"
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

	// CodeDaemonDisconnected is returned when the daemon drops an in-flight
	// RPC after the client already sent the request (EOF / reset / broken pipe).
	CodeDaemonDisconnected = "DAEMON_DISCONNECTED"
	// FixDaemonDisconnected is the exact retry/status remediation for a lost
	// mid-RPC connection (issue #2 / W18: bare EOF on acquire --wait).
	FixDaemonDisconnected = "simurgh daemon status   # confirm daemon is up, then retry your command"

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

	devNull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("ipc: open /dev/null: %w", err)
	}
	defer devNull.Close()

	cmd := detachedDaemonCommand(bin, devNull, logFile)
	if err := cmd.Start(); err != nil {
		return err
	}
	// Detached: do not Wait. Release process handle.
	_ = cmd.Process.Release()
	return nil
}

func detachedDaemonCommand(bin string, stdin, output *os.File) *exec.Cmd {
	cmd := exec.Command(bin, "daemon", "run")
	cmd.Stdin = stdin
	cmd.Stdout = output
	cmd.Stderr = output
	// New session + detached stdin: parent-shell exit / SIGHUP must not kill the
	// daemon. Setsid alone was insufficient (bench evidence); stdin=/dev/null
	// plus daemon-side signal.Ignore(SIGHUP) complete the detach.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
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

// DaemonDisconnectedError is returned when a response read fails because the
// daemon closed or reset the connection after the request was sent.
type DaemonDisconnectedError struct {
	ErrorEnvelope *Error
	Cause         error
}

func (e *DaemonDisconnectedError) Error() string {
	if e == nil || e.ErrorEnvelope == nil {
		return "ipc: daemon disconnected"
	}
	return fmt.Sprintf("ipc: %s: %s", e.ErrorEnvelope.Code, e.ErrorEnvelope.Message)
}

func (e *DaemonDisconnectedError) Unwrap() error {
	if e != nil && e.Cause != nil {
		return e.Cause
	}
	return errDaemonDisconnected
}

var errDaemonDisconnected = errors.New("ipc: daemon disconnected")

// ClassifyResponseFailure maps connection-loss failures on a response read to
// DaemonDisconnectedError. Protocol-frame errors (truncated, malformed, empty,
// oversized, validation) are returned unchanged so they stay protocol errors.
func ClassifyResponseFailure(err error) error {
	if err == nil {
		return nil
	}
	if isProtocolFrameError(err) {
		return err
	}
	if isLostConnection(err) {
		return daemonDisconnected(err)
	}
	return err
}

func isProtocolFrameError(err error) bool {
	return errors.Is(err, ErrTruncatedFrame) ||
		errors.Is(err, ErrMalformedJSON) ||
		errors.Is(err, ErrEmptyFrame) ||
		errors.Is(err, ErrOversizedFrame) ||
		errors.Is(err, ErrMissingID) ||
		errors.Is(err, ErrMissingMethod) ||
		errors.Is(err, ErrInvalidVersion) ||
		errors.Is(err, ErrInvalidError) ||
		errors.Is(err, ErrUnexpectedError) ||
		errors.Is(err, ErrUnexpectedResult) ||
		errors.Is(err, ErrUnsupportedVersion) ||
		errors.Is(err, ErrNoProgress)
}

func isLostConnection(err error) bool {
	if errors.Is(err, io.EOF) {
		return true
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.ECONNRESET || errno == syscall.EPIPE
	}
	return false
}

func daemonDisconnected(cause error) error {
	msg := "connection to daemon lost after request was sent"
	if cause != nil {
		msg = fmt.Sprintf("connection to daemon lost after request was sent (%v)", cause)
	}
	env, err := NewError(CodeDaemonDisconnected, msg, FixDaemonDisconnected)
	if err != nil {
		return errDaemonDisconnected
	}
	return &DaemonDisconnectedError{ErrorEnvelope: env, Cause: cause}
}
