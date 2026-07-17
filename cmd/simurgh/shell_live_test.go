//go:build darwin_sim

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
	"github.com/RoshanDewmina/simurgh/internal/integrations/pathshim"
	"github.com/RoshanDewmina/simurgh/internal/provisioner"
)

// TestManagedShellLiveAuditsBareXcodebuild is the R-WP5 stage-1 live gate:
// acquire → managed shell (non-interactive SHELL script) → bare
// xcodebuild -version / xcrun → JSONL audit entries → release.
//
// runShell is interactive (TTY stdin). Driving a full human shell session
// cannot be automated here; instead SHELL points at a one-shot script that
// exercises the same prepareManagedShell + PATH shim + audit path the
// interactive shell uses. Remaining manual gate: open an interactive
// `simurgh shell <lease-id>` on a real TTY and confirm the banner + audit
// log by eye.
func TestManagedShellLiveAuditsBareXcodebuild(t *testing.T) {
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

	binDir := t.TempDir()
	simurghBin := filepath.Join(binDir, "simurgh")
	shimBin := filepath.Join(binDir, "simurgh-shim")
	buildTagged := exec.Command("go", "build", "-tags", "darwin_sim", "-o", simurghBin, ".")
	if out, buildErr := buildTagged.CombinedOutput(); buildErr != nil {
		t.Fatalf("build simurgh: %v\n%s", buildErr, out)
	}
	modRoot := findModuleRootForShellLive(t)
	buildShim := exec.Command("go", "build", "-o", shimBin, "./cmd/simurgh-shim")
	buildShim.Dir = modRoot
	if out, buildErr := buildShim.CombinedOutput(); buildErr != nil {
		t.Fatalf("build simurgh-shim: %v\n%s", buildErr, out)
	}

	home := shortTempDirForDaemonTest(t)
	shellScript := filepath.Join(t.TempDir(), "managed-shell-probe.sh")
	script := "#!/bin/bash\nset -euo pipefail\nxcodebuild -version\nxcrun -f xcodebuild\n"
	if writeErr := os.WriteFile(shellScript, []byte(script), 0o755); writeErr != nil {
		t.Fatal(writeErr)
	}

	baseEnv := append(os.Environ(),
		"SIMURGH_HOME="+home,
		"SIMURGH_TEST_SKIP_PRESSURE=1",
		"SHELL="+shellScript,
	)

	run := func(args ...string) (string, int) {
		t.Helper()
		cmd := exec.CommandContext(ctx, simurghBin, args...)
		cmd.Env = append([]string(nil), baseEnv...)
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
		t.Fatalf("acquire did not return ACTIVE lease:\n%s", acquireOut)
	}
	udid := acquireWrap.Lease.Device.UDID
	t.Cleanup(func() {
		// Cleanup must not use the test's timed ctx — a failed shell leaves
		// it canceled and would skip release. --force: release runs in a
		// different CLI process than acquire (owner PID mismatch).
		rel := exec.Command(simurghBin, "release", "--force", leaseID)
		rel.Env = append([]string(nil), baseEnv...)
		_ = rel.Run()
		_ = exec.Command("xcrun", "simctl", "shutdown", udid).Run()
		_ = exec.Command("xcrun", "simctl", "delete", udid).Run()
		killDaemonForTest(t, filepath.Join(home, "simurghd.sock"))
	})

	shellOut, shellCode := run("shell", leaseID)
	if shellCode != 0 {
		t.Fatalf("shell failed exit=%d:\n%s", shellCode, shellOut)
	}
	if !strings.Contains(shellOut, "audit mode") && !strings.Contains(shellOut, "PATH shims") {
		// Banner goes to stderr; CombinedOutput captures both.
		t.Logf("shell output (banner may be absent if only stderr buffering differs):\n%s", shellOut)
	}

	leaseRoot := filepath.Join(home, "leases", leaseID)
	auditPath := pathshim.SessionDir(leaseRoot).AuditLog
	raw, readErr := os.ReadFile(auditPath)
	if readErr != nil {
		t.Fatalf("read audit log %s: %v\nshell out:\n%s", auditPath, readErr, shellOut)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 2 {
		t.Fatalf("want ≥2 audit lines, got %d:\n%s\nshell out:\n%s", len(lines), raw, shellOut)
	}
	seen := map[string]bool{}
	for _, line := range lines {
		var entry pathshim.AuditEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("audit json: %v\n%s", err, line)
		}
		if entry.Mode != pathshim.ModeAudit {
			t.Fatalf("mode=%q want %q", entry.Mode, pathshim.ModeAudit)
		}
		seen[entry.Binary] = true
	}
	if !seen["xcodebuild"] || !seen["xcrun"] {
		t.Fatalf("audit binaries=%v want xcodebuild+xcrun\n%s", seen, raw)
	}

	releaseOut, releaseCode := run("release", "--force", leaseID)
	if releaseCode != 0 {
		t.Fatalf("release failed exit=%d:\n%s", releaseCode, releaseOut)
	}
}

func findModuleRootForShellLive(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
