# Simurgh Master Build Status

**Updated:** 2026-07-14 16:01 UTC (America/Toronto 12:01 EDT)
**Roadmap naming:** Phase B packages use `R-WP*` (not Phase A `P0-*` / harness `WP*`).

## Baseline

- Prior tip (R-WP2 gate-verified): `23c2dceeb3776c465f17b1d78a32200b8dac1576`
- Branch/worktree: `feat/r-wp3-session-jobs` @ `/Users/roshansilva/Documents/simurgh/.worktrees/r-wp3-session-jobs`
- Go: `go1.26.5 darwin/arm64`
- macOS: `27.0` (Build `26A5378n`), `arm64`
- Xcode: `27.0` (Build `27A5194q`); `xcrun simctl help` exit 0

## Current work package

- ID: **R-WP3**
- Goal: Session/Job/Worker/Workspace/Artifact/Event domain; independent build slots; job FSM; cancellation ownership; retry classification; lease compatibility adapter
- State: **complete** (all literal gates green; no merge/push)
- Files owned (product): `internal/controlplane/*`, `internal/daemon/{daemon,daemon_test}.go`, `internal/mcp/{server,server_test}.go`, `cmd/simurgh/daemon.go`, docs (`AGENT_GUIDE`, `ARCHITECTURE`, `TROUBLESHOOTING`, `README`), this status + orchestrator-state
- Non-goals held: readiness/benchmark reruns (R-WP4), PATH shims (R-WP5), XcodeBuildMCP adapter (R-WP6), SQLite/dashboard/multi-Mac, priority/aging, UI-test-slot scheduler

### Acceptance commands (exact)

```bash
go test -race ./internal/controlplane/... -run 'TestBuildProceedsWhileSimulatorFull|TestDeviceHeldOnlyDuringDevicePhase|TestCancelReleasesOnlyOwnedResources|TestJobStateTransitionsRejectInvalid|TestBuildSlotQueueMetadata|TestJobRetryClassification|TestPhaseAndQueueTimings' -count=1 -v
go test -race ./internal/controlplane/... -run 'TestTransitionCancelDuringDeviceAcquireCleansLease|TestFailCancelAndSucceedRacesPreserveTerminalWinner|TestCancelFailedJobIsRejectedWithoutMutation|TestJobStateTransitionMatrixExhaustive|TestWaitFalseBuildQueueDoesNotConsumeFutureSlot|TestBuildSlotsFromEnv' -count=1 -v
go build ./...
go vet ./...
go test -race ./...
go test -race ./internal/broker/... ./internal/ipc/... ./internal/daemon/... ./internal/mcp/... ./cmd/simurgh/...
go test -race ./internal/broker -run 'TestGlobalCapacity|TestPerSpecCap|TestProvisioning|TestQueueReason|TestFairness|TestPressure' -count=1 -v
go test -race ./internal/ipc -run 'TestDialDaemonSurvivesParentExit' -count=1 -v
go test -race -tags darwin_sim ./internal/daemon -run TestReuseIfExistsLive -count=1 -v
```

### Gate results (R-WP3 worktree)

| Gate | Result |
|---|---|
| controlplane named tests (7), post-fix | **PASS** exit 0 — package 1.289s |
| review race/FSM/Wait=false/env tests (6) | **PASS** exit 0 — package 1.160s |
| `go build ./...` | **PASS** exit 0 |
| `go vet ./...` | **PASS** exit 0 |
| `go test -race ./...` | **PASS** exit 0 (**17** packages) |
| package race (broker/ipc/daemon/mcp/cmd) | **PASS** exit 0 |
| capacity named tests | **PASS** exit 0 — all 8 matching tests |
| `TestDialDaemonSurvivesParentExit` | **PASS** exit 0 — **32.58s**; daemon **PID 15735** stable ≥30s |
| `TestReuseIfExistsLive` (`darwin_sim`) | **PASS** exit 0 — test 2.37s, package 3.515s |

### Behavioral proof (R-WP3)

1. **Build vs simulator independence:** `TestBuildProceedsWhileSimulatorFull` saturates simulator capacity (`CAPACITY_QUEUED` / `simulator_slot`) then grants a build slot.
2. **Device hold window:** `TestDeviceHeldOnlyDuringDevicePhase` holds a lease only for `waiting_for_device`…`testing`; released before `collecting`.
3. **Cancel ownership:** `TestCancelReleasesOnlyOwnedResources` cancels one job's build+device resources without touching a peer job's build grant.
4. **FSM:** `TestJobStateTransitionsRejectInvalid` rejects skips/backwards/terminal escapes; `CanTransition` table covers illegal pairs.
5. **Build queue metadata:** `TestBuildSlotQueueMetadata` returns `BUILD_QUEUED` with `resourceClass=build_slot`; Wait=false leaves `queuedBuilds=0`.
6. **Retry class:** `TestJobRetryClassification` maps infrastructure→safe, source/cancel→unsafe.
7. **Timings:** `TestPhaseAndQueueTimings` records queue wait and phase start/end/duration.

