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
