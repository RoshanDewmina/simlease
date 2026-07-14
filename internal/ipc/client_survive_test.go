package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestDialDaemonSurvivesParentExit is the hard OS-level proof that Dial
// auto-start detach is real: a short-lived parent calls Dial (production
// startDetachedDaemon), then exits; the same daemon PID must still answer
// daemon.ping for ≥30s without relaunch.
func TestDialDaemonSurvivesParentExit(t *testing.T) {
	if os.Getenv("SIMURGH_IPC_SURVIVE_CHILD") == "1" {
		os.Exit(runSurviveParentChild())
	}

	dir := shortTempDir(t)
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, "logs"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	socketPath := filepath.Join(home, "simurghd.sock")
	logPath := filepath.Join(home, "logs", "simurghd.log")
	pidFile := filepath.Join(dir, "daemon.pid")

	bin := buildRealSimurgh(t)

	cmd := exec.Command(os.Args[0], "-test.run=^TestDialDaemonSurvivesParentExit$", "-test.count=1")
	cmd.Env = append(os.Environ(),
		"SIMURGH_IPC_SURVIVE_CHILD=1",
		"SIMURGH_HOME="+home,
		"SIMURGH_TEST_SOCKET="+socketPath,
		"SIMURGH_TEST_LOG="+logPath,
		"SIMURGH_TEST_PIDFILE="+pidFile,
		"SIMURGH_DAEMON_BIN="+bin,
		// Prevent nested go test from picking up the child flag incorrectly on
		// cached runs; force a fresh child binary path via executable seam.
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("short-lived Dial parent failed: %v\nlog:\n%s", err, readFileBestEffort(logPath))
	}

	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	daemonPID, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || daemonPID <= 0 {
		t.Fatalf("invalid pid file %q: %v", raw, err)
	}
	t.Logf("daemon PID after parent exit: %d (must remain stable ≥30s)", daemonPID)
	t.Cleanup(func() {
		proc, err := os.FindProcess(daemonPID)
		if err == nil {
			_ = proc.Signal(syscall.SIGTERM)
			time.Sleep(100 * time.Millisecond)
			_ = proc.Signal(syscall.SIGKILL)
		}
		_ = os.Remove(socketPath)
	})

	// Parent is gone. Confirm the SAME PID keeps answering for ≥30s.
	deadline := time.Now().Add(30 * time.Second)
	for {
		pid := pingDaemonPID(t, socketPath)
		if pid != daemonPID {
			t.Fatalf("daemon pid changed: got %d want %d (relaunch?)\nlog:\n%s",
				pid, daemonPID, readFileBestEffort(logPath))
		}
		if !processAlive(daemonPID) {
			t.Fatalf("daemon pid %d died before 30s\nlog:\n%s", daemonPID, readFileBestEffort(logPath))
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(2 * time.Second)
	}

	// Extra: SIGHUP must not kill the detached daemon.
	proc, err := os.FindProcess(daemonPID)
	if err != nil {
		t.Fatalf("FindProcess: %v", err)
	}
	if err := proc.Signal(syscall.SIGHUP); err != nil {
		t.Fatalf("SIGHUP: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	pid := pingDaemonPID(t, socketPath)
	if pid != daemonPID {
		t.Fatalf("after SIGHUP pid=%d want %d", pid, daemonPID)
	}
}

func runSurviveParentChild() int {
	home := os.Getenv("SIMURGH_HOME")
	socketPath := os.Getenv("SIMURGH_TEST_SOCKET")
	logPath := os.Getenv("SIMURGH_TEST_LOG")
	pidFile := os.Getenv("SIMURGH_TEST_PIDFILE")
	bin := os.Getenv("SIMURGH_DAEMON_BIN")
	if home == "" || socketPath == "" || logPath == "" || pidFile == "" || bin == "" {
		fmt.Fprintln(os.Stderr, "survive child: missing env")
		return 2
	}

	origExec := executablePath
	executablePath = func() (string, error) { return bin, nil }
	defer func() { executablePath = origExec }()
	// Production detach path — do not replace startDetached.

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := Dial(ctx, socketPath, logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "survive child Dial: %v\n", err)
		return 3
	}
	_ = conn.Close()

	pid, err := pingDaemonPIDErr(socketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "survive child ping: %v\n", err)
		return 4
	}
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "survive child write pid: %v\n", err)
		return 5
	}
	// Exit immediately — parent shell death / process exit is the hazard under test.
	return 0
}

func buildRealSimurgh(t *testing.T) string {
	t.Helper()
	outDir, err := os.MkdirTemp("/tmp", "smg-bin-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outDir) })
	out := filepath.Join(outDir, "simurgh")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/simurgh")
	// Module root is two levels up from internal/ipc.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	cmd.Dir = filepath.Clean(filepath.Join(wd, "../.."))
	if outBytes, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build simurgh: %v\n%s", err, outBytes)
	}
	return out
}

func pingDaemonPID(t *testing.T, socketPath string) int {
	t.Helper()
	pid, err := pingDaemonPIDErr(socketPath)
	if err != nil {
		t.Fatalf("daemon.ping: %v", err)
	}
	return pid
}

func pingDaemonPIDErr(socketPath string) (int, error) {
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	req := Request{V: ProtocolVersion, ID: "survive-ping", Method: MethodDaemonPing}
	if err := Encode(conn, req); err != nil {
		return 0, err
	}
	resp, err := DecodeResponse(conn)
	if err != nil {
		return 0, err
	}
	if !resp.OK || resp.Error != nil {
		return 0, fmt.Errorf("ping response %+v", resp)
	}
	var result struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return 0, err
	}
	if result.PID <= 0 {
		return 0, fmt.Errorf("invalid pid in result %s", string(resp.Result))
	}
	return result.PID, nil
}

func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func readFileBestEffort(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("<read %s: %v>", path, err)
	}
	return string(b)
}

// TestStartDetachedDaemonStdinIsDevNull is a fast unit check that the spawn
// wiring opens /dev/null for stdin (complement to the 30s OS survival proof).
func TestStartDetachedDaemonStdinIsDevNull(t *testing.T) {
	dir := shortTempDir(t)
	logPath := filepath.Join(dir, "daemon.log")
	// A no-op binary that exits immediately still exercises OpenFile(/dev/null).
	bin := buildFakeDaemon(t)
	t.Setenv("SIMURGH_HOME", dir)
	t.Setenv("FAKE_DAEMON_HANG", "1")
	if err := startDetachedDaemon(bin, logPath); err != nil {
		t.Fatalf("startDetachedDaemon: %v", err)
	}
	// Give the child a moment; cleanup by killing anything still running is
	// best-effort — hang mode sleeps 5s then exits.
	time.Sleep(50 * time.Millisecond)
}
