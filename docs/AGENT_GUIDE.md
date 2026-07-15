# Agent guide — using Simurgh

This document is for an AI coding agent attached to a project that uses
Simurgh. Read it once, then follow the lease discipline for every simulator
build/test/launch.

## Why Simurgh

Never pick a simulator UDID from `xcrun simctl list` (or similar) for a device
you are about to build, install, or test against. Concurrent agents sharing a
booted simulator or the default DerivedData tree collide.

Simurgh grants an **exclusive lease**: a clone device plus isolated host paths.
Always acquire a lease, use only that lease’s UDID / env / flags, and release
when finished.

## MCP tools (preferred when available)

`simurgh mcp` exposes five tools. Descriptions below are the live API surface
from `internal/mcp/server.go` — follow them literally.

### `lease_acquire`

> Acquire an isolated iOS Simulator lease before any simulator interaction. Never pick a UDID from simctl yourself — always go through this tool. Returns the device UDID plus the exact env vars and xcodebuild flags to use for isolated builds (see the env and xcodebuildFlags fields) — pass these to xcodebuild / simurgh exec rather than using default paths. Call once per parallel agent or session that needs its own simulator; do not share one lease across concurrent agents. Leases are session-bound by default: when this agent session ends, the lease is reclaimed automatically. Pass detach=true only when the lease must deliberately outlive this session.

### `lease_renew`

> Extend an active lease's TTL before it expires. Call when your work will outlast the current expiry (from lease_acquire or lease_status). Do not acquire a second lease just to keep working — renew the lease you already hold. Requires an explicit lease_id; this tool does not fall back to the last-lease pointer.

### `lease_release`

> Release a lease when you are finished with the simulator so the device can be deleted and capacity returned to the pool. Always release before ending your session. Do not leave leases dangling — they hold pool capacity and keep clone devices around until cleaned up. Pass the lease_id returned by lease_acquire. Prefer release over abandoning the lease and hoping a reaper finds it.

### `lease_status`

> Inspect a lease's current state, device UDID, expiry, and isolation env/flags. Call to confirm a lease is still ACTIVE before building or launching, or to recover details after a disconnect. If lease_id is omitted, uses the caller's most recently acquired lease (~/.simurgh/last-lease). Do not invent UDIDs from simctl list — use the UDID from this tool or from lease_acquire.

### `pool_status`

> Report pool capacity and how many leases are active, queued, or otherwise. Also reports independent buildSlots/activeBuilds/queuedBuilds (SIMURGH_BUILD_SLOTS). Call before lease_acquire when you need to know whether the pool is under pressure, or after acquire/release to confirm pool accounting. Do not use this to pick a device or UDID — always go through lease_acquire for that.

Project setup (human or agent with shell): `simurgh init` writes `.simurgh.yaml`,
registers the stdio MCP entry in `.mcp.json`, and prints a paste-ready rules
snippet. MCP clients load `.mcp.json` at session start — restart after `init`.

## CLI equivalent (no MCP)

Same daemon RPCs as the MCP tools:

```bash
simurgh acquire --model "iPhone 17 Pro" --wait --rm --json
SIMURGH_SESSION_ID=my-agent-session \
simurgh acquire --model "iPhone 17 Pro" --label project=myapp --label agent=claude-a \
  --reuse-if-exists --wait --rm --json   # same Spec+labels+agent/session → renew same lease
simurgh env <lease-id>                 # or: eval "$(simurgh env <lease-id>)"
simurgh exec <lease-id> -- xcodebuild … build
simurgh renew <lease-id> [--ttl DUR]
simurgh status --json                  # pool + pressure
simurgh release <lease-id>
```

### `--reuse-if-exists` matching (R-WP1)

Compatible ACTIVE leases are renewed in place; compatible PROVISIONING
requests attach and wait for that same grant. Both require Spec, **exact**
Labels including a non-empty `project`, and non-empty
`Owner.Agent`+`Owner.SessionID` to match. PID is ignored. Missing identity or
project is a no-match/new allocation. CLI honors `SIMURGH_SESSION_ID`; without
it, each one-shot CLI process gets a fresh random session ID, so
cross-invocation reuse requires setting the variable. MCP honors the variable;
its random fallback is stable only during that MCP server process.
RELEASED / FAILED / RECLAIMED / CANCELLED leases are never reused. Concurrent
identical reuse requests share one provision. Put project / Xcode / scheme
identity in Labels — those dimensions are not first-class fields.

Acquire responses include top-level `reused`: `true` for ACTIVE renewal or
PROVISIONING attach, `false` for a new allocation.

MCP `lease_acquire` with `reuse_if_exists=true` uses the same match key.

### Identity trust boundary

Owner/label identity is a conservative reuse partition, not authentication.
The daemon socket and parent directory are same-user protected (0600/0700),
but a same-user raw IPC client can forge Owner/Labels and can already
renew/release a known lease ID. Do not describe `SIMURGH_SESSION_ID`, Owner, or
the project label as unforgeable credentials.
Client commands auto-start the daemon via `ipc.Dial` if the socket is down.
`simurgh doctor` and `simurgh daemon status` do **not** auto-start.

## Isolation env contract

From `internal/isolator.BuildEnv` — these are the real variable names:

