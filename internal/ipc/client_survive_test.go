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
		if err := checkDaemonPID(socketPath, daemonPID, pingPollTimeout); err != nil {
			t.Fatalf("%v (relaunch?)\nlog:\n%s", err, readFileBestEffort(logPath))
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
	if err := checkDaemonPID(socketPath, daemonPID, pingPollTimeout); err != nil {
		t.Fatalf("after SIGHUP: %v", err)
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

	ctx, cancel := context.WithTimeout(context.Background(), pingPollTimeout)
	defer cancel()
	conn, err := Dial(ctx, socketPath, logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "survive child Dial: %v\n", err)
		return 3
	}
	_ = conn.Close()

	pid, err := pollDaemonPID(socketPath, pingPollTimeout)
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

// pingPollTimeout bounds a single startup / survival-loop / post-SIGHUP ping
// under parallel darwin_sim host load. 10s still exhausted under the full
// tagged race suite; 45s keeps a hard bound while absorbing CoreSimulator
// scheduling contention. Per-attempt dial/read stays short; the outer poll
// retries until this deadline.
const pingPollTimeout = 45 * time.Second

const pingPollInterval = 50 * time.Millisecond

// pollDaemonPID retries daemon.ping until success or timeout. Transient dial /
// read deadlines under host contention are retried; a successful response is
// returned immediately (callers must check PID separately).
func pollDaemonPID(socketPath string, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		attempt := 2 * time.Second
		if attempt > remaining {
			attempt = remaining
		}
		pid, err := pingDaemonPIDErrTimeout(socketPath, attempt)
		if err == nil {
			return pid, nil
		}
		lastErr = err
		sleep := pingPollInterval
		if rem := time.Until(deadline); sleep > rem {
			sleep = rem
		}
		if sleep > 0 {
			time.Sleep(sleep)
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no attempt made")
	}
	return 0, fmt.Errorf("daemon.ping timed out after %s: %w", timeout, lastErr)
}

// checkDaemonPID polls until a successful ping, then requires wantPID
// immediately — a wrong PID must not burn the remaining poll window.
func checkDaemonPID(socketPath string, wantPID int, timeout time.Duration) error {
	pid, err := pollDaemonPID(socketPath, timeout)
	if err != nil {
		return err
	}
	if pid != wantPID {
		return fmt.Errorf("daemon pid changed: got %d want %d", pid, wantPID)
	}
	return nil
}

func pingDaemonPIDErr(socketPath string) (int, error) {
	return pingDaemonPIDErrTimeout(socketPath, 2*time.Second)
}

func pingDaemonPIDErrTimeout(socketPath string, attemptTimeout time.Duration) (int, error) {
	if attemptTimeout <= 0 {
		return 0, fmt.Errorf("daemon.ping: no time remaining")
	}
	conn, err := net.DialTimeout("unix", socketPath, attemptTimeout)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(attemptTimeout))
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

// TestPollDaemonPIDNeverRespondsTimesOut: an accepted-but-silent Unix peer
// must fail when the bounded poll deadline expires — covering hung response
// reads, not only missing-socket dial failures.
func TestPollDaemonPIDNeverRespondsTimesOut(t *testing.T) {
	dir := shortTempDir(t)
	socketPath := filepath.Join(dir, "silent.sock")
	ln, err := Listen(socketPath)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	go serveSilentUnixPeer(ln)

	timeout := 200 * time.Millisecond
	start := time.Now()
	_, err = pollDaemonPID(socketPath, timeout)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error for never-responding endpoint")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error=%v want timed out", err)
	}
	if elapsed < timeout {
		t.Fatalf("returned too early: elapsed=%v timeout=%v", elapsed, timeout)
	}
	if elapsed > timeout+500*time.Millisecond {
		t.Fatalf("poll hung past deadline: elapsed=%v timeout=%v", elapsed, timeout)
	}
}

// serveSilentUnixPeer accepts connections and drains the request but never
// writes a response, so clients hit read deadlines (hung peer).
func serveSilentUnixPeer(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			_, _ = DecodeRequest(c)
			var b [1]byte
			_, _ = c.Read(b[:])
		}(conn)
	}
}

// TestCheckDaemonPIDWrongPIDFailsImmediately: after a successful ping, a wrong
// PID must fail immediately without consuming the remaining poll window.
func TestCheckDaemonPIDWrongPIDFailsImmediately(t *testing.T) {
	dir := shortTempDir(t)
	socketPath := filepath.Join(dir, "fake.sock")
	ln, err := Listen(socketPath)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	const reportedPID = 424242
	go serveFixedPIDPing(ln, reportedPID)

	start := time.Now()
	err = checkDaemonPID(socketPath, 1, 2*time.Second)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected wrong-pid error")
	}
	if !strings.Contains(err.Error(), "424242") || !strings.Contains(err.Error(), "want 1") {
		t.Fatalf("error=%v want got=424242 want=1", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("wrong-pid waited too long: elapsed=%v (must fail immediately after ping)", elapsed)
	}
}

func serveFixedPIDPing(ln net.Listener, pid int) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(2 * time.Second))
			req, err := DecodeRequest(c)
			if err != nil {
				return
			}
			result, _ := json.Marshal(map[string]int{"pid": pid})
			_ = Encode(c, Response{
				V:      ProtocolVersion,
				ID:     req.ID,
				OK:     true,
				Result: result,
			})
		}(conn)
	}
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

// TestDetachedDaemonCommandUsesDevNullAndSetsid directly asserts command
// construction. The real 30s survival proof remains
// TestDialDaemonSurvivesParentExit.
func TestDetachedDaemonCommandUsesDevNullAndSetsid(t *testing.T) {
	dir := shortTempDir(t)
	logPath := filepath.Join(dir, "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	devNull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()

	cmd := detachedDaemonCommand("/tmp/simurgh-test", devNull, logFile)
	if cmd.Stdin != devNull {
		t.Fatalf("stdin = %v want opened %s", cmd.Stdin, os.DevNull)
	}
	if cmd.Stdout != logFile || cmd.Stderr != logFile {
		t.Fatalf("stdout/stderr not attached to daemon log")
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatalf("SysProcAttr = %+v want Setsid", cmd.SysProcAttr)
	}
	if len(cmd.Args) != 3 || cmd.Args[1] != "daemon" || cmd.Args[2] != "run" {
		t.Fatalf("args = %q", cmd.Args)
	}
}
