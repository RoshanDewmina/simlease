// Package reaper acts on reclaim decisions: shuts down and deletes simulator
// clones and lease directories exclusively through internal/safety, and
// scans for in-process orphan devices.
//
// Validate-vs-act separation mirrors internal/safety: ScanOrphans returns
// candidate UDIDs without deleting; callers that want to reclaim must call
// safety.DeleteSimulatorClone (which re-validates the live name).
//
// Persistence / crash-recovery scope gap: docs/ROADMAP.md's [mac] GATE for
// M1 includes "daemon SIGKILL mid-provision → restart reconciles with zero
// orphans". That requires the broker to persist lease state to
// ~/.simurgh/state.json via internal/store so a restarted daemon can
// reconstruct which UDIDs it owned. That wiring was deliberately scoped out
// of M1.1 (in-memory only) and is NOT built here. What this package delivers
// without persistence: (1) in-process reclamation of leases the live broker
// still tracks, and (2) a live orphan scan against the broker's currently
// in-memory known UDIDs (catches bookkeeping gaps within the same process
// lifetime). Reconcile is a stubbed extension point for the future
// restart-safe path — do not fake cross-process recovery here.
package reaper
