// Package broker implements the in-memory lease lifecycle engine: state machine,
// per-DeviceSpec FIFO queue, capacity gate, and Acquire/Renew/Release/Get/List/Wait.
//
// Boundaries for this package (M1.1):
//   - Provisioning is injected via Provisioner; M1.2 supplies the real simctl-backed
//     implementation. This package never calls simctl or xcodebuild.
//   - No deletion of devices or filesystem paths. Release only transitions lease
//     state and advances the queue; M1.3's reaper performs teardown via M1.4 safety.
//   - No PID/TTL/heartbeat watcher. Renew only updates HeartbeatAt/ExpiresAt;
//     transitions to RECLAIMED are M1.3's job.
//   - In-memory only. Persistence to the store journal is a later package; see the
//     hook comment on Broker.
package broker
