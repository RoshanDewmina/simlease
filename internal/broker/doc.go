// Package broker implements the lease lifecycle engine: state machine,
// per-DeviceSpec FIFO queue, capacity gate, and Acquire/Renew/Release/Get/List/Wait.
//
// Boundaries for this package:
//   - Provisioning is injected via Provisioner; this package never calls simctl
//     or xcodebuild.
//   - No deletion of devices or filesystem paths. Release only transitions lease
//     state and advances the queue; the reaper performs teardown via safety.
//   - No PID/TTL/heartbeat watcher. Renew only updates HeartbeatAt/ExpiresAt;
//     transitions to RECLAIMED are the watcher's job.
//   - Persistence is opt-in via BrokerOptions.Store (M1.6). When configured,
//     state-mutating ops journal the lease map through internal/store, and
//     Rehydrate restores ownership across daemon restarts. Without a Store the
//     broker stays in-memory-only (existing tests and callers unchanged).
package broker
