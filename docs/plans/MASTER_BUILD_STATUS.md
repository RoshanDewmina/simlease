# Simurgh Master Build Status

**Updated:** 2026-07-14 15:44 UTC (America/Toronto 11:44 EDT)
**Roadmap naming:** Phase B packages use `R-WP*` (not Phase A `P0-*` / harness `WP*`).

## Baseline

- Prior tip (R-WP1 gate-verified): `e948d26854f1fe9ea27851964a268b90ba78db16`
- Branch/worktree: `feat/r-wp2-global-capacity` @ `/Users/roshansilva/Documents/simurgh/.worktrees/r-wp2-global-capacity`
- Go: `go1.26.5 darwin/arm64`
- macOS: `27.0` (Build `26A5378n`), `arm64`
- Xcode: `27.0` (Build `27A5194q`); `xcrun simctl help` exit 0

## Current work package

- ID: **R-WP2**
- Goal: Host-global simulator capacity + optional per-spec/provisioning limits, explicit queue reasons, cross-spec fairness; daemon Dial survives parent exit
- State: **complete** (all literal gates green; no merge/push)
- Files owned (product): `internal/broker/*`, `internal/ipc/{client,protocol}.go`, `internal/daemon/daemon.go`, `cmd/simurgh/{daemon,lease}.go`, docs (`AGENT_GUIDE`, `ARCHITECTURE`, `TROUBLESHOOTING`, `README`), this status + orchestrator-state
- Non-goals held: Session/Job/build slots (R-WP3), PATH shims (R-WP5), XcodeBuildMCP adapter (R-WP6), full priority/aging, dashboard, Phase A resume

### Acceptance commands (exact)

```bash
go test -race ./internal/broker/... ./internal/ipc/... ./internal/daemon/... ./internal/mcp/... ./cmd/simurgh/...
go test -race ./internal/broker -run 'TestGlobalCapacity|TestPerSpecCap|TestProvisioning|TestQueueReason|TestFairness|TestPressure' -count=1 -v
go test -race ./internal/ipc -run 'TestDialDaemonSurvivesParentExit' -count=1 -v
go build ./...
go vet ./...
go test -race ./...
go test -race -tags darwin_sim ./internal/daemon -run TestReuseIfExistsLive -count=1 -v
```

### Gate results (R-WP2 worktree)

| Gate | Result |
|---|---|
| package race (broker/ipc/daemon/mcp/cmd) | **PASS** exit 0 — broker 1.638s, ipc 37.336s, daemon 1.495s, mcp 1.289s, cmd 1.169s |
| capacity named tests | **PASS** exit 0 — all 8 matching tests including `TestGlobalCapacityAcrossDifferentSpecs`, `TestPerSpecCapEnforced`, `TestProvisioningOpLimit`, `TestQueueReasonExplicit`, `TestFairness*`, `TestPressure*` |
| `TestDialDaemonSurvivesParentExit` | **PASS** exit 0 — **32.49s**; daemon **PID 80395** answered `daemon.ping` continuously for ≥30s after short-lived Dial parent exited (same PID); SIGHUP also survived |
| `go build ./...` | **PASS** exit 0 |
| `go vet ./...` | **PASS** exit 0 |
| `go test -race ./...` | **PASS** exit 0 (**16** packages; ipc includes 30s survival) |
| `TestReuseIfExistsLive` (`darwin_sim`) | **PASS** exit 0 — test 2.60s, package 3.768s |

### Single bounded review-fix loop (complete)

All six scoped findings were addressed; no R-WP3 work or review dispatch:

1. MCP `lease_acquire` preserves existing success fields and additively returns
   structured `code`, `message`, `fix`, `queuePosition`, `reason`, and
   `resourceClass` for `CAPACITY_QUEUED` (`IsError=true`).
2. MCP `pool_status` now exposes daemon-matching `perSpecCapacity` and
   `maxProvisioningOperations` JSON fields.
3. Daemon startup calls `AdvanceRehydratedQueues` once after Broker/Reaper/
   Provisioner and handlers are ready. A deterministic cross-spec rehydrate test
   proves PROVISIONING→FAILED frees the slot, no provisioning occurs before
   readiness, and the competing QUEUED spec is promoted immediately.
4. `Broker.Reclaim` godoc now states deterministic host-global advancement.
5. The fast detach test directly asserts the command's `/dev/null` stdin,
   shared log stdout/stderr, Setsid, and argv. The real 30-second process proof
   remains unchanged.
6. `brokerErrorToIPC` fixture asserts `reason`, `resourceClass`, and
   `queuePosition` together.

Focused review-fix gate:

