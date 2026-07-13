package watcher

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
)

type fakeBroker struct {
	mu     sync.Mutex
	leases []broker.Lease
}

func (f *fakeBroker) List(filter broker.ListFilter) []broker.Lease {
	f.mu.Lock()
	defer f.mu.Unlock()
	_ = filter
	out := make([]broker.Lease, len(f.leases))
	copy(out, f.leases)
	return out
}

func (f *fakeBroker) set(leases ...broker.Lease) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.leases = append([]broker.Lease(nil), leases...)
}

func baseActive(id string, pid int, now time.Time) broker.Lease {
	return broker.Lease{
		ID:          id,
		State:       broker.LeaseActive,
		Owner:       broker.Owner{PID: pid},
		AcquiredAt:  now.Add(-5 * time.Minute),
		HeartbeatAt: now.Add(-30 * time.Second),
		ExpiresAt:   now.Add(25 * time.Minute),
		TTL:         30 * time.Minute,
	}
}

func TestTick_HealthyActiveProducesNoDecisions(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	fb := &fakeBroker{}
	fb.set(baseActive("lease-1", 42, now))

	w := New(WatcherOptions{
		Broker: fb,
		Now:    func() time.Time { return now },
		IsProcessAlive: func(pid int) bool {
			return pid == 42
		},
	})

	got := w.Tick(context.Background())
	if len(got) != 0 {
		t.Fatalf("want 0 decisions, got %+v", got)
	}
}

func TestTick_OwnerProcessExited_ReleaseOnPIDExit(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	lease := baseActive("lease-1", 99, now)
	lease.ReleaseOnPIDExit = true
	fb := &fakeBroker{}
	fb.set(lease)

	w := New(WatcherOptions{
		Broker:         fb,
		Now:            func() time.Time { return now },
		IsProcessAlive: func(pid int) bool { return false },
	})

	got := w.Tick(context.Background())
	if len(got) != 1 || got[0].LeaseID != "lease-1" || got[0].Reason != "owner process exited" {
		t.Fatalf("got %+v", got)
	}
}

func TestTick_OwnerProcessNotAlive_RegardlessOfFlag(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	lease := baseActive("lease-2", 77, now)
	lease.ReleaseOnPIDExit = false
	fb := &fakeBroker{}
	fb.set(lease)

	w := New(WatcherOptions{
		Broker:         fb,
		Now:            func() time.Time { return now },
		IsProcessAlive: func(pid int) bool { return false },
	})

	got := w.Tick(context.Background())
	if len(got) != 1 || got[0].LeaseID != "lease-2" || got[0].Reason != "owner process not alive" {
		t.Fatalf("got %+v", got)
	}
}

func TestTick_HardTimeout(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	lease := baseActive("lease-3", 1, now)
	lease.AcquiredAt = now.Add(-2 * time.Hour)
	lease.Timeouts.Hard = time.Hour
	fb := &fakeBroker{}
	fb.set(lease)

	w := New(WatcherOptions{
		Broker:         fb,
		Now:            func() time.Time { return now },
		IsProcessAlive: func(pid int) bool { return true },
	})

	got := w.Tick(context.Background())
	if len(got) != 1 || got[0].Reason != "hard timeout" {
		t.Fatalf("got %+v", got)
	}
}

func TestTick_InactivityTimeout(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	lease := baseActive("lease-4", 1, now)
	lease.HeartbeatAt = now.Add(-20 * time.Minute)
	lease.Timeouts.Inactivity = 10 * time.Minute
	fb := &fakeBroker{}
	fb.set(lease)

	w := New(WatcherOptions{
		Broker:         fb,
		Now:            func() time.Time { return now },
		IsProcessAlive: func(pid int) bool { return true },
	})

	got := w.Tick(context.Background())
	if len(got) != 1 || got[0].Reason != "inactivity timeout" {
		t.Fatalf("got %+v", got)
	}
}

func TestTick_TTLExpired(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	lease := baseActive("lease-5", 1, now)
	lease.ExpiresAt = now.Add(-time.Second)
	fb := &fakeBroker{}
	fb.set(lease)

	w := New(WatcherOptions{
		Broker:         fb,
		Now:            func() time.Time { return now },
		IsProcessAlive: func(pid int) bool { return true },
	})

	got := w.Tick(context.Background())
	if len(got) != 1 || got[0].Reason != "ttl expired" {
		t.Fatalf("got %+v", got)
	}
}

func TestTick_RoadmapSIGKILLOwnerReclaimedInOneTick(t *testing.T) {
	// docs/ROADMAP.md M1 GATE: SIGKILL'd owner → RECLAIMED within one watcher
	// interval. At the unit level, one Tick with IsProcessAlive=false for the
	// owner PID is sufficient — no sleep/interval required.
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	const deadPID = 4242
	lease := baseActive("lease-sigkill", deadPID, now)
	lease.ReleaseOnPIDExit = true
	fb := &fakeBroker{}
	fb.set(lease)

	w := New(WatcherOptions{
		Broker: fb,
		Now:    func() time.Time { return now },
		IsProcessAlive: func(pid int) bool {
			return pid != deadPID
		},
	})

	got := w.Tick(context.Background())
	if len(got) != 1 || got[0].LeaseID != "lease-sigkill" {
		t.Fatalf("want reclaim of lease-sigkill, got %+v", got)
	}
}

func TestTick_IgnoresNonActive(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	fb := &fakeBroker{}
	fb.set(broker.Lease{
		ID:    "lease-q",
		State: broker.LeaseQueued,
		Owner: broker.Owner{PID: 1},
	})

	w := New(WatcherOptions{
		Broker:         fb,
		Now:            func() time.Time { return now },
		IsProcessAlive: func(pid int) bool { return false },
	})

	got := w.Tick(context.Background())
	if len(got) != 0 {
		t.Fatalf("want 0, got %+v", got)
	}
}

func TestTick_PriorityPIDDeathBeforeTimeouts(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	lease := baseActive("lease-p", 9, now)
	lease.ReleaseOnPIDExit = true
	lease.ExpiresAt = now.Add(-time.Hour) // would also TTL-expire
	lease.Timeouts.Hard = time.Minute
	lease.AcquiredAt = now.Add(-time.Hour)
	fb := &fakeBroker{}
	fb.set(lease)

	w := New(WatcherOptions{
		Broker:         fb,
		Now:            func() time.Time { return now },
		IsProcessAlive: func(pid int) bool { return false },
	})

	got := w.Tick(context.Background())
	if len(got) != 1 || got[0].Reason != "owner process exited" {
		t.Fatalf("PID death must win priority, got %+v", got)
	}
}

func TestRun_InvokesCallbackOnTick(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	lease := baseActive("lease-run", 1, now)
	lease.ExpiresAt = now.Add(-time.Second)
	fb := &fakeBroker{}
	fb.set(lease)

	w := New(WatcherOptions{
		Broker:         fb,
		Now:            func() time.Time { return now },
		IsProcessAlive: func(pid int) bool { return true },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	seen := make(chan []ReclaimDecision, 1)
	go w.Run(ctx, 20*time.Millisecond, func(ds []ReclaimDecision) {
		if len(ds) > 0 {
			select {
			case seen <- ds:
			default:
			}
			cancel()
		}
	})

	select {
	case ds := <-seen:
		if ds[0].LeaseID != "lease-run" || ds[0].Reason != "ttl expired" {
			t.Fatalf("callback got %+v", ds)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run never invoked callback with a decision")
	}
}
