# PASTE — build & run the Simurgh benchmark suite

Paste everything below into a Claude Code session started in `~/Documents/simurgh`.

---

You are implementing and executing the Simurgh benchmark per
`bench/BENCHMARK_PLAN.md` — read it in full first; it is the spec and its
ground rules are binding (two baselines, negative results stay negative,
quiet-machine pre-flight, claim↔cell mapping). Also read
`docs/plans/orchestrator-state.md` (2026-07-13 entries) for known machine
context: stale `cursor-agent-worker` processes have previously held this Mac
below the RAM pressure floor — pre-flight will catch this; ask the owner
before killing anything that isn't yours.

## Sequence

1. **Fixture variant:** add the compile-flag agent badge to
   `test/fixtures/SampleApp` (plan §1). Full-screen color + agent name +
   accessibility id `agent-badge-<X>`. Verify each variant builds and the
   badge is queryable via accessibility.
2. **Harness:** implement `bench/` per plan §7. Bash + jq + python3 stdlib
   only. Every timed step wraps in monotonic timing; every failure classified
   per plan §2's incident taxonomy.
3. **Detector proof:** seed real faults (two naive agents, one device) and
   show each incident detector fires. No detector proof → no benchmark run.
4. **Pre-flight** (plan §4) — if it fails, stop and report to the owner; do
   not run timed reps on a loaded machine.
5. **Execute matrix** B1→B6. Between reps: full cleanup verification (no
   simurgh-clones, no stray daemons, DerivedData state per arm semantics).
   B6 (Lancer real-world) additional rules: disposable worktrees off clean
   `master` of `~/Documents/command-center`, build/test only — never push,
   never touch relay pairing state, remove worktrees after; the XcodeBuildMCP
   composition check is a first-class deliverable (works / doesn't, with
   evidence either way); any port-contention incidents get logged as v0.2
   requirements.
6. **Report:** generate `bench/results/<ts>/summary.md`; then write
   `bench/RESULTS.md` — the human summary with the plan §6 claim table filled
   in with real numbers, including the unflattering ones (B3 overhead, and
   warm-template latency stays out per plan ground rule 3).

## Rules

- Gates: `go build ./... && go vet ./... && go test -race ./...` stay green if
  you touch Go code (you generally shouldn't need to — this is harness work).
- The `internal/safety` invariant applies to the harness too: cleanup scripts
  may only delete `simurgh-*`-named devices it created for ARM-D/N
  (name them `bench-*` and only delete `bench-*`), and paths under `bench/results/`.
- Never fabricate or "estimate" a number. A rep that broke is a broken rep,
  logged as such.
- Evidence discipline: every number in RESULTS.md traces to a JSON file in
  the same commit.

## Done-bar

`bench/RESULTS.md` committed with: full B1–B5 medians table, incident counts
per arm, provenance block, links to raw JSON, the two cross-contamination
PNGs from ARM-N (if reproduced), and a "limitations" section (single machine,
single fixture app, n per cell). Plus the three owner spot-checks from plan §5
listed as a checklist for the owner to run manually.
