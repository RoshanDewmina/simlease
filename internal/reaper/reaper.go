package reaper

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
	"github.com/RoshanDewmina/simurgh/internal/safety"
)

const defaultReconciliationWindow = 10 * time.Second

// ReaperOptions configures New. Zero-valued seams get sane defaults.
type ReaperOptions struct {
	LeasesRoot           string
	Lister               safety.DeviceLister
	Deleter              safety.DeviceDeleter
	StartedAt            func() time.Time
	Now                  func() time.Time
	ReconciliationWindow time.Duration
}

// Reaper acts on reclaim decisions: deletes clones/lease dirs via
// internal/safety, and scans for in-process orphans.
type Reaper struct {
	leasesRoot           string
	lister               safety.DeviceLister
	deleter              safety.DeviceDeleter
	startedAt            func() time.Time
	now                  func() time.Time
	reconciliationWindow time.Duration
}

// New constructs a Reaper. ReconciliationWindow defaults to 10s
// (ARCHITECTURE §10). StartedAt/Now default to time.Now at construction
// and call time respectively.
func New(opts ReaperOptions) *Reaper {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	startedAt := opts.StartedAt
	if startedAt == nil {
		start := now()
		startedAt = func() time.Time { return start }
	}
	window := opts.ReconciliationWindow
	if window <= 0 {
		window = defaultReconciliationWindow
	}
	return &Reaper{
		leasesRoot:           opts.LeasesRoot,
		lister:               opts.Lister,
		deleter:              opts.Deleter,
		startedAt:            startedAt,
		now:                  now,
		reconciliationWindow: window,
	}
}

// Reclaim tears down a lease that the broker has already marked RECLAIMED.
// It does not decide reclaim-worthiness — that is the watcher's job.
//
// When Device is non-nil, calls safety.DeleteSimulatorClone for the UDID.
// Always calls safety.DeleteLeaseDir for the lease ID. Both paths are
// attempted independently; a missing device/path is treated as success
// (idempotent). Returns a combined error only for genuine failures.
func (r *Reaper) Reclaim(ctx context.Context, lease broker.Lease) error {
	var errs []error

	if lease.Device != nil && lease.Device.UDID != "" {
		known := map[string]bool{lease.Device.UDID: true}
		err := safety.DeleteSimulatorClone(ctx, r.lister, r.deleter, lease.Device.UDID, known)
		if err != nil && !isAlreadyGone(err) {
			errs = append(errs, err)
		}
	}

	if err := safety.DeleteLeaseDir(r.leasesRoot, lease.ID); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

func isAlreadyGone(err error) bool {
	var nf *safety.DeviceNotFoundError
	return errors.As(err, &nf)
}

// ScanOrphans lists live simurgh-managed devices whose UDIDs are not in
// knownLeaseUDIDs (the broker's currently in-memory lease set).
//
// It refuses to run during the reconciliation window after StartedAt
// (ARCHITECTURE §10): returns (nil, nil) immediately with no listing.
//
// This function does NOT delete anything. A future caller that wants to
// reclaim orphans must call safety.DeleteSimulatorClone per returned UDID,
// which re-validates the live device name — so an orphan-scan false
// positive cannot cause an unsafe delete.
func (r *Reaper) ScanOrphans(ctx context.Context, knownLeaseUDIDs map[string]bool) ([]string, error) {
	if r.now().Sub(r.startedAt()) < r.reconciliationWindow {
		return nil, nil
	}

	devices, err := r.lister.ListDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("reaper: list devices: %w", err)
	}

	var orphans []string
	for _, d := range devices {
		if !safety.IsSimurghManagedName(d.Name) {
			continue
		}
		if knownLeaseUDIDs != nil && knownLeaseUDIDs[d.UDID] {
			continue
		}
		orphans = append(orphans, d.UDID)
	}
	return orphans, nil
}

// Reconcile is the extension point for restart-safe cross-process
// reconciliation (daemon crash → restart reconstructing owned UDIDs from
// ~/.simurgh/state.json via internal/store). That persistence wiring does
// not exist yet (deliberately deferred past M1.1). Until it does, this is
// a no-op.
//
// TODO(m1.x-store): when store persistence is wired, reconstruct known
// lease UDIDs from the journal and reclaim any device-name-scan orphans
// that are not accounted for after the reconciliation window.
func (r *Reaper) Reconcile(ctx context.Context) error {
	_ = ctx
	_ = r
	return nil
}
