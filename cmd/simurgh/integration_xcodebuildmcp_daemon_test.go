//go:build darwin_sim

package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
	"github.com/RoshanDewmina/simurgh/internal/ipc"
	"github.com/RoshanDewmina/simurgh/internal/provisioner"
)

// TestXcodeBuildMCPStatusSurvivesSeparateCLIProcess is the CLI-level proof
// that R-WP6 review finding B2 is fixed.
//
// B2: cmd/simurgh's runXBMCPAction used to build a brand-new *xbmcp.Adapter
// (empty stdinHolders) on every single `simurgh integration xcodebuildmcp
// start|stop|status` invocation. The moment the `start` CLI process exited
// (immediately after printing status), the OS closed the stdin pipe fd it
// held, the spawned child saw stdin EOF, and a stdio-transport MCP server
// typically exits on that — defeating "start now, stop/status later as
// separate invocations", which is the documented public interface
// (docs/ARCHITECTURE.md §14). Every prior test was invisible to this because
// they all called Start/Stop within the same long-lived Go test process.
//
// The fix moves Start/Stop/Status into the daemon's single long-lived
// Adapter (internal/daemon.Deps.XBMCPAdapter), reached via
// integration.xcodebuildmcp.start|stop|status IPC methods the same way
// lease.* is already reached. This test proves it by running `start` as one
// real subprocess that is allowed to fully exit, then — as a SEPARATE
// subsequent subprocess — running `status` and asserting Running: true.
func TestXcodeBuildMCPStatusSurvivesSeparateCLIProcess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	client := provisioner.NewExecClient()
	runtimes, err := client.ListRuntimes(ctx)
	if err != nil {
		t.Fatalf("ListRuntimes: %v", err)
	}
	hasIOS := false
	for _, rt := range runtimes {
		if rt.IsAvailable && (rt.PlatformName == "iOS" || strings.HasPrefix(rt.Name, "iOS")) {
			hasIOS = true
			break
		}
	}
	if !hasIOS {
		t.Skip("no iOS runtime installed")
	}

	bin := buildRealSimurghBinaryForDaemonTest(t)
	home := shortTempDirForDaemonTest(t)
	fakeMCP := writeFakeMCPBinaryForDaemonTest(t)

	// SIMURGH_XBMCP_TEST_COMMAND is a test-only seam (cmd/simurgh/daemon.go)
	// that substitutes a fake long-lived child for the real
	// `npx xcodebuildmcp mcp` server, so this test doesn't depend on network
	// or the real npm package to prove process-ownership survives across CLI
	// process boundaries. It must be present in the env of whichever
	// subprocess first triggers the daemon auto-start (here, `acquire`),
	// since the detached daemon process inherits that subprocess's env.
	baseEnv := append(os.Environ(),
		"SIMURGH_HOME="+home,
		"SIMURGH_XBMCP_TEST_COMMAND="+fakeMCP,
		// This host's baseline free RAM regularly sits under the pressure
		// gate's 4 GiB default under normal daily load, which is unrelated
		// to what this test is proving (see cmd/simurgh/daemon.go).
		"SIMURGH_TEST_SKIP_PRESSURE=1",
	)

	run := func(args ...string) (string, int) {
		t.Helper()
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env = baseEnv
		out, runErr := cmd.CombinedOutput()
		code := 0
		if runErr != nil {
			exitErr, ok := runErr.(*exec.ExitError)
			if !ok {
				t.Fatalf("run %v: %v\n%s", args, runErr, out)
			}
			code = exitErr.ExitCode()
		}
		return string(out), code
	}

	// Real ACTIVE lease with a real device — the same acquire path any agent
	// uses. This subprocess also triggers the daemon auto-start.
	acquireOut, code := run("acquire", "--model", "iPhone 17 Pro", "--wait", "--ttl", "20m", "--json")
	if code != 0 {
		t.Fatalf("acquire failed exit=%d:\n%s", code, acquireOut)
	}
	var acquireWrap struct {
		Lease broker.Lease `json:"lease"`
	}
	if jsonErr := json.Unmarshal([]byte(acquireOut), &acquireWrap); jsonErr != nil {
		t.Fatalf("decode acquire: %v\nout:\n%s", jsonErr, acquireOut)
	}
	leaseID := acquireWrap.Lease.ID
	if leaseID == "" || acquireWrap.Lease.Device == nil || acquireWrap.Lease.Device.UDID == "" {
		t.Fatalf("acquire did not return an ACTIVE lease with a device: %s", acquireOut)
	}
	udid := acquireWrap.Lease.Device.UDID

	t.Cleanup(func() {
		_ = exec.Command(bin, "integration", "xcodebuildmcp", "stop", "--session", leaseID).Run()
		_ = exec.Command(bin, "release", leaseID).Run()
		_ = exec.Command("xcrun", "simctl", "shutdown", udid).Run()
		_ = exec.Command("xcrun", "simctl", "delete", udid).Run()
		killDaemonForTest(t, filepath.Join(home, "simurghd.sock"))
	})

	// Subprocess #1: start. Allowed to fully exit (CombinedOutput waits for
	// it) immediately after printing its status — this is the exact hazard
	// B2 describes.
	startOut, code := run("integration", "xcodebuildmcp", "start", "--session", leaseID, "--json")
	if code != 0 {
		t.Fatalf("start failed exit=%d:\n%s", code, startOut)
	}
	var startStatus struct {
		Running bool `json:"running"`
		PID     int  `json:"pid"`
	}
	if jsonErr := json.Unmarshal([]byte(startOut), &startStatus); jsonErr != nil {
		t.Fatalf("decode start status: %v\nout:\n%s", jsonErr, startOut)
	}
	if !startStatus.Running || startStatus.PID <= 0 {
		t.Fatalf("start did not report a running child: %s", startOut)
	}
	t.Logf("start subprocess exited; owned child pid=%d", startStatus.PID)

	// Subprocess #2: a SEPARATE, later invocation of `status`. This is the
	// entire point of the test — if B2 were not fixed, the child's stdin
	// pipe would have closed when subprocess #1 exited and this would report
	// Running: false.
	statusOut, code := run("integration", "xcodebuildmcp", "status", "--session", leaseID, "--json")
	if code != 0 {
		t.Fatalf("status failed exit=%d:\n%s", code, statusOut)
	}
	var status struct {
		Running bool `json:"running"`
		PID     int  `json:"pid"`
	}
	if jsonErr := json.Unmarshal([]byte(statusOut), &status); jsonErr != nil {
		t.Fatalf("decode status: %v\nout:\n%s", jsonErr, statusOut)
	}
	if !status.Running {
		t.Fatalf("B2 regression: a separate `status` subprocess reported Running: false after the `start` subprocess exited (want true). status output:\n%s", statusOut)
	}
	if status.PID != startStatus.PID {
		t.Fatalf("status pid=%d differs from start pid=%d — not the same owned child", status.PID, startStatus.PID)
	}

	// Stop (subprocess #3), then confirm a further separate status
	// subprocess sees it stopped — proving Stop still reaches the same
	// daemon-owned adapter, not a fresh one.
	stopOut, code := run("integration", "xcodebuildmcp", "stop", "--session", leaseID, "--json")
	if code != 0 {
		t.Fatalf("stop failed exit=%d:\n%s", code, stopOut)
	}
	afterStopOut, code := run("integration", "xcodebuildmcp", "status", "--session", leaseID, "--json")
	if code != 0 {
		t.Fatalf("status after stop failed exit=%d:\n%s", code, afterStopOut)
	}
	var afterStop struct {
		Running bool `json:"running"`
	}
	if jsonErr := json.Unmarshal([]byte(afterStopOut), &afterStop); jsonErr != nil {
		t.Fatalf("decode status after stop: %v\nout:\n%s", jsonErr, afterStopOut)
	}
	if afterStop.Running {
		t.Fatalf("status after stop still reports Running: true:\n%s", afterStopOut)
	}
}

