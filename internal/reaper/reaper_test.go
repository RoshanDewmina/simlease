package reaper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
	"github.com/RoshanDewmina/simurgh/internal/safety"
)

type fakeLister struct {
	mu      sync.Mutex
	devices []safety.Device
	err     error
}

func (f *fakeLister) ListDevices(ctx context.Context) ([]safety.Device, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	out := make([]safety.Device, len(f.devices))
	copy(out, f.devices)
	return out, nil
}

type fakeDeleter struct {
	mu            sync.Mutex
	shutdownCalls []string
	deleteCalls   []string
	deleteErr     error
}

func (f *fakeDeleter) Shutdown(ctx context.Context, udid string) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shutdownCalls = append(f.shutdownCalls, udid)
	return nil
}

func (f *fakeDeleter) Delete(ctx context.Context, udid string) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls = append(f.deleteCalls, udid)
	if f.deleteErr != nil {
		return f.deleteErr
	}
	return nil
}

func (f *fakeDeleter) deleted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.deleteCalls))
	copy(out, f.deleteCalls)
	return out
}

func TestReclaim_DeletesDeviceAndLeaseDir(t *testing.T) {
	root := t.TempDir()
	leaseID := "lease-1"
	leaseDir := filepath.Join(root, leaseID)
	if err := os.Mkdir(leaseDir, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(leaseDir, "marker.txt")
	if err := os.WriteFile(marker, []byte("lease-data"), 0o600); err != nil {
		t.Fatal(err)
	}

	udid := "AAAA-BBBB"
	lister := &fakeLister{devices: []safety.Device{{UDID: udid, Name: "simurgh-clone[test]"}}}
	deleter := &fakeDeleter{}

	r := New(ReaperOptions{
		LeasesRoot: root,
		Lister:     lister,
		Deleter:    deleter,
		StartedAt:  func() time.Time { return time.Unix(0, 0) },
		Now:        time.Now,
	})

	lease := broker.Lease{
		ID:     leaseID,
		State:  broker.LeaseReclaimed,
		Device: &broker.Device{UDID: udid, Name: "simurgh-clone[test]"},
	}
	if err := r.Reclaim(context.Background(), lease); err != nil {
		t.Fatalf("Reclaim: %v", err)
	}

	if got := deleter.deleted(); len(got) != 1 || got[0] != udid {
		t.Fatalf("want delete of %s, got %v", udid, got)
	}
	if _, err := os.Stat(leaseDir); !os.IsNotExist(err) {
		t.Fatalf("lease dir still present: %v", err)
	}
}

func TestReclaim_NilDeviceSkipsDeviceDelete(t *testing.T) {
	root := t.TempDir()
	leaseID := "lease-2"
	if err := os.Mkdir(filepath.Join(root, leaseID), 0o700); err != nil {
		t.Fatal(err)
	}

	deleter := &fakeDeleter{}
	r := New(ReaperOptions{
		LeasesRoot: root,
		Lister:     &fakeLister{},
		Deleter:    deleter,
	})

	lease := broker.Lease{
		ID:     leaseID,
		State:  broker.LeaseReclaimed,
		Device: nil,
	}
	if err := r.Reclaim(context.Background(), lease); err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if got := deleter.deleted(); len(got) != 0 {
		t.Fatalf("nil Device must not delete devices, got %v", got)
	}
	if _, err := os.Stat(filepath.Join(root, leaseID)); !os.IsNotExist(err) {
		t.Fatalf("lease dir should still be deleted: %v", err)
	}
}

func TestReclaim_MissingDeviceIsIdempotent(t *testing.T) {
	root := t.TempDir()
	leaseID := "lease-3"
	if err := os.Mkdir(filepath.Join(root, leaseID), 0o700); err != nil {
		t.Fatal(err)
	}

	r := New(ReaperOptions{
		LeasesRoot: root,
		Lister:     &fakeLister{}, // empty — device already gone
		Deleter:    &fakeDeleter{},
	})

	lease := broker.Lease{
		ID:     leaseID,
		State:  broker.LeaseReclaimed,
		Device: &broker.Device{UDID: "GONE-UDID", Name: "simurgh-clone[gone]"},
	}
	if err := r.Reclaim(context.Background(), lease); err != nil {
		t.Fatalf("missing device must be idempotent, got %v", err)
	}
}

func TestReclaim_ContinuesWhenOneSideFails(t *testing.T) {
	root := t.TempDir()
	leaseID := "lease-4"
	// Intentionally do NOT create the lease dir — DeleteLeaseDir is idempotent.
	// Force a real device-delete failure so we can assert both paths ran.
	udid := "FAIL-UDID"
	lister := &fakeLister{devices: []safety.Device{{UDID: udid, Name: "simurgh-clone[fail]"}}}
	deleter := &fakeDeleter{deleteErr: errors.New("simctl boom")}

	r := New(ReaperOptions{
		LeasesRoot: root,
		Lister:     lister,
		Deleter:    deleter,
	})

	err := r.Reclaim(context.Background(), broker.Lease{
		ID:     leaseID,
		State:  broker.LeaseReclaimed,
		Device: &broker.Device{UDID: udid, Name: "simurgh-clone[fail]"},
	})
	if err == nil {
		t.Fatal("want combined/device delete error")
	}
	if got := deleter.deleted(); len(got) != 1 || got[0] != udid {
		t.Fatalf("device delete must still be attempted, got %v", got)
	}
}

func TestScanOrphans_RespectsReconciliationWindow(t *testing.T) {
	start := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	now := start.Add(5 * time.Second) // inside default 10s window

	lister := &fakeLister{devices: []safety.Device{
		{UDID: "orphan-1", Name: "simurgh-clone[orphan]"},
	}}
	r := New(ReaperOptions{
		Lister:               lister,
		Deleter:              &fakeDeleter{},
		StartedAt:            func() time.Time { return start },
		Now:                  func() time.Time { return now },
		ReconciliationWindow: 10 * time.Second,
	})

	got, err := r.ScanOrphans(context.Background(), nil)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(got) != 0 {
		t.Fatalf("must refuse during reconciliation window, got %v", got)
	}
}

func TestScanOrphans_AfterWindow_FindsManagedUnknowns(t *testing.T) {
	start := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	now := start.Add(11 * time.Second)

	lister := &fakeLister{devices: []safety.Device{
		{UDID: "known-1", Name: "simurgh-clone[owned]"},
		{UDID: "orphan-clone", Name: "simurgh-clone[lost]"},
		{UDID: "orphan-warm", Name: "simurgh-warm[lost]"},
		{UDID: "base-1", Name: "simurgh-base[iPhone 17 Pro:27.0]"},
		{UDID: "user-1", Name: "My iPhone"},
	}}
	r := New(ReaperOptions{
		Lister:               lister,
		Deleter:              &fakeDeleter{},
		StartedAt:            func() time.Time { return start },
		Now:                  func() time.Time { return now },
		ReconciliationWindow: 10 * time.Second,
	})

	got, err := r.ScanOrphans(context.Background(), map[string]bool{"known-1": true})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	want := map[string]bool{"orphan-clone": true, "orphan-warm": true}
	if len(got) != 2 {
		t.Fatalf("want 2 orphans, got %v", got)
	}
	for _, udid := range got {
		if !want[udid] {
			t.Fatalf("unexpected orphan %s in %v", udid, got)
		}
	}
}

func TestReconcile_DelegatesToScanOrphans(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := start.Add(11 * time.Second) // past reconciliation window
	lister := &fakeLister{devices: []safety.Device{
		{UDID: "orphan-1", Name: "simurgh-clone[x]"},
		{UDID: "known-1", Name: "simurgh-clone[y]"},
		{UDID: "base-1", Name: "simurgh-base[z]"},
	}}
	r := New(ReaperOptions{
		Lister:    lister,
		Deleter:   &fakeDeleter{},
		StartedAt: func() time.Time { return start },
		Now:       func() time.Time { return now },
	})
	got, err := r.Reconcile(context.Background(), map[string]bool{"known-1": true})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(got) != 1 || got[0] != "orphan-1" {
		t.Fatalf("Reconcile orphans = %v, want [orphan-1]", got)
	}
}

func TestReconcile_RespectsReconciliationWindow(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := New(ReaperOptions{
		Lister: &fakeLister{devices: []safety.Device{
			{UDID: "orphan-1", Name: "simurgh-clone[x]"},
		}},
		Deleter:   &fakeDeleter{},
		StartedAt: func() time.Time { return start },
		Now:       func() time.Time { return start.Add(5 * time.Second) },
	})
	got, err := r.Reconcile(context.Background(), nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got != nil {
		t.Fatalf("within window Reconcile must return nil, got %v", got)
	}
}
