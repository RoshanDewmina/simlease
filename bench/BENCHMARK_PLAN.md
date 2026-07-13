# Simurgh benchmark & validation plan — "with vs without"

**Version:** 1.0 (2026-07-13) · **Goal:** produce honest, reproducible,
launch-announceable numbers comparing parallel agent iOS work **with Simurgh**
vs **without**, plus feature-level validation a skeptical dev can re-run.

Ground rules (non-negotiable, they're what makes the numbers tweetable):
1. **Every published claim maps to a measured cell in this matrix.** No
   extrapolation, no cherry-picking reps.
2. **Two baselines, not one.** Beating a strawman impresses nobody:
   - **ARM-N (naive):** what agents do by default — same simulator
     (`-destination name=iPhone 17 Pro`), global DerivedData, no coordination.
   - **ARM-D (DIY):** what a careful dev scripts by hand — per-agent
     `simctl create`d sim + manual `-derivedDataPath`/module-cache flags.
     Simurgh's honest pitch vs DIY is *reliability + zero setup + cleanup*,
     not raw speed.
   - **ARM-S (Simurgh):** `acquire → exec → release`.
3. **Known negative results stay negative.** Warm templates measured *slower*
   than cold on this Mac (state file, M2.2: cold 2.147s vs warm 2.696s).
   Do NOT claim warm-template latency wins. Exclude warm templates from
   benchmark arms entirely, or report the real numbers.
4. **Quiet machine or no run.** The 2026-07-13 M4 log shows this Mac under
   real RAM pressure from stale `cursor-agent-worker` processes. Pre-flight
   (below) must pass before any timed rep counts.
5. Report medians + min/max over ≥5 reps, n stated, hardware + Xcode versions
   stated, raw JSON published in the repo.

---

## 1. The workload (identical across arms)

One "agent task" = the loop a real coding agent runs:

```
build fixture app (test/fixtures/SampleApp, per-agent variant)
→ install to its simulator → launch → assert healthy 5s
→ screenshot → verify screenshot shows OWN variant → terminate
```

**Per-agent app variant (cross-contamination detector):** each agent builds
with `OTHER_SWIFT_FLAGS='-DAGENT_<X>'`; SampleApp gains a compile-time switch
rendering a full-screen color + agent name (A=red, B=blue, C=green, D=yellow)
with an accessibility identifier `agent-badge-<X>`. A screenshot (or
`simctl`/XCTest ax query) showing the *wrong* badge = **evidence-integrity
failure** — the killer demo: naive parallel agents literally screenshot each
other's app.

Fixture change is ~15 lines of SwiftUI; harness passes the flag per agent.

## 2. Scenario matrix

| ID | Scenario | Arms | Agents | Reps | Primary metrics |
|---|---|---|---|---|---|
| B1 | Parallel feature tasks | N/D/S | 2 | 10 | per-agent success rate; collision incidents; wall-clock/agent; total wall-clock |
| B2 | Parallel, heavier | N/D/S | 4 | 5 | same |
| B3 | Solo overhead (the honest cost) | N/S | 1 | 10 | Simurgh overhead vs raw: acquire latency, cold-DerivedData penalty, total delta |
| B4 | Crash cleanup | N/D/S | 2 (kill −9 one mid-build) | 5 | orphaned booted sims + leftover dirs + stray processes after 90s (count) |
| B5 | Overload / thundering herd | N/S | 6 (capacity 3) | 3 | failures under thrash vs queued completion; total time; peak RAM |
| B6 | **Real-world: Lancer** | N/D/S | 2–3 (git worktrees of `~/Documents/command-center`) | 3 | same incident classes as B1 at real scale |

### B6 — the original problem, reproduced (this is why Simurgh exists)

B1–B5 use a toy fixture; B6 replays the actual Lancer dogfooding pain that
motivated the product:

- Each agent works in its **own git worktree of the Lancer repo** — worktrees
  sharing one global `DerivedData/Lancer-<hash>` is the original collision
  mode (`build.db` locks, module thrash) and toy apps can understate it:
  long real builds (minutes, not seconds) widen every race window.
- Workload per agent: `build_sim`-equivalent (build the Lancer app scheme for
  simulator) → install → launch → screenshot; one rep variant adds an
  affected UITest run (heavier, longer sim occupancy).
- ARM-N: agents build from their worktrees with default (shared) DerivedData
  targeting the same-named simulator. ARM-D: manual per-worktree flags +
  pre-created sims. ARM-S: `simurgh exec` per lease.
- **XcodeBuildMCP composition check** (one rep, ARM-S): drive the build via
  XcodeBuildMCP inside a lease and verify the isolated paths are honored
  (session defaults / flag passthrough). If they aren't, that's a real
  compatibility finding to fix before launch, not to hide.
- Watch for port contention from Lancer's local relay/daemon processes —
  Simurgh v0 has no port reservation (dropped from the original report's
  wish list); if B6 surfaces it, log as a v0.2 requirement with evidence.
- Safety: worktrees are disposable (`git worktree add` from a clean master,
  removed after), builds only — no pushes, no Lancer state mutation, no
  running the relay against production pairing state.
- B6 is also the launch story: "we used it to build our own app" beats any
  synthetic benchmark. Keep the raw logs.

**Collision incident classification (count separately, script-detected):**
`build.db`/DerivedData contention errors · install-over-each-other (bundle
replaced mid-run) · wrong-variant screenshot (evidence integrity) · launch/
destination failures · post-run residue (devices/dirs not cleaned).

**Expected honest shape of results** (verify, don't assume): B1/B2 ARM-N
shows real failures/flakes, ARM-D mostly works but leaves residue and needed
hand-written setup, ARM-S ~100% with zero residue; B3 shows Simurgh costs a
few seconds (acquire ~2s + cold DerivedData on first build) — publish that
number, it buys credibility; B4 is Simurgh's clearest categorical win
(reaper vs orphans); B5 shows queueing beats thrash.

## 3. Statistics & reporting

- n per cell as in matrix; medians + min–max; no means-only reporting.
- Success rates with raw counts (e.g. "18/20 agent-tasks failed at least one
  step" not "90%").
- Record per rep: timestamps per step, exit codes, incident classifications,
  `simurgh status --json` snapshots (ARM-S), `vm_stat` + thermal before/after.
- Output: `bench/results/<ISO-timestamp>/{run-*.json, summary.md}`;
  `summary.md` auto-generated with the matrix filled in.
- Hardware/software provenance block: Mac model, RAM, macOS, Xcode,
  runtime version, Simurgh commit.

## 4. Pre-flight checklist (scripted, `bench/preflight.sh`)

1. No stray `cursor-agent-worker`/agent processes (list & require owner
   confirmation to proceed if found).
2. Free+inactive RAM ≥ 8GB; thermal nominal (`simurgh status --json` pressure
   block green).
3. `xcrun simctl list devices` contains no `simurgh-clone[*]` leftovers.
4. Xcode + runtime versions match the provenance block.
5. Screen locked / no user activity during timed reps (reduce noise).
6. Order of arms randomized per rep (avoid cache-warmth bias toward later
   arms); DerivedData state reset between reps per arm's own semantics
   (naive keeps global DD warm — that's its real behavior; note it).

## 5. Feature-level validation (beyond the benchmark)

Already covered by the repo's suite (don't duplicate — cite it): exclusion,
queueing, reclamation, safety guards, pressure gating (`go test -race`, the
`darwin_sim` integration suite, M3.3 dogfood).

Owner spot-checks still outstanding (do these once, manually):
1. **Two real Claude Code windows** (not subagents) each in a project with
   `simurgh init` run, MCP registered, told to build+launch+screenshot —
   the literal M3.3 fidelity gap noted in the dogfood log.
2. `--rm` behavior from a real agent session (lease dies with the session).
3. `simurgh doctor` + `init` on a second Mac / fresh user account (fresh-Mac
   quickstart claim).

## 6. Launch-claim mapping (write the tweet from this table only)

| Claim template | Source cell |
|---|---|
| "N parallel agents on one Mac, zero collisions" | B1/B2 ARM-S incidents=0 |
| "Without it: X/Y parallel tasks hit collisions" | B1/B2 ARM-N counts |
| "Agents literally screenshot each other's app without isolation" | B1 ARM-N wrong-badge count + the two PNGs |
| "Kill an agent mid-build: everything cleaned in <90s; without: Z orphans" | B4 |
| "6 agents, capacity 3: queued & finished vs thrash" | B5 |
| "Cost: ~Xs overhead per task solo" | B3 (publish it yourself before someone else does) |
| "Dogfooding found a real reclaim-race bug before launch" | docs/dogfood/2026-07-13.md (great thread material) |

## 7. Harness layout (implementation spec)

```
bench/
├── BENCHMARK_PLAN.md      (this file)
├── preflight.sh
├── lib.sh                 # timing, JSON emit, incident detectors
├── workload.sh            # one agent task; env-driven (ARM, AGENT, UDID?, flags?)
├── arm_naive.sh  arm_diy.sh  arm_simurgh.sh
├── kill_midbuild.sh       # B4 helper
├── run.sh                 # matrix driver: --scenario B1..B5 --reps N
├── report.py              # python3 stdlib: results → summary.md
└── results/               # gitignored raw output, committed summaries
```

Acceptance for the harness itself (before any published numbers):
- `run.sh --scenario B1 --reps 1 --arms S` completes green on a quiet machine;
- incident detectors proven by *seeded* faults (deliberately run two naive
  agents on one device and confirm the detector fires);
- report.py reproduces summary.md byte-identically from the same JSON.
```
