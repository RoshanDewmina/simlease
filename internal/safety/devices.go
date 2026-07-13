package safety

import (
	"context"
	"regexp"
)

// managedNamePattern is the single source of truth for which simulator
// device names may ever be deleted. Anchored; bases (simurgh-base[...])
// intentionally do not match.
var managedNamePattern = regexp.MustCompile(`^simurgh-(clone|warm)\[.*\]$`)

// warmTemplateNamePattern matches deliberately-persistent warm templates.
// Anchored; clones and bases do not match.
var warmTemplateNamePattern = regexp.MustCompile(`^simurgh-warm\[.*\]$`)

// IsSimurghManagedName reports whether name matches the deletion-allowlist
// pattern ^simurgh-(clone|warm)\[.*\]$. Exported so orphan scanners and prune
// planners can classify without duplicating the regex.
func IsSimurghManagedName(name string) bool {
	return managedNamePattern.MatchString(name)
}

// IsWarmTemplateName reports whether name matches ^simurgh-warm\[.*\]$.
// Warm templates are managed (deletable via DeleteSimulatorClone) but are
// never orphans — the reaper skips them during ScanOrphans.
func IsWarmTemplateName(name string) bool {
	return warmTemplateNamePattern.MatchString(name)
}

// Device is a minimal simulator device identity used by this package.
// Fields are duplicated (not imported) from provisioner to avoid cycles.
type Device struct {
	UDID string
	Name string
}

// DeviceLister returns the live simulator inventory. Callers must never
// supply a trusted device name — validation always re-queries.
type DeviceLister interface {
	ListDevices(ctx context.Context) ([]Device, error)
}

// DeviceDeleter performs shutdown-then-delete against a single UDID.
// Shutdown of an already-shutdown device should be tolerated by real
// implementations; DeleteSimulatorClone ignores already-shutdown-shaped
// shutdown errors and proceeds to Delete.
type DeviceDeleter interface {
	Shutdown(ctx context.Context, udid string) error
	Delete(ctx context.Context, udid string) error
}

// ValidateDeviceDeletion queries the live device list, finds udid, and
// refuses unless the live name matches IsSimurghManagedName. knownLeaseUDIDs
// is audit context only — a true entry never overrides a failing name check.
// This function performs no deletion.
func ValidateDeviceDeletion(
	ctx context.Context,
	lister DeviceLister,
	udid string,
	knownLeaseUDIDs map[string]bool,
) (Device, bool, error) {
	devices, err := lister.ListDevices(ctx)
	if err != nil {
		return Device{}, false, err
	}

	var found *Device
	for i := range devices {
		if devices[i].UDID == udid {
			found = &devices[i]
			break
		}
	}
	if found == nil {
		return Device{}, false, deviceNotFound(udid)
	}

	if !IsSimurghManagedName(found.Name) {
		return Device{}, false, deviceNotManaged(udid, found.Name)
	}

	knownLease := knownLeaseUDIDs != nil && knownLeaseUDIDs[udid]
	return *found, knownLease, nil
}

// DeleteSimulatorClone validates via ValidateDeviceDeletion immediately
// before acting (no cached prior validation), then shutdown (best-effort for
// already-shutdown) and delete. This is the only function in the package
// that deletes a simulator device.
func DeleteSimulatorClone(
	ctx context.Context,
	lister DeviceLister,
	deleter DeviceDeleter,
	udid string,
	knownLeaseUDIDs map[string]bool,
) error {
	if _, _, err := ValidateDeviceDeletion(ctx, lister, udid, knownLeaseUDIDs); err != nil {
		return err
	}

	// Shutdown is best-effort. Already-shutdown devices produce a simctl
	// error that must not block Delete; other shutdown failures are also
	// non-fatal so reclaim can still proceed to Delete after validation.
	_ = deleter.Shutdown(ctx, udid)

	if err := deleter.Delete(ctx, udid); err != nil {
		return deviceDeleteFailed(udid, err)
	}
	return nil
}
