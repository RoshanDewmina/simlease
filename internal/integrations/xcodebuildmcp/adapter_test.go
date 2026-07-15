package xcodebuildmcp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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

// TestXcodeBuildMCPStdinHoldersConcurrentAccessRace proves stdinHolders is
// safe for the concurrency shape the daemon actually exercises now that
// R-WP6 review finding B2 replaced "one throwaway *Adapter per CLI process"
// with "one long-lived *Adapter shared across every IPC connection/goroutine
// for the daemon's whole lifetime". Before B2, this map was never touched by
// more than one goroutine at a time, so the missing mutex was safe by
// accident; after B2, concurrent Start/Stop calls for different (or the
// same) sessions race on it. A second independent review reproduced
// `WARNING: DATA RACE` on both the Start write and the Stop read/delete with
// a throwaway (deleted) -race repro; this is the permanent version — it must
// fail under `go test -race` on the pre-mutex code and pass once
// Adapter.stdinMu guards every stdinHolders access.
func TestXcodeBuildMCPStdinHoldersConcurrentAccessRace(t *testing.T) {
	bin := fakeXBMCP(t)
	root := t.TempDir()

	const sessions = 8
	ids := make([]string, sessions)
	leases := make(map[string]broker.Lease, sessions)
	for i := 0; i < sessions; i++ {
		// safety.ValidateLeasePath requires ^lease-[0-9]+$ — Stop/Status
		// reject anything else before ever touching stdinHolders.
		id := fmt.Sprintf("lease-9%03d", i)
		ids[i] = id
		leases[id] = activeLease(id, fmt.Sprintf("AAAAAAAA-0000-0000-0000-%012d", i))
	}
	get := func(_ context.Context, id string) (broker.Lease, error) {
		lease, ok := leases[id]
		if !ok {
			return broker.Lease{}, fmt.Errorf("unknown lease %s", id)
		}
		return lease, nil
	}

	// One shared Adapter, as the daemon constructs exactly once at startup —
	// this is the concurrency shape B2 introduced.
	a := NewAdapter(root, get)
	a.Command = []string{bin}
	a.Version = "test-fake"

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan string, sessions*2)
	pids := make(chan int, sessions)

	for _, id := range ids {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release all goroutines at once to maximize overlap
			st, err := a.Start(context.Background(), id)
			if err != nil {
				errs <- fmt.Sprintf("Start(%s): %v", id, err)
				return
			}
			pids <- st.PID
			if err := a.Stop(context.Background(), id); err != nil {
				errs <- fmt.Sprintf("Stop(%s): %v", id, err)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	close(pids)

	for msg := range errs {
		t.Error(msg)
	}
	// A corrupted stdinHolders map (the pre-fix failure mode) can also
	// manifest as a lost/duplicated holder entry that leaves a child running
	// after Stop reports success — check every started child was reaped.
	deadline := time.Now().Add(3 * time.Second)
	for pid := range pids {
		for processAlive(pid) && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if processAlive(pid) {
			t.Errorf("pid %d still alive after concurrent Stop", pid)
		}
	}
}

// TestXcodeBuildMCPStartConcurrentSameSessionRace proves the R-WP6
// third-review TOCTOU finding is closed: many goroutines calling Start
// concurrently for the SAME sessionID must never let more than one of them
// pass the "already running" guard and spawn a child. Before the
// sessionLocks fix, the read (readPIDMeta/processAlive) — spawn (cmd.Start)
// — write (writePIDMeta) sequence was not serialized per-session, so two
// overlapping Starts could both observe "not running" and both spawn,
// silently orphaning one child. This test must reliably show exactly one
// winner, exactly one live child, and exactly one line in the fake binary's
// shared log — run with -race and -count>1, since a race this narrow can
// pass on scheduling luck alone on a single run.
func TestXcodeBuildMCPStartConcurrentSameSessionRace(t *testing.T) {
	const goroutines = 25
	bin := fakeXBMCP(t)
	lease := activeLease("lease-501", "AAAAAAAA-0000-0000-0000-000000000501")
	root := t.TempDir()
	get := func(_ context.Context, id string) (broker.Lease, error) {
		if id != lease.ID {
			return broker.Lease{}, os.ErrNotExist
		}
		return lease, nil
	}
	a := NewAdapter(root, get)
	a.Command = []string{bin}
	a.Version = "test-fake"

	type result struct {
		st  Status
		err error
	}
	results := make([]result, goroutines)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release all goroutines at once to maximize overlap
			st, err := a.Start(context.Background(), "lease-501")
			results[i] = result{st: st, err: err}
		}()
	}
	close(start)
	wg.Wait()

	var successes, failures []result
	for _, r := range results {
		if r.err == nil {
			successes = append(successes, r)
		} else {
			failures = append(failures, r)
		}
	}
	if len(successes) != 1 {
		t.Fatalf("expected exactly 1 successful Start out of %d, got %d: %+v", goroutines, len(successes), results)
	}
	if len(failures) != goroutines-1 {
		t.Fatalf("expected %d failed Starts, got %d", goroutines-1, len(failures))
	}
	winner := successes[0]
	if !winner.st.Running || winner.st.PID <= 0 {
		t.Fatalf("winner status=%+v", winner.st)
	}
	for _, f := range failures {
		if !strings.Contains(f.err.Error(), "already running") {
			t.Fatalf("unexpected failure error (want \"already running\"): %v", f.err)
		}
	}

	if !processAlive(winner.st.PID) {
		t.Fatalf("winner pid %d not alive", winner.st.PID)
	}

	paths := AdapterDir(filepath.Join(root, "lease-501"))
	meta, err := readPIDMeta(paths.PIDMeta)
	if err != nil {
		t.Fatalf("pid meta: %v", err)
	}
	if meta.PID != winner.st.PID || meta.LeaseID != "lease-501" {
		t.Fatalf("on-disk meta=%+v does not match winner pid=%d", meta, winner.st.PID)
	}

	// Confirm only one child process ever executed the fake binary — if the
	// TOCTOU race were unfixed, two goroutines could pass the "not already
	// running" guard and both spawn, orphaning one.
	logPath := filepath.Join(paths.Root, "adapter.log")
	raw := waitFile(t, logPath, 2*time.Second)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 child process log line (one spawned child), got %d:\n%s", len(lines), raw)
	}
	if !strings.Contains(lines[0], fmt.Sprintf("pid=%d", winner.st.PID)) {
		t.Fatalf("log line does not attribute the winner pid %d: %s", winner.st.PID, lines[0])
	}

	if err := a.Stop(context.Background(), "lease-501"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitDead(t, winner.st.PID, 2*time.Second)
}