### Commits (from `23c2dce`)

| SHA | Summary |
|---|---|
| `bc86479` | feat(controlplane): session/job model and independent build slots |
| `d1c789f` | feat(daemon): surface build slot status on pool.status |
| `0f28d7a` | docs: record R-WP3 build slots and session/job handoff |
| `4050a5e` | fix(controlplane): make job transitions race-safe |
| *(status tip)* | docs: record bounded R-WP3 review-fix gates |

### Model notes (R-WP3)

- Env: `SIMURGH_BUILD_SLOTS` (default **2**).
- Resource class `build_slot` is independent; R-WP2 device deny order unchanged.
- `pool.status` / MCP additively expose `buildSlots`, `activeBuilds`, `queuedBuilds`.
- Session/job state is **in-memory / non-durable** (documented); no SQLite.
- No new public session/job CLI/RPC; lease.* remains the compatibility adapter and never returns build queue errors.
- Worker/Artifact remain intentional domain types only; no speculative lifecycle.

### Single bounded R-WP3 review-fix loop

All blocking/major/minor findings were addressed with failing tests first:

1. `TransitionJob` now captures state+generation, performs device work unlocked,
   and commits state/timings only after exact revalidation. A lost race releases
   only the lease acquired by that transition.
2. `FailJob`/`CancelJob` revalidate exact prior generation and preserve a
   concurrent terminal winner. Failed→Cancelled is rejected; repeated terminal
   results are explicit.
3. Retry edges are exactly Failed→Retrying→Queued; exhaustive matrix test covers
   every state pair.
4. Wait=false build contention returns `BUILD_QUEUED`/`build_slot` without a
   durable waiter. Only Wait=true FIFO waiters count in `queuedBuilds` and may
   be promoted.
5. Dead `buildWaiter.canceled` state was removed.
6. `BuildSlotsFromEnv` covers empty, invalid, zero, and positive values.

Race proof:

```text
go test -race ./internal/controlplane -run \
  'TestTransitionCancelDuringDeviceAcquireCleansLease|TestFailCancelAndSucceedRacesPreserveTerminalWinner|TestWaitFalseBuildQueueDoesNotConsumeFutureSlot|TestCancelFailedJobIsRejectedWithoutMutation' \
  -count=50
PASS — package 1.323s
```

### Approved R-WP2 minor debt (logged, untouched)

Paths not modified in R-WP3; residual items remain accepted:

1. Capacity env migration still tightens multi-spec hosts that previously multiplied per SpecKey.
2. Dial detach is Go-only (Setsid + `/dev/null` + SIGHUP ignore); LaunchAgent remains optional/future.
3. Queue position remains global rank among QUEUED leases (not per-spec-only).

## Completed packages

| Package | Commit / note | State |
|---|---|---|
| M0–M4 (v0 scaffold) | prior `master` lineage | historical complete |
| Phase A P0 harness (partial) | `593a051` STOPPED/BLOCKED | not a Phase A pass |
| **R-WP0** | `0757144` | complete |
| **R-WP1** | tip `e948d26` | complete |
| **R-WP2** | tip `23c2dce` | complete |
| **R-WP3** | tip after status commit on `feat/r-wp3-session-jobs` | complete |

## Open failures/risks

1. Control-plane Session/Job state is non-durable — daemon restart drops jobs/build queue; device leases remain on the broker journal.
2. Worker and Artifact are type-only in this bounded package; lifecycle and persistence remain later roadmap work.
3. No public session/job CLI yet — agents continue on lease tools until a later WP exposes session RPCs.

### Phase A carry-forward

Unmet cells (B2 retries, B4-defaults-rep, B5 dual-window, B6 third ARM-S, XcodeBuildMCP composition) remain unmet. Do not restart benchmarks unless owner directs.

## Next package

- **R-WP4** — Readiness, warm-cache and benchmark improvements.
- Do **not** merge/push R-WP3 from this package alone.
- Gate R-WP5 behind R-WP6 reassessment (owner correction #3).
- Handoff: start from R-WP3 tip on `feat/r-wp3-session-jobs`; build-slot vocabulary is stable; do not invent public session RPCs in R-WP4.
