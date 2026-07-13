// Package safety is the ONLY package permitted to perform recursive filesystem
// deletion or simulator device deletion.
//
// Every destructive call site in the product (reaper, prune, CLI prune
// --apply, warm-template removal) must route through this package — no
// exceptions. See docs/ARCHITECTURE.md §2 (deletion safety invariant) and §12.
//
// Invariant: recursive deletion is only ever permitted on (a) paths strictly
// under an injected leases root (ValidateLeasePath / DeleteLeaseDir), and
// (b) simulator devices whose live name matches
// ^simurgh-(clone|warm)\[.*\]$ (ValidateDeviceDeletion / DeleteSimulatorClone).
// The live name is always re-queried; caller-supplied names are never trusted.
package safety