// TestXcodeBuildMCPStartRacesStopSameSession attempts to prove Start and Stop
// racing each other for the SAME sessionID never leaves an inconsistent or
// orphaned state. Making this fully deterministic (asserting a specific
// winner) is not possible — Start and Stop are legitimately racing, and
// either a fresh Start or a Stop may legitimately "win" the lock for any
// given tick — so instead this asserts the invariant the sessionLocks fix is
// supposed to guarantee regardless of interleaving: at the end, the on-disk
// Status must exactly match reality (a PID is reported running iff that
// exact PID is alive), and every other child ever spawned during the run
// must be dead — i.e. no run can end with two live children for one session,
// nor with a live child that Status doesn't know about (an orphan).
func TestXcodeBuildMCPStartRacesStopSameSession(t *testing.T) {
	bin := fakeXBMCP(t)
	lease := activeLease("lease-777", "AAAAAAAA-0000-0000-0000-000000000777")
	root := t.TempDir()
	get := func(_ context.Context, id string) (broker.Lease, error) {
		if id != lease.ID {
			return broker.Lease{}, os.ErrNotExist
		}
		return lease, nil
	}
	a := NewAdapter(root, get)
	a.Command = []string{bin}
	a.Version = "test-fake"

	// Prime one running instance so the very first ticks already have a live
	// child for Stop calls to race against, not just after some Start
	// eventually wins the lock for the first time.
	st0, err := a.Start(context.Background(), "lease-777")
	if err != nil {
		t.Fatalf("initial Start: %v", err)
	}

	const goroutines = 20
	var wg sync.WaitGroup
	start := make(chan struct{})
	var mu sync.Mutex
	var startedPIDs []int
	for i := 0; i < goroutines; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if i%2 == 0 {
				st, err := a.Start(context.Background(), "lease-777")
				if err == nil {
					mu.Lock()
					startedPIDs = append(startedPIDs, st.PID)
					mu.Unlock()
				}
			} else {
				_ = a.Stop(context.Background(), "lease-777")
			}
		}()
	}
	close(start)
	wg.Wait()

	mu.Lock()
	allPIDs := append([]int{st0.PID}, startedPIDs...)
	mu.Unlock()

	finalSt, err := a.Status(context.Background(), "lease-777")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	aliveCount := 0
	for _, pid := range allPIDs {
		if processAlive(pid) {
			aliveCount++
			if !finalSt.Running || finalSt.PID != pid {
				t.Fatalf("pid %d alive but final status=%+v does not attribute it (orphan or misattribution)", pid, finalSt)
			}
		}
	}
	if finalSt.Running {
		if aliveCount != 1 {
			t.Fatalf("status reports running but aliveCount=%d (want exactly 1), pids=%v, status=%+v", aliveCount, allPIDs, finalSt)
		}
	} else if aliveCount != 0 {
		t.Fatalf("status reports not running but %d orphaned children still alive: %v", aliveCount, allPIDs)
	}

	// Cleanup: stop whatever's left running and wait for every spawned child
	// (including any that lost the race and were already reaped) to be dead.
	_ = a.Stop(context.Background(), "lease-777")
	deadline := time.Now().Add(3 * time.Second)
	for _, pid := range allPIDs {
		for processAlive(pid) && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if processAlive(pid) {
			t.Fatalf("pid %d still alive after final cleanup Stop", pid)
		}
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
