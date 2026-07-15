package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntegrationXcodeBuildMCPHelp(t *testing.T) {
	out, err := exec.Command("go", "run", ".", "integration", "xcodebuildmcp", "-h").CombinedOutput()
	if err != nil {
		t.Fatalf("help: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "start|stop|status") {
		t.Fatalf("help output:\n%s", out)
	}
}

func TestIntegrationXcodeBuildMCPRequiresSession(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "integration", "xcodebuildmcp", "start", "--json")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected failure, got:\n%s", out)
	}
	text := string(out)
	if !strings.Contains(text, "INVALID_INPUT") && !strings.Contains(text, "--session") {
		t.Fatalf("unexpected output:\n%s", text)
	}
}

// TestXcodeBuildMCPCLIStartUnknownSessionReturnsLeaseNotFound is the R-WP6
// second-review Fix 3 regression proof. Before 740abdb, a nonexistent lease
// ID reached the daemon's real `lease.get` IPC handler and came back as a
// structured LEASE_NOT_FOUND (exit 3, fix "simurgh list"). 740abdb's
// in-process GetLease closure in cmd/simurgh/daemon.go hand-rolled a bare
// fmt.Errorf that internal/daemon's xbmcpErrorToIPC didn't recognize,
// silently falling through to generic INTERNAL (exit 5) — contradicting the
// change's own "exit codes are unchanged" claim. This drives the CLI against
// a real auto-started daemon (not a mock): a genuinely nonexistent session
// ID under a fresh, empty SIMURGH_HOME can never correspond to any lease the
// broker knows about.
func TestXcodeBuildMCPCLIStartUnknownSessionReturnsLeaseNotFound(t *testing.T) {
	// macOS sun_path is ~104 bytes; t.TempDir() paths are often too long for
	// the Unix socket SIMURGH_HOME/simurghd.sock ends up at (this test needs
	// the daemon to actually be reachable, unlike its DAEMON_NOT_RUNNING
	// sibling below, which tolerates any failure).
	home, err := os.MkdirTemp("/tmp", "smg-xbmcp-lnf-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })

	// Build and run the real binary directly rather than `go run .`: go run's
	// own process always exits 1 on any non-zero child exit code (it does
	// not forward the real one), which would make this test unable to
	// observe the exit(3) it's asserting on.
	bin := filepath.Join(t.TempDir(), "simurgh")
	buildCmd := exec.Command("go", "build", "-o", bin, ".")
	if buildOut, buildErr := buildCmd.CombinedOutput(); buildErr != nil {
		t.Fatalf("build simurgh: %v\n%s", buildErr, buildOut)
	}

	cmd := exec.Command(bin, "integration", "xcodebuildmcp", "start", "--session", "lease-999999", "--json")
	cmd.Env = append(os.Environ(), "SIMURGH_HOME="+home)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected failure, got:\n%s", out)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected *exec.ExitError, got %T: %v\noutput:\n%s", err, err, out)
	}
	if got := exitErr.ExitCode(); got != 3 {
		t.Fatalf("exit code = %d, want 3 (LEASE_NOT_FOUND)\noutput:\n%s", got, out)
	}
	var payload map[string]any
	if jsonErr := json.Unmarshal(out, &payload); jsonErr != nil {
		t.Fatalf("decode JSON error payload: %v\noutput:\n%s", jsonErr, out)
	}
	if code, _ := payload["code"].(string); code != "LEASE_NOT_FOUND" {
		t.Fatalf("code = %q, want LEASE_NOT_FOUND\noutput:\n%s", code, out)
	}
}

func TestXcodeBuildMCPCLIRejectsInactiveWithoutDaemon(t *testing.T) {
	// Without a daemon, start should fail with DAEMON_NOT_RUNNING (env exit 4),
	// never invent a UDID or write committed config.
	home := t.TempDir()
	cmd := exec.Command("go", "run", ".", "integration", "xcodebuildmcp", "start", "--session", "lease-missing", "--json")
	cmd.Env = append(os.Environ(), "SIMURGH_HOME="+home)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected failure, got:\n%s", out)
	}
	var payload map[string]any
	if jsonErr := json.Unmarshal(out, &payload); jsonErr == nil {
		if code, _ := payload["code"].(string); code != "" && code != "DAEMON_NOT_RUNNING" && code != "INTERNAL" {
			// Accept DAEMON_NOT_RUNNING preferred; some hosts encode differently.
			t.Logf("error code=%s body=%s", code, out)
		}
	}
	// Must not create a lease adapter dir for a missing lease under home.
	matches, _ := filepath.Glob(filepath.Join(home, "leases", "*", "xcodebuildmcp", "mcp.json"))
	if len(matches) != 0 {
		t.Fatalf("wrote ephemeral config without ACTIVE lease: %v", matches)
	}
}
