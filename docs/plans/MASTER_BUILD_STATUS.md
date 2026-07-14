# Simurgh Master Build Status

**Updated:** 2026-07-14 14:32 UTC (America/Toronto 10:32 EDT)  
**Roadmap naming:** Phase B packages use `R-WP*` (not Phase A `P0-*` / harness `WP*`).

## Baseline

- Commit: `593a051ac36539d8f76483246e3b17baf3d3f7d5` (`593a051` — Phase A STOPPED/BLOCKED handoff)
- Branch/worktree: `feat/r-wp0-baseline` @ `/Users/roshansilva/Documents/simurgh/.worktrees/r-wp0-baseline`
- Go: `go1.26.5 darwin/arm64` (satisfies `go.mod` `go 1.25.0`)
- macOS: `27.0` (Build `26A5378n`), `arm64`
- Xcode: `27.0` (Build `27A5194q`); `xcrun simctl help` exit 0
- Existing dirty files preserved: **yes** — R-WP0 worktree started clean at `593a051`; parent checkout `/Users/roshansilva/Documents/simurgh` dirty/untracked owner files left untouched (no reset/clean/stash). Observed parent status at session start included `M recap-deck.html`, `M recap-log.json`, and untracked `.cursor/`, `.serena/`, `Untitled/`, `bench/HANDOFF_FIXES_PROMPT.md`, `bench/RESULTS.md`, `bench/REVIEW_AND_NEXT_STEPS.md`, `docs/plans/CURSOR_SOL_ORCHESTRATOR_PASTE.md`, `docs/plans/LOOP_VERIFIER_PROMPT.md`, `docs/plans/SIMURGH_CLAUDE_CODE_MASTER_PROMPT.md`, `docs/plans/SIMURGH_MASTER_IMPLEMENTATION_REPORT.md`, `docs/plans/work-packages/m4.4-docs.md`, `simurgh`.
- Baseline build/test status:

| Gate | Command | Result |
|---|---|---|
| status | `git status --short` | clean (empty) at worktree start |
| log | `git log --oneline -10` | tip `593a051` … see orchestrator-state |
| go | `go version` | `go1.26.5 darwin/arm64` |
| xcode | `xcodebuild -version` | `Xcode 27.0` / `Build version 27A5194q` |
| simctl | `xcrun simctl help >/dev/null` | exit 0 |
| build | `go build ./...` | **PASS** exit 0 |
| vet | `go vet ./...` | **PASS** exit 0 |
| race | `go test -race ./...` | **PASS** exit 0 (16 packages) |
| macOS integration | `go test -race -tags darwin_sim -count=1 ./internal/provisioner/ ./internal/safety/ ./internal/reaper/` | **PASS** exit 0 |
| live darwin_sim | `-run TestProvisionRealClone\|TestSafetyLiveDeletionGuards\|TestReaperLiveReclaimAndOrphanScan` | **PASS** exit 0 (real simctl) |

**macOS suite discovery:** `docs/ROADMAP.md` cites `go test -race -tags darwin_sim ./test/integration/...`, but `test/integration/` does **not** exist. Exact search: `grep -rn '//go:build\|darwin_sim' --include='*.go' internal cmd` found three tagged files — `internal/provisioner/integration_test.go`, `internal/safety/integration_test.go`, `internal/reaper/integration_test.go` (`//go:build darwin_sim`). Those packages are the existing suite; ran them (not invented).

## Current work package

- ID: **R-WP0**
- Goal: Protect baseline/evidence; record trustworthy Mac baseline; create this status document (no product code changes)
- State: **complete**
- Files owned: `docs/plans/MASTER_BUILD_STATUS.md`, `docs/plans/orchestrator-state.md`, optional narrative-only B2 typo fix in `bench/RESULTS.md` (17/18→17/20); never `bench/evidence/`, `bench/results/`, or `docs/dogfood/`
- Acceptance commands:

