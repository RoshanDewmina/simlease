package reaper

import (
	"context"
	"os"
	"path/filepath"

	"github.com/RoshanDewmina/simurgh/internal/broker"
)

// PruneBucket is a read-only plan of what prune would act on.
// Active is informational only — prune --apply never touches it.
type PruneBucket struct {
	Held    []broker.Lease
	Orphans []string
	Active  []broker.Lease
	// OrphanScanDeferred is true when PlanPrune ran inside the post-start
	// reconciliation window: Orphans is empty for that reason, not because a
	// real scan found zero unmanaged devices.
	OrphanScanDeferred bool
}

// PlanPrune buckets leases into held/active and discovers orphan devices.
//
// allLeases is the broker's full lease list (caller-supplied — Reaper does not
// hold a *broker.Broker). knownDeviceUDIDs should come from
// Broker.KnownDeviceUDIDs(); orphan detection reuses Reconcile (same 10s
// window and warm-template exclusion). UDIDs that still appear on any lease
// record (including terminal held leases) are removed from the orphan bucket
// so a lingering RELEASED device is "held", not "orphan".
//
// Held means teardown has not finished: a live simctl device still exists for
// the lease's UDID, and/or the lease directory is still on disk. A RELEASED
// lease whose Device pointer is non-nil but whose clone and lease dir are
// already gone is NOT held (broker keeps the Device field after Release;
// release→Reclaim clears the live resources without clearing that pointer).
func (r *Reaper) PlanPrune(ctx context.Context, allLeases []broker.Lease, knownDeviceUDIDs map[string]bool) (PruneBucket, error) {
	var plan PruneBucket

	liveUDIDs, err := r.liveDeviceUDIDs(ctx)
	if err != nil {
		return PruneBucket{}, err
	}

	leaseUDIDs := make(map[string]bool)
	for _, lease := range allLeases {
		if lease.Device != nil && lease.Device.UDID != "" {
			leaseUDIDs[lease.Device.UDID] = true
		}
		switch lease.State {
		case broker.LeaseQueued, broker.LeaseProvisioning, broker.LeaseActive, broker.LeaseExpiring:
			plan.Active = append(plan.Active, lease)
		case broker.LeaseReleased, broker.LeaseCancelled, broker.LeaseFailed:
			deviceLingering := lease.Device != nil && lease.Device.UDID != "" && liveUDIDs[lease.Device.UDID]
			dirLingering := leaseDirExists(r.leasesRoot, lease.ID)
			if deviceLingering || dirLingering {
				plan.Held = append(plan.Held, lease)
			}
		case broker.LeaseReclaimed:
			// Already torn down — nothing to plan.
		}
	}

	deferred := r.now().Sub(r.startedAt()) < r.reconciliationWindow
	orphans, err := r.Reconcile(ctx, knownDeviceUDIDs)
	if err != nil {
		return PruneBucket{}, err
	}
	plan.OrphanScanDeferred = deferred
	for _, udid := range orphans {
		if leaseUDIDs[udid] {
			continue // owned by a lease record (typically held) — not a true orphan
		}
		plan.Orphans = append(plan.Orphans, udid)
	}
	return plan, nil
}

func (r *Reaper) liveDeviceUDIDs(ctx context.Context) (map[string]bool, error) {
	out := map[string]bool{}
	if r.lister == nil {
		return out, nil
	}
	devices, err := r.lister.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range devices {
		if d.UDID != "" {
			out[d.UDID] = true
		}
	}
	return out, nil
}

func leaseDirExists(leasesRoot, leaseID string) bool {
	if leasesRoot == "" || leaseID == "" {
		return false
	}
	fi, err := os.Stat(filepath.Join(leasesRoot, leaseID))
	return err == nil && fi.IsDir()
}