| Variable | Purpose |
|---|---|
| `SIMURGH_LEASE_ID` | Lease id |
| `SIMURGH_UDID` | Leased simulator UDID |
| `SIMURGH_DERIVED_DATA` | Per-lease DerivedData root |
| `CLANG_MODULE_CACHE_PATH` | Per-lease Clang module cache |
| `SIMURGH_RESULT_BUNDLE_DIR` | Per-lease Results directory |
| `SIMURGH_SWIFTPM_DIR` | Per-lease SwiftPM / cloned packages dir |

Emitted `xcodebuildFlags` (also on the lease / MCP acquire result):

- `-derivedDataPath` → `SIMURGH_DERIVED_DATA`
- `-clonedSourcePackagesDirPath` → `SIMURGH_SWIFTPM_DIR`
- `-destination` → `platform=iOS Simulator,id=<SIMURGH_UDID>`

`simurgh exec` injects those flags for `xcodebuild` (and allocates a fresh
`-resultBundlePath` under Results for `test` / `test-without-building`). There
is no PATH shim yet (v0.1) — bare `xcodebuild` without `exec` or explicit flags
will use shared defaults and defeat isolation.

## Capacity (host-global)

`SIMURGH_CAPACITY` caps ACTIVE+PROVISIONING leases for the whole host — it no
longer multiplies per DeviceSpec. Optional `SIMURGH_PER_SPEC_CAPACITY` and
`SIMURGH_MAX_PROVISIONING` add tighter limits. When acquire queues,
`CAPACITY_QUEUED` includes `reason` / `resourceClass`
(`simulator_slot` | `provisioning_slot` | `per_spec_slot` | `pressure`).
Call `pool_status` / `simurgh status --json` before blaming hangs on a bug.

### Build slots (R-WP3)

`SIMURGH_BUILD_SLOTS` configures an independent build pool (default 2). Build
queueing uses resource class `build_slot` and is separate from simulator
capacity — a full simulator pool does not block a free build slot. Existing
`lease_acquire` / `lease.acquire` never return build-slot queue errors; those
surface only through the internal control-plane build scheduler.
`pool_status` additively reports `buildSlots` / `activeBuilds` /
`queuedBuilds`. Session/job state is in-memory and non-durable in this
package; keep using lease tools as the public compatibility surface.

### Warm DerivedData seeds + readiness (R-WP4)

`simurgh exec … -- xcodebuild` seeds an empty lease DerivedData from an
immutable shared APFS seed when the formal compatibility key matches
exactly (project/workspace, scheme, configuration, Xcode version/build,
SDK/runtime, `Package.resolved` hash, policy version). Exact mismatch →
cold/no-seed. Non-empty lease DerivedData is never overwritten. Clone
failure falls back to cold. Writable caches stay under the lease root —
never `~/Library/Developer/Xcode/DerivedData`.

Boot wait uses bounded readiness polls (`READINESS_TIMEOUT` classified;
optional weaker fallback after the primary deadline). Control-plane jobs
record additive phase timings for `acquire` / `build` / `install` /
`launch` / `test` / `capture` from real clock samples.

**Benchmark-deferred:** timed B1/B2/B4/B5/B6 cells, `p0_rerun_driver`, and
new `bench/evidence` publishes are explicitly out of this package. Do not
treat any warm-seed or readiness change as a measured latency win until a
later owner-directed benchmark pass.

## XcodeBuildMCP integration adapter (R-WP6)

`simurgh integration xcodebuildmcp start|stop|status --session <id> [--json]`
starts/stops an XcodeBuildMCP child process bound to one lease's isolated
env/paths (own UDID, own DerivedData/SwiftPM/module cache). Two concurrent
sessions get two independent adapters; stopping one never touches the
other's process or DerivedData. `stop` rejects a foreign PID.

Live-validated (two real leases, two real `xcodebuildmcp simulator build`
runs, `darwin_sim`-tagged `TestXcodeBuildMCPRuntimeTwoSessionAttribution`):
distinct UDIDs, distinct DerivedData roots, distinct adapter PIDs, no growth
in the shared global DerivedData tree, and session B stayed valid after
session A's adapter was stopped. This is a functional isolation proof, not
a latency/performance claim, and not an independent-review pass — see
`docs/ARCHITECTURE.md` §14 and `docs/plans/orchestrator-state.md`.

## Release discipline

Always `lease_release` / `simurgh release` when done.

Since M2.3, a successful release best-effort tears down the clone device and
returns capacity to the pool. Do not leave leases dangling “for later” — they
hold capacity and keep devices around until TTL / inactivity / hard-cap or an
explicit `simurgh prune --apply`.

Explicit release is a courtesy, not the safety net. The safety net is
session binding:

- **MCP (default):** leases are session-bound — the lease is registered
  against the MCP server process, which lives exactly as long as your agent
  session. Crash, force-quit, or window close ⇒ the watcher reclaims the
  lease (and deletes its device) within seconds. Pass `detach=true` on
  `lease_acquire` only when a lease must deliberately outlive the session.
- **CLI:** pass `--rm` on `acquire` (as in the snippet above). `--rm` binds
  the lease to the *parent* of the one-shot `acquire` process — your shell or
  agent session — so the lease dies when that session does. Without `--rm`, a
  lease survives the acquiring process exiting and is only reclaimed by
  TTL / inactivity / hard-cap; use `--inactivity-timeout DUR` as a fallback
  when a long-lived owner process doesn't exist (e.g. fire-and-forget
  scripts). See [TROUBLESHOOTING.md](TROUBLESHOOTING.md).
