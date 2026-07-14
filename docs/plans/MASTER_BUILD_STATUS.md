# Simurgh Master Build Status

**Updated:** 2026-07-14 16:47 UTC (America/Toronto 12:47 EDT)
**Roadmap naming:** Phase B packages use `R-WP*` (not Phase A `P0-*` / harness `WP*`).
**Package state:** **R-WP4 implementation-complete / benchmark-deferred**

## Baseline

- Prior tip (R-WP3 gate-verified): `b811736a4543d658bd5aecbf5701b16bb5a7548a`
- Branch/worktree: `feat/r-wp4-readiness-cache` @ `/Users/roshansilva/Documents/simurgh/.worktrees/r-wp4-readiness-cache`
- Go: `go1.26.5 darwin/arm64`
- macOS: `27.0` (Build `26A5378n`), `arm64`
- Xcode: `27.0` (Build `27A5194q`); `xcrun simctl help` exit 0

## Current work package

- ID: **R-WP4**
- Goal: Bounded readiness polls; formal APFS warm-seed compatibility keys; private writable-cache isolation; acquire/build/install/launch/test/capture phase instrumentation
- State: **implementation-complete / benchmark-deferred** (all literal implementation gates green; **no** timed matrix / no new `bench/evidence` / no latency claims)
- Files owned: `cmd/simurgh/warm_seed.go`, `cmd/simurgh/env.go`, `cmd/simurgh/warmdd.go`, `internal/isolator/*`, `internal/provisioner/{readiness,errors,simctl,provisioner}.go`, `internal/controlplane/{types,controlplane}.go`, docs (`AGENT_GUIDE`, `ARCHITECTURE`, this status + orchestrator-state)
- Non-goals held: timed B1/B2/B4/B5/B6 / `p0_rerun_driver` / `BENCH_START_AT` / XcodeBuildMCP composition (owner STOPPED); R-WP5 PATH shims; R-WP6 adapter; public session/job RPCs; broker identity/capacity rewrites

### Explicitly deferred (unpublished claims)

| Cell / claim | Status |
|---|---|
| B1-fresh / B1-reuse timed | **deferred** — do not restart |
| B2 retries timed | **deferred** |
| B4-defaults-rep | **deferred** |
| B5 dual-window | **deferred** |
| B6 third ARM-S | **deferred** |
| XcodeBuildMCP composition check | **deferred** |
| Warm-DD latency / time-to-green numbers | **unpublished** — implementation only |
| New `bench/evidence/*` publishes | **not created** |

### Acceptance commands (exact)

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

### Gate results (R-WP4 worktree)

| Gate | Result |
|---|---|
| warm-seed named 4 | **PASS** exit 0 — package 1.309s |
| `TestWritableCachesStayUnderLeaseRoot` | **PASS** exit 0 — 1.153s |
| readiness named 2 | **PASS** exit 0 — 1.212s |
| controlplane named 4 | **PASS** exit 0 — 1.153s |
| `go build ./...` | **PASS** exit 0 |
| `go vet ./...` | **PASS** exit 0 |
| `go test -race ./...` | **PASS** exit 0 (**17** packages; ipc 36.638s) |
| capacity named tests | **PASS** exit 0 — 8 tests, 1.126s |
| `TestDialDaemonSurvivesParentExit` | **PASS** exit 0 — **32.47s**; daemon **PID 70540** stable ≥30s |
| `TestReuseIfExistsLive` (`darwin_sim`) | **PASS** exit 0 — test 2.52s, package 3.747s |

### Behavioral proof (R-WP4)

1. **Warm seed hit:** matching formal key CoW-seeds lease DerivedData (`SeedOutcomeHit`).
2. **Warm seed miss:** exact field mismatch (e.g. XcodeBuild) → `SeedOutcomeMiss`, empty dest.
3. **Never overwrite:** non-empty destination → `SeedOutcomeSkippedNonEmpty`.
4. **Clone fallback:** failing clone → `SeedOutcomeFallbackCold`, empty dir restored.
5. **Writable caches:** DerivedData/ModuleCache/SwiftPM/Results/tmp under lease root; escapes + default Xcode paths rejected.
6. **Readiness success:** probe becomes ready under bound.
7. **Readiness timeout:** classified `READINESS_TIMEOUT`; optional fallback after primary deadline; no hang.
8. **Phase coverage:** aliases `acquire/build/install/launch/test/capture` recorded from real JobState timings.

### Commits (from `b811736`)

