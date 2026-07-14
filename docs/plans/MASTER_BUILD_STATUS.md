# Simurgh Master Build Status

**Updated:** 2026-07-14 17:16 UTC (America/Toronto 13:16 EDT)
**Roadmap naming:** Phase B packages use `R-WP*` (not Phase A `P0-*` / harness `WP*`).
**Package state:** **R-WP6 PAUSED** at `bec8670` — **not complete, not reviewed**

## Baseline

- Prior tip (R-WP4 review-fix closed): `f39506114f222d33ef5c6a62b97fc90e81083900`
- Branch/worktree: `feat/r-wp6-xcodebuildmcp` @ `/Users/roshansilva/Documents/simurgh/.worktrees/r-wp6-xcodebuildmcp`
- Go: `go1.26.5 darwin/arm64`
- macOS: `27.0` (Build `26A5378n`), `arm64`
- Xcode: `27.0` (Build `27A5194q`); `xcrun simctl help` exit 0
- **Master (unchanged):** `4447e3c267a25d09ea20c3779c400dfa71330bef` — nothing merged or pushed

## Current work package

- ID: **R-WP6**
- Goal: Bounded XcodeBuildMCP session adapter + validation; evidence-backed R-WP5 reassessment
- State: **PAUSED** (owner stop). Committed adapter at `bec8670`; untracked unverified `internal/integrations/xcodebuildmcp/runtime_live_test.go` preserved. **Do not claim complete or reviewed.**
- Files owned (committed): `internal/integrations/xcodebuildmcp/*` (except untracked live test), `cmd/simurgh/integration.go`, `cmd/simurgh/integration_xcodebuildmcp_test.go`, `cmd/simurgh/main.go`, plus this status / orchestrator-state / session report
- Non-goals held: Phase A timed cells / `p0_rerun_driver` / `BENCH_START_AT` / bench evidence publishes; R-WP5 PATH shims (gated); merge/push

### Untracked (not in `bec8670`)

| Path | Status |
|---|---|
| `internal/integrations/xcodebuildmcp/runtime_live_test.go` | untracked; SHA-256 `9582650a420aa69976fe03a15a81a799d136eadfb8f71796fca7ce0dda13f5f3`; **not** formatted/compiled/run |

### Last verified gates (against `bec8670`, before untracked live test)

| Gate | Result |
|---|---|
| Package focused gate | **PASS**, 7 tests, `6.722s` |
| CLI focused gate | **PASS**, `31.208s` |
| `go build ./...` | **PASS** |
| `go vet ./...` | **PASS** |
| `go test -race ./...` | **PASS**, 18 packages |
| Capacity named tests | **PASS**, `1.814s` |
| 30s detach | **PASS**, daemon PID `66767`, `35.990s` |
| Live reuse (`darwin_sim`) | **PASS**, test `13.25s`, package `14.484s` |

### Explicitly deferred (unpublished claims)

| Cell / claim | Status |
|---|---|
| B1-fresh / B1-reuse timed | **deferred** — do not restart |
| B2 retries timed | **deferred** |
| B4-defaults-rep | **deferred** |
| B5 dual-window | **deferred** |
| B6 third ARM-S | **deferred** |
| XcodeBuildMCP composition (bench) | **deferred** |
| R-WP6 two-clone live runtime | **not run** (paused) |
| Warm-DD latency / time-to-green numbers | **unpublished** |
| New `bench/evidence/*` publishes | **not created** from R-WP4/R-WP6 |

### Prior R-WP4 acceptance commands (still the last full impl matrix on tip `f395061`)

```bash
go test -race ./cmd/simurgh -run 'TestWarmSeedCompatibilityHit|TestWarmSeedCompatibilityMiss|TestWarmSeedNeverOverwritesNonEmptyDestination|TestWarmSeedCloneFallback' -count=1 -v
go test -race ./internal/isolator -run 'TestWritableCachesStayUnderLeaseRoot' -count=1 -v
go test -race ./internal/provisioner -run 'TestReadinessPollSuccess|TestReadinessPollTimeoutClassified' -count=1 -v
go test -race ./internal/controlplane -run 'TestPhaseInstrumentationCoverage|TestPhaseAndQueueTimings|TestBuildProceedsWhileSimulatorFull|TestDeviceHeldOnlyDuringDevicePhase' -count=1 -v
go build ./...
go vet ./...
go test -race ./...
go test -race ./internal/broker -run 'TestGlobalCapacity|TestPerSpecCap|TestProvisioning|TestQueueReason|TestFairness|TestPressure' -count=1 -v
go test -race ./internal/ipc -run 'TestDialDaemonSurvivesParentExit' -count=1 -v
go test -race -tags darwin_sim ./internal/daemon -run TestReuseIfExistsLive -count=1 -v
```

## Completed packages

| Package | Commit / note | State |
|---|---|---|
| M0–M4 (v0 scaffold) | prior `master` lineage | historical complete |
| Phase A P0 harness (partial) | `593a051` STOPPED/BLOCKED | not a Phase A pass |
| **R-WP0** | `0757144` | complete |
| **R-WP1** | tip `e948d26` | complete |
| **R-WP2** | tip `23c2dce` | complete |
| **R-WP3** | tip `b811736` | complete |
| **R-WP4** | tip `f395061` (code tip `4e500f1` + status) | **implementation-complete / benchmark-deferred** |
| **R-WP6** | `bec8670` | **PAUSED** — not complete / not reviewed |

## Open failures/risks

1. Benchmark cells remain unmet by owner STOPPED directive — not a code defect in R-WP4/R-WP6.
2. Control-plane Session/Job state remains non-durable (R-WP3 carry-forward).
3. R-WP5 PATH shim still deferred pending R-WP6 reassessment (correction #3); reassessment **not** evidence-backed yet.
4. R-WP6 untracked live test unverified; package not reviewed.
5. Security rewrite unreachable-object caveat remains; **no push** of rewritten history without owner review.
6. Approved R-WP2 minor debt untouched: capacity migration surprise; LaunchAgent optional; global queue-position semantics.
7. GitHub issues #1–#3 still OPEN.

## Next package

- Resume **R-WP6 only** (review untracked live test → runtime validation → docs → R-WP5 decision), or stop at this docs boundary.
- **R-WP5** only after R-WP6 reassessment — do not start reflexively.
- Do **not** merge/push stacked branches from this status alone.
- Do **not** restart Phase A timed drivers unless owner directs.

## Session report

Detailed Sol session report: `docs/plans/SIMURGH_SOL_SESSION_REPORT_2026-07-14.md`
