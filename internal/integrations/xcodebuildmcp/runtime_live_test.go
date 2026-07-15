//go:build darwin_sim

package xcodebuildmcp_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
	xbmcp "github.com/RoshanDewmina/simurgh/internal/integrations/xcodebuildmcp"
	"github.com/RoshanDewmina/simurgh/internal/isolator"
	"github.com/RoshanDewmina/simurgh/internal/provisioner"
)

// TestXcodeBuildMCPRuntimeTwoSessionAttribution is the bounded R-WP6 live
// validation: two real isolated clones, adapter start/stop ownership, and the
// smallest XcodeBuildMCP-driven build that proves UDID + DerivedData attribution.
// Not a timed benchmark — no latency claims.
func TestXcodeBuildMCPRuntimeTwoSessionAttribution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not available")
	}
	verOut, err := exec.CommandContext(ctx, "npx", "--yes", "xcodebuildmcp@"+xbmcp.DefaultPinnedVersion, "--version").Output()
	if err != nil {
		t.Fatalf("xcodebuildmcp version: %v", err)
	}
	version := strings.TrimSpace(string(verOut))
	t.Logf("xcodebuildmcp version=%s", version)

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

	repoRoot := findRepoRoot(t)
	project := filepath.Join(repoRoot, "test", "fixtures", "SampleApp", "SampleApp.xcodeproj")
	if _, err := os.Stat(project); err != nil {
		t.Fatalf("SampleApp fixture missing: %v", err)
	}

	leasesRoot := t.TempDir()
	prov := provisioner.New(client)
	b := broker.New(broker.BrokerOptions{
		Capacity:    2,
		Provisioner: prov,
		Clock:       time.Now,
		PressureOK:  func() (bool, string) { return true, "" },
	})

	acquire := func(label string) broker.Lease {
		t.Helper()
		lease, err := b.Acquire(ctx, broker.AcquireRequest{
			Spec:          broker.DeviceSpec{Platform: "iOS", Model: "iPhone 17 Pro"},
			Labels:        map[string]string{"rwp6": label},
			TTL:           20 * time.Minute,
			Wait:          true,
			ReuseIfExists: false,
			Owner:         broker.Owner{PID: os.Getpid(), Agent: "r-wp6-runtime", SessionID: label},
		})
		if err != nil {
			t.Fatalf("acquire %s: %v", label, err)
		}
		if lease.Device == nil || lease.Device.UDID == "" {
			t.Fatalf("acquire %s missing device", label)
		}
		dirs, err := isolator.EnsureLeaseDir(leasesRoot, lease.ID)
		if err != nil {
			t.Fatalf("EnsureLeaseDir %s: %v", label, err)
		}
		lease.Env = isolator.BuildEnv(dirs, lease.ID, lease.Device.UDID)
		t.Cleanup(func() {
			_, _ = b.Release(context.Background(), lease.ID)
			_ = exec.Command("xcrun", "simctl", "shutdown", lease.Device.UDID).Run()
			_ = exec.Command("xcrun", "simctl", "delete", lease.Device.UDID).Run()
		})
		return lease
	}

	leaseA := acquire("session-A")
	leaseB := acquire("session-B")
	if leaseA.Device.UDID == leaseB.Device.UDID {
		t.Fatalf("leases share UDID %s", leaseA.Device.UDID)
	}

	leases := map[string]broker.Lease{leaseA.ID: leaseA, leaseB.ID: leaseB}
	get := func(_ context.Context, id string) (broker.Lease, error) { return leases[id], nil }

	// Fake long-lived MCP child for ownership; real attribution uses CLI build.
	fakeBin := writeFakeMCP(t)
	adapter := xbmcp.NewAdapter(leasesRoot, get)
	adapter.Command = []string{fakeBin}
	adapter.Version = version

	stA, err := adapter.Start(ctx, leaseA.ID)
	if err != nil {
		t.Fatalf("adapter start A: %v", err)
	}
	stB, err := adapter.Start(ctx, leaseB.ID)
	if err != nil {
		t.Fatalf("adapter start B: %v", err)
	}
	t.Cleanup(func() {
		_ = adapter.Stop(context.Background(), leaseA.ID)
		_ = adapter.Stop(context.Background(), leaseB.ID)
	})

	bindA, err := xbmcp.BuildBindings(leaseA, leasesRoot)
	if err != nil {
		t.Fatalf("bindings A: %v", err)
	}
	bindB, err := xbmcp.BuildBindings(leaseB, leasesRoot)
	if err != nil {
		t.Fatalf("bindings B: %v", err)
	}

	globalDD := filepath.Join(os.Getenv("HOME"), "Library", "Developer", "Xcode", "DerivedData")
	beforeGlobal := countSampleAppDerivedData(t, globalDD)

	runBuild := func(label string, b xbmcp.Bindings) {
		t.Helper()
		args := []string{"--yes", "xcodebuildmcp@" + xbmcp.DefaultPinnedVersion, "simulator", "build",
			"--project-path", project,
			"--scheme", "SampleApp",
			"--configuration", "Debug",
			"--output", "json",
		}
		args = append(args, b.CLIFlags...)
		cmd := exec.CommandContext(ctx, "npx", args...)
		cmd.Env = append(os.Environ(), envSlice(b.Env)...)
		cmd.Dir = b.Dirs.Root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s xcodebuildmcp build: %v\n%s", label, err, out)
		}
		entries, _ := os.ReadDir(b.Dirs.DerivedData)
		if len(entries) == 0 {
			t.Fatalf("%s DerivedData empty under lease root %s", label, b.Dirs.DerivedData)
		}
		t.Logf("%s build ok dd_entries=%d udid=%s", label, len(entries), b.UDID)
	}

	runBuild("A", bindA)
	runBuild("B", bindB)

	afterGlobal := countSampleAppDerivedData(t, globalDD)
	if afterGlobal > beforeGlobal {
		t.Fatalf("unexpected global SampleApp DerivedData growth: before=%d after=%d", beforeGlobal, afterGlobal)
	}

	if err := adapter.Stop(ctx, leaseA.ID); err != nil {
		t.Fatalf("stop A: %v", err)
	}
	stB2, err := adapter.Status(ctx, leaseB.ID)
	if err != nil || !stB2.Running {
		t.Fatalf("B should remain running after A stop: %+v err=%v", stB2, err)
	}
	if !processAlivePID(stB.PID) {
		t.Fatalf("B child pid %d died when A stopped", stB.PID)
	}
	if processAlivePID(stA.PID) {
		t.Fatalf("A child pid %d still alive after stop", stA.PID)
	}

	// B's DerivedData must still be intact after A cleanup.
	entriesB, _ := os.ReadDir(bindB.Dirs.DerivedData)
	if len(entriesB) == 0 {
		t.Fatalf("B DerivedData cleared after A stop")
	}

	evidence := map[string]any{
		"check":                   "r-wp6-xcodebuildmcp-runtime",
		"xcodebuildmcp_version":   version,
		"lease_a":                 leaseA.ID,
		"lease_b":                 leaseB.ID,
		"udid_a":                  leaseA.Device.UDID,
		"udid_b":                  leaseB.Device.UDID,
		"dd_a":                    bindA.Dirs.DerivedData,
		"dd_b":                    bindB.Dirs.DerivedData,
		"adapter_pid_a":           stA.PID,
		"adapter_pid_b":           stB.PID,
		"global_sampleapp_before": beforeGlobal,
		"global_sampleapp_after":  afterGlobal,
		"no_global_pollution":     afterGlobal <= beforeGlobal,
		"stop_a_leaves_b_valid":   true,
		"verdict":                 "composes",
	}
	raw, _ := json.MarshalIndent(evidence, "", "  ")
	t.Logf("evidence:\n%s", raw)
}

func writeFakeMCP(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-xbmcp")
	script := "#!/bin/bash\nset -euo pipefail\nexec cat >/dev/null\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func envSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

func countSampleAppDerivedData(t *testing.T, globalDD string) int {
	t.Helper()
	entries, err := os.ReadDir(globalDD)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "SampleApp-") {
			n++
		}
	}
	return n
}

func processAlivePID(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func findRepoRoot(t *testing.T) string {
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
