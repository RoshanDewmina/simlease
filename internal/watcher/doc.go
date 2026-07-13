// Package watcher decides which ACTIVE leases should be reclaimed.
//
// Tick is a pure decision function: it scans the broker's in-memory ACTIVE
// leases and returns ReclaimDecision values (lease ID + reason) without
// calling Broker.Reclaim or internal/safety. Production wiring (a later
// daemon/CLI package) connects Run's callback to those side effects.
//
// Decision priority for each ACTIVE lease:
//  1. ReleaseOnPIDExit and owner PID not alive → "owner process exited"
//  2. owner PID not alive (always checked; ARCHITECTURE §4) → "owner process not alive"
//  3. hard timeout exceeded → "hard timeout"
//  4. inactivity timeout exceeded → "inactivity timeout"
//  5. TTL past ExpiresAt → "ttl expired"
//
// Cross-process crash recovery (daemon restart reconstructing owned UDIDs
// from ~/.simurgh/state.json) is out of scope — that requires persistence
// wiring deferred past M1.1. This package only reasons about the live
// in-memory lease map.
package watcher
