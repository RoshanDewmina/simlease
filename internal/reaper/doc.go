// Package reaper acts on reclaim decisions: shuts down and deletes simulator
// clones and lease directories exclusively through internal/safety, and
// scans for orphan simurgh-managed devices.
//
// Validate-vs-act separation mirrors internal/safety: ScanOrphans/Reconcile
// return candidate UDIDs without deleting; callers that want to reclaim must
// call safety.DeleteSimulatorClone (which re-validates the live name).
//
// Crash recovery (M1.6): the broker persists lease ownership to
// ~/.simurgh/state.json via internal/store. On daemon restart, Rehydrate
// restores known ACTIVE leases into the in-memory broker; Reconcile then
// runs the same device-name orphan scan as ScanOrphans against
// Broker.KnownDeviceUDIDs (after the reconciliation window). Orphans remain
// log-only — auto-reclaim is still deferred to prune (M2.3).
package reaper