```bash
git status --short
git log --oneline -10
go version
xcodebuild -version
xcrun simctl help >/dev/null
go build ./...
go vet ./...
go test -race ./...
# existing macOS suite (discovered, not invented):
go test -race -tags darwin_sim -count=1 ./internal/provisioner/ ./internal/safety/ ./internal/reaper/
```

## Completed packages

| Package | Commit / note | State |
|---|---|---|
| M0–M4 (v0 scaffold) | merged on prior `master` lineage; baseline tip is Phase A branch `593a051` | historical complete |
| Phase A P0 harness (partial) | stopped at `593a051` by **owner override** — **not a Phase A pass** | STOPPED/BLOCKED |
| **R-WP0** | this status commit on `feat/r-wp0-baseline` | complete |

## Open failures/risks

### Phase A owner override (carried forward — unmet cells remain unmet)

Phase A is **STOPPED/BLOCKED** by explicit owner directive (2026-07-14 14:25 UTC), recorded in `593a051`. Phase B proceeds **only by that override**, not because Phase A exit criteria passed. Do **not** call Phase A complete.

| # | Criterion | Status at stop |
|---|---|---|
| P0-1 | B1 ARM-S ≥ DIY success at 2 agents, or root-caused reason | **MET** |
| P0-2 | time-to-N-green + B1/B2 rerun cells | **PARTIAL** — B1 committed; **B2 retries unmet** |
| P0-3 | B6 third simurgh rep + XcodeBuildMCP composition evidence | **UNMET** |
| P0-4 | B4 rep on shipped defaults only | **PARTIAL** — code landed; **B4-defaults-rep unmet** |
| P0-5 | receipts committed | **MET** |
| P1-6 | B5 dual-window / late-acquire measurement | **UNMET** |
| P1-7 | warm-DD on Lancer | **MET** (−0.2%; do not cite toy ~30%) |

Cell status at stop: **B2 retries**, **B4 shipped-defaults**, **B5 dual-window**, **B6 third ARM-S**, **XcodeBuildMCP composition** — all unmet / not committed as complete. Do not restart benchmarks unless the owner directs.

### Product risks deferred into R-WP1 / R-WP2 (owner corrections)

1. `--reuse-if-exists` is a silent no-op in `broker.Acquire` → fold into **R-WP1**.
2. Dying daemon leaves QUEUED lease with no owner-liveness check → fold into **R-WP1/R-WP2**.
3. `ipc.Dial` auto-started daemons die with the parent shell → fold into **R-WP1/R-WP2**.

### Other

- No private Git remote — local-only; do not push.
- Warm-template latency ≥50% target remains unmet on this Mac (historical M2.2 finding).
- `docs/ROADMAP.md` path `./test/integration/...` is stale vs actual `darwin_sim` package locations.

## Benchmark evidence

- Raw evidence under `bench/evidence/` is **append-only** — R-WP0 did not modify it.
- Narrative-only correction: B2 naive failures **17/20** (3/20 success ⇒ 17 failures of 20), not 17/18 — fixed in `bench/RESULTS.md` claim table; raw JSON untouched.
- Prior launch cells under `bench/evidence/B2/B3/B4/B5/B6-*-20260713T*` stand; 2026-07-14 B2 retries cell is the blocked snapshot `bench/evidence/blocked-b2-2026-07-14T14:16:20Z.json`.
- Do not treat gitignored `bench/results/` as committed proof.

## Next package

- **R-WP1** — Implement real `ReuseIfExists` (and fold orphaned-QUEUED / daemon-dies-with-parent lifecycle fixes per owner corrections in `docs/plans/CURSOR_SOL_ORCHESTRATOR_PASTE.md`).
- Do **not** merge/push R-WP0 or start R-WP1 from this package’s commit without orchestrator dispatch.
- Gate R-WP5 behind R-WP6 reassessment (owner correction #3).