// shortTempDirForDaemonTest returns a short path under /tmp. macOS sun_path
// is ~104 bytes; t.TempDir() paths are often too long for the Unix socket
// SIMURGH_HOME/simurghd.sock ends up at (see internal/ipc's shortTempDir).
func shortTempDirForDaemonTest(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "smg-xbmcp-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func buildRealSimurghBinaryForDaemonTest(t *testing.T) string {
	t.Helper()
	outDir := t.TempDir()
	out := filepath.Join(outDir, "simurgh")
	// -tags darwin_sim: this test relies on SIMURGH_XBMCP_TEST_COMMAND and
	// SIMURGH_TEST_SKIP_PRESSURE actually being read by the daemon it
	// launches (R-WP6 second-review Fix 2 moved those reads behind the
	// darwin_sim build tag — see cmd/simurgh/xbmcp_test_seams_darwin_sim.go).
	// A tagless build here would silently no-op both seams and this test
	// would hang/fail against the real npx package and the real pressure
	// gate instead of exercising the B2 regression it's meant to prove.
	cmd := exec.Command("go", "build", "-tags", "darwin_sim", "-o", out, ".")
	if outBytes, buildErr := cmd.CombinedOutput(); buildErr != nil {
		t.Fatalf("build simurgh: %v\n%s", buildErr, outBytes)
	}
	return out
}

func writeFakeMCPBinaryForDaemonTest(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-xbmcp")
	script := "#!/bin/bash\nset -euo pipefail\nexec cat >/dev/null\n"
	if writeErr := os.WriteFile(path, []byte(script), 0o755); writeErr != nil {
		t.Fatal(writeErr)
	}
	return path
}

// killDaemonForTest tears down the daemon started for this test's isolated
// SIMURGH_HOME so repeated test runs don't accumulate stray daemon
// processes. Best-effort: a failure here does not fail the test.
func killDaemonForTest(t *testing.T, socketPath string) {
	t.Helper()
	conn, dialErr := net.DialTimeout("unix", socketPath, 2*time.Second)
	if dialErr != nil {
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	req := ipc.Request{V: ipc.ProtocolVersion, ID: "cleanup-ping", Method: ipc.MethodDaemonPing}
	if encErr := ipc.Encode(conn, req); encErr != nil {
		return
	}
	resp, decErr := ipc.DecodeResponse(conn)
	if decErr != nil || !resp.OK || resp.Error != nil {
		return
	}
	var result struct {
		PID int `json:"pid"`
	}
	if jsonErr := json.Unmarshal(resp.Result, &result); jsonErr != nil || result.PID <= 0 {
		return
	}
	proc, procErr := os.FindProcess(result.PID)
	if procErr != nil {
		return
	}
	_ = proc.Signal(syscall.SIGTERM)
	time.Sleep(200 * time.Millisecond)
	_ = proc.Signal(syscall.SIGKILL)
}