```text
go test -race ./internal/mcp ./internal/daemon ./internal/ipc ./internal/broker ./cmd/simurgh \
  -run 'TestLeaseAcquireCapacityQueuedHasStructuredResourceReason|TestPoolStatusMapsResult|TestPoolStatusCountsByState|TestBrokerErrorToIPCMappings|TestStartupRehydrateAdvancesCompetingSpecImmediately|TestDetachedDaemonCommandUsesDevNullAndSetsid|TestFairnessCrossSpecReleaseAdvancesWaiter' \
  -count=1 -v
PASS: mcp 1.263s; daemon 1.260s; ipc 1.214s; broker 1.221s; cmd 1.237s
```

Literal gates after the review fixes:

| Gate | Exact result |
|---|---|
| package race | **PASS** exit 0 — broker 2.958s, ipc 40.475s, daemon 2.561s, mcp 2.800s, cmd 1.565s |
| named capacity regex | **PASS** exit 0 — 8 tests, broker 1.124s |
| 30s detach | **PASS** exit 0 — PID **92313** stable after parent exit; test 32.52s, package 33.627s |
| `go build ./...` | **PASS** exit 0 |
| `go vet ./...` | **PASS** exit 0 |
| `go test -race ./...` | **PASS** exit 0 — 16 packages |
| R-WP1 `TestReuseIfExistsLive` | **PASS** exit 0 — test 6.14s, package 7.365s |

### Commits (from `e948d26`)

| SHA | Summary |
|---|---|
| `db55a0b` | feat(broker): host-global capacity with explicit queue reasons |
| `18880b0` | fix(ipc): detach auto-start daemon from parent shell |
| `7a0277c` | docs: document global capacity migration and queue reasons |
| `d41edb5` | docs(plans): record R-WP2 gates and R-WP3 handoff |
| `50417bd` | test(ipc): log surviving daemon PID in detach proof |
| `c069e81` | fix(mcp): expose structured capacity queue metadata |
| `8d374fa` | fix(daemon): advance rehydrated queues at startup |
| *(status tip)* | docs(plans): record bounded R-WP2 review-fix gates |

### Capacity model (R-WP2)

- `SIMURGH_CAPACITY` → host-global ACTIVE+PROVISIONING cap (migrated from per-SpecKey).
- Optional `SIMURGH_PER_SPEC_CAPACITY`, `SIMURGH_MAX_PROVISIONING`.
- Deny order: `simulator_slot` → `provisioning_slot` → `per_spec_slot` → `pressure`.
- Fairness: oldest `AcquiredAt` then ID across competing SpecKey queues on release/fail/reclaim/`RetryQueues`.
- `CAPACITY_QUEUED` + `pool.status` expose `reason` / `resourceClass` and optional caps.

### Daemon detach (R-WP2 obligation closed)

- `startDetachedDaemon`: Setsid **and** stdin=`/dev/null` (Setsid alone was not accepted).
- `daemon run`: `signal.Ignore(SIGHUP)`.
- Proof: `TestDialDaemonSurvivesParentExit` — real `go build ./cmd/simurgh`, Dial from short-lived parent process, parent exit, same PID ping ≥30s (no unit-seam fake).

## Completed packages

| Package | Commit / note | State |
|---|---|---|
| M0–M4 (v0 scaffold) | prior `master` lineage | historical complete |
| Phase A P0 harness (partial) | `593a051` STOPPED/BLOCKED | not a Phase A pass |
| **R-WP0** | `0757144` | complete |
| **R-WP1** | tip `e948d26` | complete |
| **R-WP2** | tip after status commit on `feat/r-wp2-global-capacity` | complete |

## Open failures/risks

### Sensitive / residual

1. **Capacity migration surprise:** hosts that relied on per-spec multiplication of `SIMURGH_CAPACITY` will see tighter effective concurrency after upgrade — document and monitor first multi-spec load.
2. **Detach is Go-only, not LaunchAgent:** Dial auto-start now survives parent exit/SIGHUP under test; production may still prefer an explicit long-lived `daemon run` / future LaunchAgent for log/lifecycle ownership (not required for this gate).
3. **Queue position is global rank** among QUEUED (AcquiredAt/ID), while per-spec FIFOs remain for ordering within a SpecKey — clients that assumed per-spec-only positions should re-check.
4. **No build/UI-test slots yet** — simulator capacity still couples build-heavy agents until R-WP3.

### Phase A carry-forward

Unmet cells (B2 retries, B4-defaults-rep, B5 dual-window, B6 third ARM-S, XcodeBuildMCP composition) remain unmet. Do not restart benchmarks unless owner directs.

## Next package

- **R-WP3** — Session/Job model and separate build/device slots; compatibility adapter for existing lease commands; job state machine; cancellation ownership.
- Do **not** merge/push R-WP2 from this package alone.
- Gate R-WP5 behind R-WP6 reassessment (owner correction #3).
- Handoff: start from R-WP2 tip on `feat/r-wp2-global-capacity`; capacity/resource-class vocabulary is stable for build-slot reasons later; do not reopen Setsid-only detach claims.
