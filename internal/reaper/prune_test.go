package reaper

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
	"github.com/RoshanDewmina/simurgh/internal/safety"
)

func TestPlanPrune_BucketsByState(t *testing.T) {
	root := t.TempDir()
	heldDir := filepath.Join(root, "held-dir-only")
	if err := os.Mkdir(heldDir, 0o700); err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	now := start.Add(11 * time.Second)
	lister := &fakeLister{devices: []safety.Device{
		{UDID: "orphan-1", Name: "simurgh-clone[lost]"},
		{UDID: "warm-1", Name: "simurgh-warm[iPhone 17 Pro:27.0]"},
		{UDID: "active-udid", Name: "simurgh-clone[active]"},
		{UDID: "held-udid", Name: "simurgh-clone[held]"},
		{UDID: "fail-udid", Name: "simurgh-clone[fail]"},
		// reclaimed-udid / released-clean intentionally absent from live list
	}}
	r := New(ReaperOptions{
		LeasesRoot:           root,
		Lister:               lister,
		Deleter:              &fakeDeleter{},
		StartedAt:            func() time.Time { return start },
		Now:                  func() time.Time { return now },
		ReconciliationWindow: 10 * time.Second,
	})

	leases := []broker.Lease{
		{ID: "active-1", State: broker.LeaseActive, Device: &broker.Device{UDID: "active-udid"}},
		{ID: "queued-1", State: broker.LeaseQueued},
		{ID: "prov-1", State: broker.LeaseProvisioning},
		{ID: "held-dev", State: broker.LeaseReleased, Device: &broker.Device{UDID: "held-udid"}},
		{ID: "held-dir-only", State: broker.LeaseCancelled}, // dir exists, no device
		{ID: "failed-1", State: broker.LeaseFailed, Device: &broker.Device{UDID: "fail-udid"}},
		{ID: "reclaimed-1", State: broker.LeaseReclaimed, Device: &broker.Device{UDID: "reclaimed-udid"}},
		// Device pointer still set after successful release→Reclaim, but live
		// device and lease dir are gone → not held.
		{ID: "released-clean", State: broker.LeaseReleased, Device: &broker.Device{UDID: "gone-udid"}},
	}

	known := map[string]bool{"active-udid": true, "held-udid": true, "fail-udid": true, "reclaimed-udid": true}
	plan, err := r.PlanPrune(context.Background(), leases, known)
	if err != nil {
		t.Fatalf("PlanPrune: %v", err)
	}

	heldIDs := leaseIDs(plan.Held)
	if !containsAll(heldIDs, "held-dev", "held-dir-only", "failed-1") {
		t.Fatalf("held=%v want held-dev, held-dir-only, failed-1", heldIDs)
	}
	if containsAll(heldIDs, "released-clean") {
		t.Fatalf("clean RELEASED with no live device/dir must not be held: %v", heldIDs)
	}
	if containsAll(heldIDs, "reclaimed-1") {
		t.Fatalf("RECLAIMED must not be held: %v", heldIDs)
	}

	activeIDs := leaseIDs(plan.Active)
	if !containsAll(activeIDs, "active-1", "queued-1", "prov-1") {
		t.Fatalf("active=%v want active-1, queued-1, prov-1", activeIDs)
	}
	if containsAll(activeIDs, "reclaimed-1") {
		t.Fatalf("RECLAIMED must not be active: %v", activeIDs)
	}

	if len(plan.Orphans) != 1 || plan.Orphans[0] != "orphan-1" {
		t.Fatalf("orphans=%v want [orphan-1] (warm excluded)", plan.Orphans)
	}
	if plan.OrphanScanDeferred {
		t.Fatal("past reconciliation window: OrphanScanDeferred must be false")
	}
}

func TestPlanPrune_ReclaimedInNeitherBucket(t *testing.T) {
	r := New(ReaperOptions{
		LeasesRoot: t.TempDir(),
		Lister:     &fakeLister{},
		Deleter:    &fakeDeleter{},
		StartedAt:  func() time.Time { return time.Unix(0, 0) },
		Now:        time.Now,
	})
	plan, err := r.PlanPrune(context.Background(), []broker.Lease{
		{ID: "r1", State: broker.LeaseReclaimed, Device: &broker.Device{UDID: "x"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Held) != 0 || len(plan.Active) != 0 {
		t.Fatalf("RECLAIMED must land in neither held nor active: held=%v active=%v", plan.Held, plan.Active)
	}
}

func TestPlanPrune_OrphanScanDeferredDuringWindow(t *testing.T) {
	start := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	r := New(ReaperOptions{
		LeasesRoot: t.TempDir(),
		Lister: &fakeLister{devices: []safety.Device{
			{UDID: "orphan-1", Name: "simurgh-clone[x]"},
		}},
		Deleter:              &fakeDeleter{},
		StartedAt:            func() time.Time { return start },
		Now:                  func() time.Time { return start.Add(5 * time.Second) },
		ReconciliationWindow: 10 * time.Second,
	})
	plan, err := r.PlanPrune(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Orphans) != 0 {
		t.Fatalf("within window orphans must be empty, got %v", plan.Orphans)
	}
	if !plan.OrphanScanDeferred {
		t.Fatal("OrphanScanDeferred must be true during reconciliation window")
	}
}

func leaseIDs(leases []broker.Lease) []string {
	out := make([]string, len(leases))
	for i, l := range leases {
		out[i] = l.ID
	}
	return out
}

func containsAll(haystack []string, needles ...string) bool {
	set := map[string]bool{}
	for _, h := range haystack {
		set[h] = true
	}
	for _, n := range needles {
		if !set[n] {
			return false
		}
	}
	return true
}