| SHA | Summary |
|---|---|
| `f6237d6` | feat(warm-seed): formal APFS compatibility keys and CoW seed/fallback |
| `49bc1b3` | feat(isolator): enforce writable caches stay under lease root |
| `41aa107` | feat(provisioner): bounded readiness polls with classified timeouts |
| `1d51667` | feat(controlplane): additive acquire/build/install/launch/test/capture timings |
| `36e1943` | docs(plans): record R-WP4 implementation-complete / benchmark-deferred |
| `6c1277e` | docs(plans): pin R-WP4 tip SHA in MASTER_BUILD_STATUS |
| `4133a4b` | docs(plans): finalize pre-review R-WP4 status |
| `25f2926` | fix(warm-seed): require complete keys and serialize seed replacement |
| `0d07d60` | fix(provisioner): bound fallback probes and preserve cancellation |
| `0b47e3d` | fix(isolator): resolve symlinks before cache confinement |
| `42174fc` | fix(controlplane): reset phase timings on retry attempts |
| `4e500f1` | test(warm-seed): stress cross-process publish and seed locking |
| *(final status commit is branch tip)* | docs(plans): close bounded R-WP4 review-fix loop |

### Public symbols / dependents

| Symbol | Dependents |
|---|---|
| `WarmSeedKey` / `WarmSeedKeyIncompleteError` | `BuildWarmSeedKeyFromArgv`; `cmd/simurgh` `runExec` |
| `SeedWarmDerivedData` / `PublishWarmSeedContext` / per-digest lock | `cmd/simurgh` `runExec`; cross-process stress helper |
| `ValidateWritableCachesUnderLeaseRoot` | `EnsureLeaseDir`; isolator tests |
| `PollReadiness` / `FallbackTimeout` / `ReadinessTimeoutError` | `execSimctlClient.WaitBooted`; `Provisioner.Provision` |
| `InstrumentedPhases` / aliases | `ControlPlane.TransitionJob` / `endPhaseLocked` |

### Model notes (R-WP4)

- Warm seed policy version: `WarmSeedPolicyVersion = "1"`.
- Shared seed layout: `~/.simurgh/warm-dd/<digest>/{DerivedData,meta.json}`.
- No public session/job CLI/RPC added.
- Broker capacity/reuse identity untouched.
- **No performance claims** from this package.

### Single bounded R-WP4 review-fix loop

All blocking/major/minor/nit findings were addressed with tests:

1. `BuildWarmSeedKeyFromArgv` returns typed
   `WarmSeedKeyIncompleteError` for missing/empty configuration and other
   required dimensions. Omitted config never aliases explicit `Debug`; both
   seed and publish cold-skip.
2. Per-digest BSD `flock`: seed holds shared lock for validation+clone;
   publish holds exclusive lock for unique staging + live replacement.
   Replacement moves the old complete tree aside under lock, atomically
   renames the staged tree live, then removes backup. Context cancellation
   while acquiring a lock is returned; descriptors unlock/close on every exit.
3. `FallbackTimeout` bounds fallback probes (default
   `min(primary timeout, 5s)`, positive). Fallback context derives from caller;
   blocking/ignoring fallback is still bounded and classified
   `READINESS_TIMEOUT`; caller `context.Canceled` remains distinguishable.
4. Cache confinement resolves symlinks through nearest existing ancestors,
   including root; not-yet-created descendants remain supported. Symlink
   escapes and default Xcode DerivedData/SwiftPM paths are rejected before
   directory creation.
5. Retried instrumented phases reset start/end/duration and aliases together.
6. `WaitBooted` timeout/cancel mapping is tested; `Provision` retains the
   existing `BOOT_TIMEOUT` error contract with the dead duplicate branch
   removed.

#### Review-fix gate proof

| Gate | Result |
|---|---|
| focused warm-seed 8 tests | **PASS** 12.236s |
| cross-process/goroutine stress `-race -count=3` | **PASS** 34.282s |
| focused isolator 4 tests | **PASS** 1.981s |
| focused readiness/provisioner tests | **PASS** 1.708s |
| focused controlplane 5 tests | **PASS** 1.695s |
| `go build ./...` / `go vet ./...` | **PASS** |
| `go test -race ./...` | **PASS** 17 packages; cmd 10.839s |
| capacity named 8 | **PASS** 1.226s |
| detach 30s | **PASS** 33.51s; PID **4277** stable |
| live `darwin_sim` reuse | **PASS** test 2.67s, package 3.887s |

## Completed packages

| Package | Commit / note | State |
|---|---|---|
| M0–M4 (v0 scaffold) | prior `master` lineage | historical complete |
| Phase A P0 harness (partial) | `593a051` STOPPED/BLOCKED | not a Phase A pass |
| **R-WP0** | `0757144` | complete |
| **R-WP1** | tip `e948d26` | complete |
| **R-WP2** | tip `23c2dce` | complete |
| **R-WP3** | tip `b811736` | complete |
| **R-WP4** | review-fix code tip `4e500f1`; final status commit is branch tip | **implementation-complete / benchmark-deferred** |

## Open failures/risks

1. Benchmark cells remain unmet by owner STOPPED directive — not a code defect in R-WP4.
2. Control-plane Session/Job state remains non-durable (R-WP3 carry-forward).
3. PATH shim / XcodeBuildMCP adapter still deferred (R-WP5 gated behind R-WP6 reassessment).

## Next package

- **R-WP5** only after R-WP6 reassessment (owner correction #3) — do not start reflexively.
- Do **not** merge/push R-WP4 from this package alone.
- Do **not** restart Phase A timed drivers unless owner directs.
