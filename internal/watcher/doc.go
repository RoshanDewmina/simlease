// Package watcher decides which ACTIVE leases should be reclaimed and which
// QUEUED leases should be cancelled because their owner process is dead.
//
// Tick is a pure decision function: it scans the broker's in-memory leases and
// returns ReclaimDecision values (lease ID + reason) without calling
// Broker.Reclaim/Release or internal/safety. The daemon ReclaimLoop connects
// Run's callback to those side effects (Reclaim for ACTIVE, Release→CANCELLED
// for dead-owner QUEUED).
//
// Decision priority for each ACTIVE lease:
//  1. ReleaseOnPIDExit and owner PID not alive → "owner process exited"
//  2. hard timeout exceeded → "hard timeout"
//  3. inactivity timeout exceeded → "inactivity timeout"
//  4. TTL past ExpiresAt → "ttl expired"
//
// QUEUED leases always check owner PID liveness (independent of
// ReleaseOnPIDExit). A dead queued owner strands capacity after a cancelled
// --wait or daemon restart; reason "queued owner process exited".
package watcher
