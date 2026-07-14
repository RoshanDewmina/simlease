package xcodebuildmcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
)

func fakeXBMCP(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-xbmcp")
	// Dump env under the lease adapter dir (derived from injected isolation env),
	// then hold stdin open until Stop closes the pipe / kills the process group.
	script := `#!/bin/bash
set -euo pipefail
out_dir="$(dirname "${SIMURGH_DERIVED_DATA}")/xcodebuildmcp"
mkdir -p "$out_dir"
env | sort >"$out_dir/child.env"
echo "fake-xbmcp pid=$$ lease=${SIMURGH_LEASE_ID:-}" >>"$out_dir/adapter.log"
exec cat >/dev/null
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake: %v", err)
	}
	return path
}

func TestXcodeBuildMCPStartStopOwnership(t *testing.T) {
	bin := fakeXBMCP(t)
	lease := activeLease("lease-901", "AAAAAAAA-0000-0000-0000-000000000001")

	a := NewAdapter(t.TempDir(), func(_ context.Context, id string) (broker.Lease, error) {
		if id != lease.ID {
			return broker.Lease{}, os.ErrNotExist
		}
		return lease, nil
	})
	a.Command = []string{bin}
	a.Version = "test-fake"

	st, err := a.Start(context.Background(), "lease-901")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !st.Running || st.PID <= 0 {
		t.Fatalf("status=%+v", st)
	}
	if !processAlive(st.PID) {
		t.Fatalf("child pid %d not alive after start", st.PID)
	}
	paths := AdapterDir(filepath.Join(a.LeasesRoot, "lease-901"))
	meta, err := readPIDMeta(paths.PIDMeta)
	if err != nil {
		t.Fatalf("pid meta: %v", err)
	}
	if meta.LeaseID != "lease-901" || meta.PID != st.PID {
		t.Fatalf("meta=%+v", meta)
	}

	envPath := filepath.Join(paths.Root, "child.env")
	raw := waitFile(t, envPath, 2*time.Second)
	text := string(raw)
	for _, want := range []string{
		"SIMURGH_LEASE_ID=lease-901",
		"SIMURGH_UDID=AAAAAAAA-0000-0000-0000-000000000001",
		"XCODEBUILDMCP_SIMULATOR_ID=AAAAAAAA-0000-0000-0000-000000000001",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("env dump missing %q\n%s", want, text)
		}
	}

	pid := st.PID
	if err := a.Stop(context.Background(), "lease-901"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitDead(t, pid, 2*time.Second)
	if _, err := os.Stat(paths.PIDMeta); !os.IsNotExist(err) {
		t.Fatalf("pid meta should be removed after Stop, err=%v", err)
	}
	st2, err := a.Status(context.Background(), "lease-901")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st2.Running {
		t.Fatalf("status still running after stop")
	}
}

func TestXcodeBuildMCPTwoSessionAttribution(t *testing.T) {
	bin := fakeXBMCP(t)
	leaseA := activeLease("lease-101", "AAAAAAAA-0000-0000-0000-0000000000AA")
	leaseB := activeLease("lease-102", "BBBBBBBB-0000-0000-0000-0000000000BB")
	root := t.TempDir()
	leases := map[string]broker.Lease{"lease-101": leaseA, "lease-102": leaseB}
	get := func(_ context.Context, id string) (broker.Lease, error) {
		return leases[id], nil
	}
	mk := func() *Adapter {
		a := NewAdapter(root, get)
		a.Command = []string{bin}
		a.Version = "test-fake"
		return a
	}
	a1, a2 := mk(), mk()
	stA, err := a1.Start(context.Background(), "lease-101")
	if err != nil {
		t.Fatalf("Start A: %v", err)
	}
	stB, err := a2.Start(context.Background(), "lease-102")
	if err != nil {
		t.Fatalf("Start B: %v", err)
	}
	t.Cleanup(func() {
		_ = a1.Stop(context.Background(), "lease-101")
		_ = a2.Stop(context.Background(), "lease-102")
	})

	if stA.PID == stB.PID {
		t.Fatalf("sessions share pid %d", stA.PID)
	}
	if stA.Config == stB.Config {
		t.Fatalf("sessions share config path")
	}

	textA := string(waitFile(t, filepath.Join(a1.LeasesRoot, "lease-101", adapterSubdir, "child.env"), 2*time.Second))
	textB := string(waitFile(t, filepath.Join(a2.LeasesRoot, "lease-102", adapterSubdir, "child.env"), 2*time.Second))
	if !strings.Contains(textA, "SIMURGH_LEASE_ID=lease-101") || !strings.Contains(textA, "SIMURGH_UDID=AAAAAAAA-0000-0000-0000-0000000000AA") {
		t.Fatalf("session A misattributed:\n%s", textA)
	}
	if !strings.Contains(textB, "SIMURGH_LEASE_ID=lease-102") || !strings.Contains(textB, "SIMURGH_UDID=BBBBBBBB-0000-0000-0000-0000000000BB") {
		t.Fatalf("session B misattributed:\n%s", textB)
	}
	if strings.Contains(textA, "lease-102") || strings.Contains(textB, "lease-101") {
		t.Fatalf("cross-session env leakage")
	}

	cfgA, _ := os.ReadFile(stA.Config)
	cfgB, _ := os.ReadFile(stB.Config)
	if !strings.Contains(string(cfgA), "AAAAAAAA-0000-0000-0000-0000000000AA") {
		t.Fatalf("config A missing UDID")
	}
	if !strings.Contains(string(cfgB), "BBBBBBBB-0000-0000-0000-0000000000BB") {
		t.Fatalf("config B missing UDID")
	}
	if strings.Contains(string(cfgA), "BBBBBBBB-0000-0000-0000-0000000000BB") {
		t.Fatalf("config A contains B UDID")
	}
}

func TestXcodeBuildMCPCleanupIsolation(t *testing.T) {
	bin := fakeXBMCP(t)
	leaseA := activeLease("lease-101", "AAAAAAAA-0000-0000-0000-0000000000AA")
	leaseB := activeLease("lease-102", "BBBBBBBB-0000-0000-0000-0000000000BB")
	root := t.TempDir()
	leases := map[string]broker.Lease{"lease-101": leaseA, "lease-102": leaseB}
	get := func(_ context.Context, id string) (broker.Lease, error) { return leases[id], nil }

	mk := func() *Adapter {
		a := NewAdapter(root, get)
		a.Command = []string{bin}
		a.Version = "test-fake"
		return a
	}
	a1, a2 := mk(), mk()
	stA, err := a1.Start(context.Background(), "lease-101")
	if err != nil {
		t.Fatalf("Start A: %v", err)
	}
	stB, err := a2.Start(context.Background(), "lease-102")
	if err != nil {
		t.Fatalf("Start B: %v", err)
	}

	if err := a1.Stop(context.Background(), "lease-101"); err != nil {
		t.Fatalf("Stop A: %v", err)
	}
	waitDead(t, stA.PID, 2*time.Second)
	if !processAlive(stB.PID) {
		t.Fatalf("B was killed when stopping A — cleanup not isolated")
	}
	stB2, err := a2.Status(context.Background(), "lease-102")
	if err != nil || !stB2.Running {
		t.Fatalf("B status after A stop: %+v err=%v", stB2, err)
	}

	if err := a1.Stop(context.Background(), "lease-101"); err == nil {
		t.Fatalf("expected error stopping already-stopped A")
	}
	if !processAlive(stB.PID) {
		t.Fatalf("B died on second Stop A")
	}

	if err := a2.Stop(context.Background(), "lease-102"); err != nil {
		t.Fatalf("Stop B: %v", err)
	}
}

func TestXcodeBuildMCPStopRejectsForeignPID(t *testing.T) {
	root := t.TempDir()
	paths := AdapterDir(filepath.Join(root, "lease-999"))
	if err := os.MkdirAll(paths.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	meta := PIDMeta{
		LeaseID: "lease-999",
		PID:     1 << 22,
		PGID:    1 << 22,
	}
	if err := writePIDMeta(paths.PIDMeta, meta); err != nil {
		t.Fatal(err)
	}
	a := NewAdapter(root, nil)
	_ = a.Stop(context.Background(), "lease-999")
	if !processAlive(cmd.Process.Pid) {
		t.Fatalf("Stop killed unrelated sleeper pid %d", cmd.Process.Pid)
	}
}

func TestXcodeBuildMCPStopAndStatusRejectMalformedSessionID(t *testing.T) {
	root := t.TempDir()
	// A sentinel outside root: if Stop/Status ever built a path from an
	// unvalidated sessionID and escaped root, this is what a traversal
	// attempt would target.
	victimDir := filepath.Join(filepath.Dir(root), "xbmcp-victim-"+filepath.Base(root))
	if err := os.MkdirAll(victimDir, 0o700); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(victimDir)
	victimFile := filepath.Join(victimDir, "adapter.pid.json")
	if err := os.WriteFile(victimFile, []byte(`{"leaseId":"sentinel","pid":1}`), 0o600); err != nil {
		t.Fatal(err)
	}

	a := NewAdapter(root, nil)
	for _, bad := range []string{
		"../" + filepath.Base(victimDir),
		"../../etc",
		"lease-1/../../etc",
		"",
		"lease-abc",
		"lease-1.5",
	} {
		if err := a.Stop(context.Background(), bad); err == nil {
			t.Fatalf("Stop(%q) = nil error, want rejection of malformed session id", bad)
		}
		st, err := a.Status(context.Background(), bad)
		if err != nil {
			t.Fatalf("Status(%q) unexpected error: %v", bad, err)
		}
		if st.Running {
			t.Fatalf("Status(%q) = Running true, want false for malformed session id", bad)
		}
	}

	if _, err := os.ReadFile(victimFile); err != nil {
		t.Fatalf("victim file outside leases root was modified/removed: %v", err)
	}
}

func waitFile(t *testing.T, path string, d time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil && len(raw) > 0 {
			return raw
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", path)
	return nil
}

func waitDead(t *testing.T, pid int, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if processAlive(pid) {
		t.Fatalf("pid %d still alive", pid)
	}
}
