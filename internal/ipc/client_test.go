package ipc

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestDialConnectsWhenDaemonRunning(t *testing.T) {
	dir := shortTempDir(t)
	socketPath := filepath.Join(dir, "simurghd.sock")
	logPath := filepath.Join(dir, "logs", "simurghd.log")

	ln, err := Listen(socketPath)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			_ = c.Close()
		}
	}()

	var spawns atomic.Int32
	origStart := startDetached
	startDetached = func(bin, logPath string) error {
		spawns.Add(1)
		return origStart(bin, logPath)
	}
	t.Cleanup(func() { startDetached = origStart })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := Dial(ctx, socketPath, logPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	_ = conn.Close()
	if spawns.Load() != 0 {
		t.Fatalf("spawns = %d, want 0 when daemon already up", spawns.Load())
	}
}

func TestDialAutoStartsOnceAndRetries(t *testing.T) {
	dir := shortTempDir(t)
	t.Setenv("SIMURGH_HOME", dir)
	socketPath := filepath.Join(dir, "simurghd.sock")
	logPath := filepath.Join(dir, "logs", "simurghd.log")

	fake := buildFakeDaemon(t)

	var spawns atomic.Int32
	origExec := executablePath
	origStart := startDetached
	executablePath = func() (string, error) { return fake, nil }
	startDetached = func(bin, logPath string) error {
		spawns.Add(1)
		return startDetachedTracked(t, bin, logPath)
	}
	t.Cleanup(func() {
		executablePath = origExec
		startDetached = origStart
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := Dial(ctx, socketPath, logPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	_ = conn.Close()

	if spawns.Load() != 1 {
		t.Fatalf("spawns = %d, want exactly 1", spawns.Load())
	}
}

func TestDialAutoStartFailureReturnsTypedError(t *testing.T) {
	dir := shortTempDir(t)
	t.Setenv("SIMURGH_HOME", dir)
	t.Setenv("FAKE_DAEMON_HANG", "1")
	socketPath := filepath.Join(dir, "simurghd.sock")
	logPath := filepath.Join(dir, "logs", "simurghd.log")

	fake := buildFakeDaemon(t)

	var spawns atomic.Int32
	origExec := executablePath
	origStart := startDetached
	executablePath = func() (string, error) { return fake, nil }
	startDetached = func(bin, logPath string) error {
		spawns.Add(1)
		return startDetachedTracked(t, bin, logPath)
	}
	t.Cleanup(func() {
		executablePath = origExec
		startDetached = origStart
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := Dial(ctx, socketPath, logPath)
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil {
		t.Fatal("Dial succeeded; want daemon-not-running error")
	}
	var dn *DaemonNotRunningError
	if !errors.As(err, &dn) || dn.ErrorEnvelope == nil {
		t.Fatalf("err = %v, want DaemonNotRunningError", err)
	}
	if dn.ErrorEnvelope.Code != CodeDaemonNotRunning || dn.ErrorEnvelope.Fix != FixDaemonNotRunning {
		t.Fatalf("envelope = %+v", dn.ErrorEnvelope)
	}
	if spawns.Load() != 1 {
		t.Fatalf("spawns = %d, want exactly 1 (no spawn loop)", spawns.Load())
	}
}

func startDetachedTracked(t *testing.T, bin, logPath string) error {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, "daemon", "run")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// Inherit env (SIMURGH_HOME, FAKE_DAEMON_HANG) from the test process.
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = logFile.Close()
	})
	return nil
}

func buildFakeDaemon(t *testing.T) string {
	t.Helper()
	outDir, err := os.MkdirTemp("/tmp", "smg-fake-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outDir) })
	out := filepath.Join(outDir, "simurgh")
	cmd := exec.Command("go", "build", "-o", out, "./testdata/fakedaemon")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	cmd.Dir = wd
	if outBytes, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fakedaemon: %v\n%s", err, outBytes)
	}
	return out
}
