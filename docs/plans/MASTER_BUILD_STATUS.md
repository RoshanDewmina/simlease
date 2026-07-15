# Simurgh Master Build Status

**Updated:** 2026-07-14 (correction pass, following the R-WP6 merge)
**Roadmap naming:** Phase B packages use `R-WP*` (not Phase A `P0-*` / harness `WP*`).
**Package state:** **R-WP6 MERGED to local `master`** at `a5afb8a` ("merge: R-WP6
XcodeBuildMCP session adapter (3 review rounds, all blocking findings
resolved)") after three independent review rounds; not pushed to `origin`.

## Baseline

- Prior tip (R-WP4 review-fix closed): `f39506114f222d33ef5c6a62b97fc90e81083900`
- Branch/worktree: `feat/r-wp6-xcodebuildmcp` @ `/Users/roshansilva/Documents/simurgh/.worktrees/r-wp6-xcodebuildmcp`
- Go: `go1.26.5 darwin/arm64`
- macOS: `27.0` (Build `26A5378n`), `arm64`
- Xcode: `27.0` (Build `27A5194q`); `xcrun simctl help` exit 0
- **Master:** `a5afb8a` (R-WP6 merge commit) — R-WP0 through R-WP6 are all
  ancestors of local `master`; nothing pushed to `origin`. Prior baseline for
  this table was `4447e3c267a25d09ea20c3779c400dfa71330bef` (pre-R-WP6-merge).

## Current work package

- ID: **R-WP6**
- Goal: Bounded XcodeBuildMCP session adapter + validation; evidence-backed R-WP5 reassessment
- State: **Resumed and gate-verified this session.** Committed adapter at `bec8670`; live runtime test formatted, one-line-fixed, compiled, and run green with real evidence (see below). **Since this session: independently reviewed across 3 rounds and merged to local `master` (`a5afb8a`); still not pushed.**
- Files owned (committed): `internal/integrations/xcodebuildmcp/*` (including the now-committed live test), `cmd/simurgh/integration.go`, `cmd/simurgh/integration_xcodebuildmcp_test.go`, `cmd/simurgh/main.go`, plus this status / orchestrator-state / session report / AGENT_GUIDE / ARCHITECTURE
- Non-goals held: Phase A timed cells / `p0_rerun_driver` / `BENCH_START_AT` / bench evidence publishes; R-WP5 PATH shims (gated); merge/push

### Live test resolution (this session)

| Item | Detail |
|---|---|
| `runtime_live_test.go` pre-fix SHA-256 | `9582650a420aa69976fe03a15a81a799d136eadfb8f71796fca7ce0dda13f5f3` |
| `gofmt -w` | needed formatting; applied |
| Compile check | **build failed**: `runtime_live_test.go:93:8: assignment mismatch: 1 variable but b.Release returns 2 values` — file had never been compiled before this session |
| Fix applied (owner-authorized, scoped to this exact line only) | `_ = b.Release(...)` → `_, _ = b.Release(...)`, matching the discard-error style of the two adjacent cleanup lines in the same `t.Cleanup` block |
| Post-fix `gofmt -l` | clean (no-op) |
| Post-fix SHA-256 | `dd315aba5f82cda2eabb5ce726fd31071c632f4cd986aa125d61059fa14ce980` |

### `internal/pressure` exclusion (this session)

`TestCheckCombinesSignals` in `internal/pressure` fails deterministically on
this host: the test mocks RAM/thermal via `setRunner` but computes the disk
signal from the real host filesystem, and this host has ~2.3GiB free against
the test's hardcoded 5GiB minimum. Confirmed pre-existing and unrelated to
R-WP6 (`internal/pressure` last touched by `2b0a4d8`, a shared ancestor of
`bec8670`, not modified by this branch). Owner-authorized, scoped exclusion
of this one package from the regression gate for this dispatch only —
`internal/pressure` itself was **not modified**.

### Gates run this session (resume, in order)

| Gate | Result |
|---|---|
| Package focused gate (7 tests) | **PASS**, `1.493s` |
| CLI focused gate (3 tests) | **PASS**, `3.936s` |
| `go build ./...` | **PASS**, `0.436s` |
| `go vet ./...` | **PASS**, `0.385s` |
| `go test -race ./...` (unscoped) | **FAIL** — only `internal/pressure` (see exclusion above); all other packages passed |
| `go test -race $(go list ./... \| grep -v internal/pressure)` | **PASS**, 17/17 packages |
| Broker named tests (8) | **PASS**, `1.018s` |
| IPC `TestDialDaemonSurvivesParentExit` | **PASS**, `32.28s` (daemon PID `38481` stable) |
| Daemon `TestReuseIfExistsLive` (`darwin_sim`) | **PASS**, `2.54s` (package `3.554s`) |
| **Live runtime validation** `TestXcodeBuildMCPRuntimeTwoSessionAttribution` (`darwin_sim`) | **PASS**, `63.50s` (package `64.518s`) — verdict `"composes"`, see evidence JSON in `docs/plans/orchestrator-state.md` |

### Explicitly deferred (unpublished claims)

| Cell / claim | Status |
|---|---|
| B1-fresh / B1-reuse timed | **deferred** — do not restart |
| B2 retries timed | **deferred** |
| B4-defaults-rep | **deferred** |
| B5 dual-window | **deferred** |
| B6 third ARM-S | **deferred** |
| XcodeBuildMCP composition (bench, timed/latency) | **deferred** — functional composition proved (see below), no latency claim |
| R-WP6 two-clone live runtime | **PASS**, `63.50s`, verdict `"composes"` — functional/isolation proof only, not a benchmark |
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
| **R-WP6** | `bec8670` + fix rounds, merged as `a5afb8a` | **complete — merged to local `master` after 3 review rounds; not pushed** |

## Open failures/risks

1. Benchmark cells remain unmet by owner STOPPED directive — not a code defect in R-WP4/R-WP6.
2. Control-plane Session/Job state remains non-durable (R-WP3 carry-forward).
3. R-WP5 PATH shim still gated behind R-WP6 reassessment (correction #3); the live runtime evidence now exists — see this doc's R-WP5 recommendation section / orchestrator-state.md for the grounded (not decided) call.
4. R-WP6 live test is compiled, formatted, and passing with real evidence, and **has since had three independent review rounds and been merged to local `master`** (`a5afb8a`); one major, non-blocking finding (same-session concurrent `start` TOCTOU) was accepted as tracked debt rather than a 4th fix-review cycle — see `docs/plans/orchestrator-state.md`.
5. Security rewrite unreachable-object caveat remains; **no push** of rewritten history without owner review.
6. `internal/pressure` `TestCheckCombinesSignals` is disk-space-dependent on hosts with <5GiB free on `/` (not hermetic on disk); excluded from this session's regression gate by owner authorization; not fixed, not in scope for R-WP6.
7. Approved R-WP2 minor debt untouched: capacity migration surprise; LaunchAgent optional; global queue-position semantics.
8. GitHub issues #1–#3 still OPEN.

## Next package

- R-WP6 is merged to local `master` (`a5afb8a`) after 3 review rounds; remaining next steps are the owner's R-WP5 decision and, eventually, an explicit push to `origin`.
- **R-WP5**: owner decides defer/shrink/retain using the grounded recommendation in this session's orchestrator-state.md entry — this doc does not make that call.
- Do **not** merge/push stacked branches from this status alone.
- Do **not** restart Phase A timed drivers unless owner directs.
- Do **not** start R-WP5 implementation from this status alone.

## Session report

Detailed Sol session report: `docs/plans/SIMURGH_SOL_SESSION_REPORT_2026-07-14.md`
