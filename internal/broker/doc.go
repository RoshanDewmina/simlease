// Package broker implements the lease lifecycle engine: state machine,
// per-DeviceSpec FIFO queues under a host-global capacity gate, Acquire/Renew/
// Release/Get/List/Wait, and ReuseIfExists matching (R-WP1).
//
// Boundaries for this package:
//   - Provisioning is injected via Provisioner; this package never calls simctl
//     or xcodebuild.
//   - No deletion of devices or filesystem paths. Release only transitions lease
//     state and advances the fair queue; the reaper performs teardown via safety.
//   - No PID/TTL/heartbeat watcher. Renew only updates HeartbeatAt/ExpiresAt;
//     transitions to RECLAIMED (ACTIVE) or CANCELLED (dead QUEUED owner) are
//     the watcher's job via the daemon ReclaimLoop.
//   - Persistence is opt-in via BrokerOptions.Store (M1.6). When configured,
//     state-mutating ops journal the lease map through internal/store, and
//     Rehydrate restores ownership across daemon restarts. Without a Store the
//     broker stays in-memory-only (existing tests and callers unchanged).
//   - Capacity (SIMURGH_CAPACITY) is host-global ACTIVE+PROVISIONING. Optional
//     PerSpecCapacity and MaxProvisioningOperations add independent limits.
//     Queue reasons are machine-readable: simulator_slot, provisioning_slot,
//     per_spec_slot, pressure.
//   - ReuseIfExists matching key is Spec + exact Labels containing non-empty
//     project + non-empty Owner.Agent/SessionID; see reuse.go. This partitions
//     reuse but is not authentication against same-user raw IPC clients.
package broker
