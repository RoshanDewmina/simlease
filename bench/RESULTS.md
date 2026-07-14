# Simurgh Benchmark — Results

**Run window:** 2026-07-13, ~10:30 AM – 9:20 PM (America/Toronto), single continuous session.
**Spec:** `bench/BENCHMARK_PLAN.md` v1.0. **Harness:** `bench/` (this commit tree).

This document is intentionally long. It is written for someone reviewing the work who
was not present for the session — every number traces to a JSON file, every bug has a
root cause, and every place the plan's ground rules were bent or a scenario fell short
of its target `n` is called out explicitly rather than smoothed over.

---

## Provenance

| | |
|---|---|
| Hardware | Mac16,13 |
| RAM | 16 GB |
| CPUs | 10 |
| macOS | 27.0 (build 26A5378j) |
| Xcode | 27.0 |
| Simurgh commit | `4447e3c` (87 commits on `master`) |
| Fixture | `test/fixtures/SampleApp` (compile-flag agent badge, A–F) |
| B6 real-world app | `~/Documents/command-center` (Lancer), `master` @ `0e0b9eba` |

**A note on the RAM figure:** 16 GB is genuinely tight for this workload. The plan's own
preflight gate (§4: free+inactive ≥ 8 GB) never once passed on this machine during the
entire session — it was deliberately disabled (`BENCH_RAM_FLOOR_GB=0`) per an explicit
owner decision, on the reasoning that macOS pages to compressor/swap under pressure
rather than failing outright, and that gating on an 8 GB floor calibrated for a
different machine wasn't the right test. That decision is real and is reflected
honestly in results below — this Mac did hit genuine memory and disk pressure more than
once during the run, and those episodes are reported as findings, not hidden.

---

## Claim table (BENCHMARK_PLAN.md §6) — filled with real numbers

| Claim template | Source cell | Real result |
|---|---|---|
| "N parallel agents on one Mac, zero collisions" | B1/B2 ARM-S incidents=0 | **B2 achieved this**: 20/20 (100%), zero incidents, zero residue, at 4 concurrent agents. B1's first attempt did not (harness bug, see below); the corrected re-run hit 17/20 (85%), not zero incidents — see "B1: not a clean win" below. |
| "Without it: X/Y parallel tasks hit collisions" | B1/B2 ARM-N counts | **B1 naive: 10/20 failed** (real cross-contamination — one agent's screenshot showed another agent's badge). **B2 naive: 17/18 failed** (85%+ failure at 4 agents — naive gets worse, not better, as concurrency rises). |
| "Agents literally screenshot each other's app without isolation" | B1 ARM-N wrong-badge count + two PNGs | **Confirmed with real evidence.** `run-B1-naive-rep2-A.json`: expected `agent-badge-A`, observed `agent-badge-B`. Screenshots exist alongside every run JSON (`bench/results/<ts>/*.png`, gitignored — not force-added to this commit; see Limitations). |
| "Kill an agent mid-build: everything cleaned in <90s; without: Z orphans" | B4 | **True for DIY, true for Simurgh, in opposite directions.** DIY orphaned its killed victim's simulator in **5/5 reps** (`device_still_present: true`, every time). Simurgh's daemon watcher reclaimed the killed lease in **4/4 cleanly-measured reps** (`lease_state: reclaimed`, device gone) — rep 1's victim-info capture raced the kill and came back empty (harness gap, not a behavior signal). |
| "6 agents, capacity 3: queued & finished vs thrash" | B5 | **Half true.** Naive thrashed as expected (1/18 success, 5.6%). Simurgh's capacity limit correctly gated 3-active/3-queued and never leaked isolation (all "wrong_variant" hits were timeouts, `observed: none` — never a real cross-agent badge) — but only **10/18 (55.6%) actually finished**, with tail latencies up to 843s (14 min). This is not the clean "queueing beats thrash" story the plan hoped for. Reported as-is; see "B5: the unflattering result" below. |
| "Cost: ~Xs overhead per task solo" | B3 | **Naive 7.33s vs Simurgh 48.67s median, both 10/10 success, zero incidents.** ~41s real solo overhead. Root cause identified and partially addressed (see Warm-DD feature below) — mostly a cold-vs-warm-DerivedData tax that DIY pays almost as much of (38.64s in B1), not something unique to Simurgh's mechanism. |
| "Dogfooding found a real reclaim-race bug before launch" | this run | **True, and it's a good story.** See "Bug 1: stuck lease queue" below — found under real 4-agent load, root-caused, fixed with a regression test, re-verified live. |

---

## B1–B6: what actually happened, scenario by scenario

### B1 — Parallel feature tasks (2 agents, N/D/S, 10 reps)

**First attempt was discarded.** ARM-S showed only 10/20 success with every failure's launch log
completely empty. Root cause: the daemon's boot-readiness check (`WaitBooted`) only polls
`simctl` device state until `"Booted"` — weaker than `simctl bootstatus`'s full readiness
check (SpringBoard etc. actually up). Under real 2-agent concurrent load that gap was wide
enough to race installs/launches against a not-yet-ready simulator. **Fixed**
(`bench/workload.sh` now runs its own `bootstatus` wait before install, for every arm,
belt-and-braces even though the daemon already waits) and re-run clean.

**Clean re-run** (`bench/results/20260713T154936Z/`):

| Arm | n | Success | Median | Incidents |
|---|---|---|---|---|
| naive | 20 | 10/20 (50%) | 8.06s | `launch_destination_failure`=10, `wrong_variant`=10 — real cross-contamination |
| diy | 20 | 20/20 (100%) | 38.64s | none |
| simurgh | 20 | 17/20 (85%) | 81.43s | `launch_destination_failure`=3, `wrong_variant`=3 |

**B1: not a clean win.** Even after the boot-readiness fix, ARM-S landed at 85%, not 100%.
Investigation of the 3 failures (`run-B1-simurgh-rep6-A/B.json`, `rep8-B.json`) showed
build/install durations 2–3× elevated (46s/29s vs the ~20–25s/~8s typical) — consistent
with real CoreSimulator contention when 2 agents hammer `simctl` simultaneously, pushing
the fixed 5-second post-launch health window too tight during peak load spikes. This
affects the OS layer all arms share; naive/diy simply didn't happen to hit it this run.
Reported honestly rather than adjusting the window to make the number look better.

### B2 — Parallel, heavier (4 agents, N/D/S, 5 reps)

`bench/results/20260713T171721Z/`:

| Arm | n | Success | Median | Incidents |
|---|---|---|---|---|
| naive | 20 | 3/20 (15%) | 8.54s | `install_clobber`=5, `launch_destination_failure`=27, `unclassified_failure`=4, `wrong_variant`=17 |
| diy | 20 | 20/20 (100%) | 94.34s | none |
| simurgh | 20 | 20/20 (100%) | 88.19s | **none** |

**This is the plan's cleanest win.** Naive collapsed harder than B1 as concurrency rose
(50%→15% failure), with real data corruption (`install_clobber`) on top of cross-contamination.
Simurgh hit 100% with zero incidents under real 4-agent/capacity-2 pressure — validated
*after* finding and fixing Bug 1 below, which was originally surfaced by this exact scenario.

### B3 — Solo overhead, the honest cost (1 agent, N/S, 10 reps)

`bench/results/20260713T175107Z/`:

| Arm | n | Success | Median | Min | Max |
|---|---|---|---|---|---|
| naive | 10 | 10/10 | 7.33s | 6.89s | 9.79s |
| simurgh | 10 | 10/10 | 48.67s | 39.80s | 54.99s |

Both arms 100% success, zero incidents. Real, unsoftened overhead: **~41s median**.
Step breakdown from this data (median across the 10 simurgh reps): acquire ~1.3s, build
~15s, boot-readiness wait ~10s (the harness's own defensive check — partially
self-inflicted overhead), install ~11s (variable, 8–19s), launch 5s (fixed harness
window), screenshot ~3s.

### B4 — Crash cleanup, the categorical win (2 agents, kill mid-build, N/D/S, 5 reps)

`bench/results/20260713T180241Z/`:

| Arm | n | Success | Median | Incidents |
|---|---|---|---|---|
| naive | 5 | 2/5 (40%) | 7.91s | `launch_destination_failure`=1, `wrong_variant`=3 |
| diy | 5 | 4/5 (80%) | 60.41s | `launch_destination_failure`=1, `wrong_variant`=1 |
| simurgh | 5 | 5/5 (100%) | 39.85s | none |

**Residue after 90s per arm** (the plan's actual primary metric here — not the survivor's
pass/fail above, but what's left behind by the *killed* agent):

- **naive**: no lease concept, no device to orphan (shared persistent sim) — the real
  cost shows up as corrupted `build.db` state affecting the survivor, not as leftover
  processes.
- **DIY**: `device_still_present: true` in **5/5 reps**. The killed victim's SIGKILL
  bypasses its own trailing `simctl shutdown` line — nothing is left to run it.
- **Simurgh**: `lease_state: reclaimed`, `device_still_present: false` in **4/4 cleanly-
  measured reps**. Rep 1's victim-info capture raced the very fast kill and came back
  empty (harness limitation in `BENCH_LEASE_INFO_FILE` timing, not a behavior signal).

To make this test meaningful at all required two real design decisions during
implementation: (1) `kill_midbuild.sh` originally targeted only the `xcodebuild` leaf
process — killing just that left every arm's wrapper script alive to run its own normal
cleanup regardless of arm, making all three arms look artificially clean. Redesigned to
SIGKILL the entire agent process tree, simulating a genuinely crashed session. (2) Without
`--rm`/a short inactivity timeout, Simurgh's watcher never checks owner-PID liveness at
all for a lease not marked `ReleaseOnPIDExit` — the victim agent's `--inactivity-timeout
20s` override was added specifically so the daemon's real reclaim mechanism (inactivity
timeout, not PID-death) got a chance to fire within the 90s measurement window.

### B5 — Thundering herd (6 agents, capacity 3, N/S, 3 reps)

`bench/results/20260713T185910Z/`, peak-RAM log in the same directory:

| Arm | n | Success | Median | Max | Incidents |
|---|---|---|---|---|---|
| naive | 18 | 1/18 (5.6%) | 6.82s | 19.29s | total thrash — real cross-contamination, `install_clobber`, 38 `launch_destination_failure` |
| simurgh | 18 | 10/18 (55.6%) | 270.19s | **843.30s (14 min)** | 8 `launch_destination_failure` |

Peak RAM observed (min free+inactive during each rep): naive 4.16–4.55 GB, simurgh
3.5–3.73 GB — genuinely tight on this 16 GB machine.

**B5: the unflattering result.** Naive thrashed exactly as predicted. Simurgh's capacity
gate (`SIMURGH_CAPACITY=3`, forced via env override — see "why capacity 3" note below)
correctly limited concurrent ACTIVE leases and correctly queued the other 3, but only
55.6% of agent-tasks actually completed within the harness's fixed 5-second launch-health
window, with individual completions stretching to 14 minutes under real 6-agent CPU/IO
contention. **One important nuance that belongs alongside this number**: every one of
Simurgh's `wrong_variant` hits was `observed: none` (a timeout — the badge simply never
appeared in time), never a real wrong-agent badge. Naive's failures include actual
cross-agent badge leakage (data corruption). So the isolation *guarantee* held even under
this overload; what degraded was *throughput*, not *correctness*. Both facts are true and
both are reported — this is not spun as a win.

**Why capacity 3, not an auto-computed value:** `SIMURGH_CAPACITY` is a new env override
(added to `cmd/simurgh/daemon.go` for this benchmark) forcing the daemon's capacity
regardless of what `ComputeCapacity(numCPU, ram)` would auto-derive on this Mac (very
likely 4, the hard cap, given 10 CPUs / 16 GB RAM). Without the override, B5 could not
reproduce the plan's literal "6 agents, capacity 3" test condition.

### B6 — Real-world: Lancer (2 agents, real git worktrees of `~/Documents/command-center`, N/D/S)

This is the scenario the whole project exists for, and it is the one that ran into the
most real infrastructure trouble — reported in full below rather than glossed over.

**Final tallies** (naive: `bench/results/20260713T200959Z/`; diy: three separate runs,
`20260713T211221Z` + `20260713T212218Z` + `20260713T213216Z`; simurgh: two separate runs,
`20260713T214256Z` + `20260713T215145Z` — see "Why B6 is split across so many directories"
below):

| Arm | n | Reps | Success | Median | Min | Max | Incidents |
|---|---|---|---|---|---|---|---|
| naive | 6 | 3/3 | 4/6 (66.7%) | 576.42s | 321.78s | 589.91s | `launch_destination_failure`=5 |
| diy | 6 | 3/3 | 5/6 (83.3%) | 476.23s | 468.37s | 507.27s | `launch_destination_failure`=1 |
| simurgh | **4** | **2/3** | 2/4 (50%) | 641.61s | 491.93s | 790.33s | `launch_destination_failure`=2 |

**Simurgh's n=4, not the planned n=6 (3 reps × 2 agents): the third simurgh rep was not
run.** After several hours of real build time and three separate infrastructure problems
(below), the session's time budget was explicitly renegotiated with the owner rather than
pushed to completion silently. This is stated here as a real limitation, not implied to
be a full dataset.

**Every one of these builds is real** — the real Lancer scheme, real SPM dependency
resolution (SwiftTerm, swift-nio-ssh, Sentry, GRDB, Citadel, and others), in disposable
git worktrees checked out at `master`'s last commit (`0e0b9eba`, detached — the primary
checkout's own uncommitted working-tree changes were never touched, never pushed). Build
times alone ran 260–500s per agent — this is what "long real builds widen every race
window" (BENCHMARK_PLAN.md §2) actually looks like, not a toy-fixture approximation.

**Three real infrastructure bugs, found and fixed during this run, before any B6 number
was reported as real:**

1. **`workload_lancer.sh` was missing its execute bit.** Created via a file-write tool
   (not `chmod +x`'d like every other bench script). Every single B6 rep in the first
   attempt failed instantly on `Permission denied` — zero real data, caught before
   anything was reported. Fixed, committed (`8ff6c75`).
2. **`Lancer.xcodeproj` doesn't exist in a fresh worktree.** It's gitignored in
   command-center (`*.xcodeproj/`) and generated on demand via `xcodegen` — same pattern
   as this project's own `SampleApp` fixture, missed on the first pass. Every rep failed
   instantly with `does not exist`. Fixed by running `xcodegen generate` inside each
   worktree immediately after creation (`4447e3c`).
3. **Real disk exhaustion mid-run.** This Mac's actual writable APFS volume is much
   smaller than its nominal disk size. DIY's per-agent scratch DerivedData
   (`bench/results/.scratch/b6-diy/<agent>-<run-id>/`) is deliberately *not* cleaned
   between reps within one `run_b6.sh` invocation (mirroring the toy-fixture DIY arm's
   "leaves residue" cost story) — but across the *several* separate invocations this
   session needed (see below), that scratch directory accumulated roughly **40 GB** of
   real Lancer DerivedData across abandoned/restarted attempts, at one point genuinely
   exhausting available disk mid-build (`No space left on device`, both agents in one rep
   failed with an incomplete JSON — only a `terminate` step recorded, no build/install/
   launch data at all). Recovered by manually deleting the completed-and-already-recorded
   scratch directories. **This is a real harness gap, not fixed in code**: unlike
   `run.sh`'s startup sweep for `bench-*` simulators, nothing analogous exists yet for
   `bench/results/.scratch/`. Logged as a v0.2 requirement.

**Why B6 is split across five separate results directories instead of one clean run:**
three consecutive background-task executions of the full `run_b6.sh --reps 3 --arms
D,S` were killed externally mid-run, with no error from the harness itself and no
correlation with disk space or memory pressure (verified healthy at the time of each
kill). The first kill correlated with — and was likely *caused* by — this agent's own
`ScheduleWakeup` polling calls, which the tool's own documentation explicitly warns
against for exactly this reason ("harness-tracked work finishes, you are re-invoked
automatically" — polling is not just redundant but was, on inspection, the more likely
explanation for background-task interruption in this environment). The second kill
happened with zero polling in flight, disproving that as the sole cause. Root cause was
not fully isolated. **Workaround**: the remaining diy (3 reps) and simurgh (2 of 3 reps)
were each run as individual *foreground* calls (`run_b6.sh --reps 1 --arms X`, one rep at
a time), which never got killed. This is why diy/simurgh's data lives across five
separate timestamped directories rather than one — each foreground call is its own
`run_b6.sh` invocation with its own results directory. The data itself is exactly as real
and as trustworthy as a single continuous run would have produced; only the harness's own
plumbing needed to change mid-session.

**XcodeBuildMCP composition check (plan §2, "first-class deliverable"): not run.**
Explicitly descoped along with B6 simurgh's third rep when the session's time budget was
renegotiated. This remains a real gap in the deliverable — recorded honestly rather than
claimed as done.

---

## Bugs found and fixed during this benchmark

This section is arguably the most valuable output of the whole exercise — dogfooding
under real concurrent load surfaced problems that unit tests and single-agent manual
testing had not.

### Bug 1: daemon lease queue could get stuck forever under transient RAM pressure

**Found by:** B2 (4-agent scenario). Two leases sat in state `QUEUED` for 20+ minutes
with the daemon reporting `active: 0` and capacity fully idle.

**Root cause:** `Broker.Release()` triggers exactly one promotion attempt for the next
queued lease (`tryBeginNextLocked` → `canGrantLocked` → `pressureOK()`). If that one
attempt happens to land during a transient RAM-pressure dip (very plausible under 4
concurrent Xcode builds), the promotion silently fails and **nothing else ever retries
it** — not on a timer, not on the next unrelated Acquire/Release. The queued lease waits
forever even after pressure fully recovers.

**Fix:** `Broker.RetryQueues()` (new method, `internal/broker/broker.go`) re-attempts
promotion for every non-empty queue; wired into the daemon's existing 5-second reclaim-
watcher ticker (`internal/daemon/daemon.go`). Regression test
(`TestRetryQueuesPromotesAfterTransientPressureDip`) reproduces the exact race — pressure
false during Release, true again after — and asserts the lease stays `QUEUED` without the
fix and promotes to `ACTIVE` with it. `go test -race ./...` clean.

**Verified live:** re-ran B2 after the fix — 20/20 (100%) simurgh success, zero
incidents, and B5's 6-agent scenario (much heavier queue pressure) never reproduced a
stuck queue afterward.

### Bug 2: boot-readiness gap under concurrent load (harness-side, but real)

Covered above under B1. `internal/provisioner`'s `WaitBooted` polls simctl device state
only; `bench/workload.sh` now additionally waits on `simctl bootstatus` (the stronger
readiness check) before every install, for every arm. This is a harness fix, not a
Simurgh product fix — but it's real, it was blocking real data, and it's the direct
reason B1 needed two attempts.

### Feature: same-path DerivedData seeding via APFS clonefile

**Motivated by:** B3's real, unflattering ~41s solo overhead number. Investigation showed
most of that gap is a cold-vs-warm-DerivedData tax paid by *any* isolation approach — DIY
pays nearly the same cost (38.6s in B1) as Simurgh (48.7s), so the naive comparison (7.3s,
warm/incremental) was measuring something different from "the cost of isolation."

**Validated before writing product code** (a deliberate departure from M2.2's earlier
warm-*simulator*-template work, which assumed a latency win and then found none): `cp -Rc`
(APFS clonefile, copy-on-write, no data duplication) seeds a real DerivedData tree in
under a second; a real `xcodebuild` into a clonefile-seeded cache ran 2.9s vs 4.2–4.5s
cold on the toy fixture — a real, reproduced ~30% cut, not a repeat of M2.2's "assumed win
that didn't reproduce."

**Scoped deliberately narrow, by owner decision**: seeding is keyed on the exact absolute
`-project`/`-workspace` path + `-scheme`, so it only ever helps repeated builds of the
*same* checkout (e.g. one agent's repeated acquire/build/release cycles). It **never**
applies across B6's git worktrees, since each has a distinct path — zero cross-
contamination risk, but also zero measured benefit there yet. Implemented in
`cmd/simurgh/env.go`'s `runExec` (client-side, where `-project`/`-scheme` were already
being parsed for flag injection) plus `cmd/simurgh/warmdd.go`; 5 new unit tests, full
suite green. **Measured on Lancer (P1-7, 2026-07-14):** cold build 318.2s vs warm
seeded build 319.0s (`warm_speedup_percent: -0.2`) on the primary
`~/Documents/command-center` checkout — clonefile seeding works mechanically, but
Lancer's incremental graph dominates and the toy ~30% win does **not** reproduce.
Source: `bench/evidence/warmdd-lancer-20260714T033925Z.json` (committed).

### Fix: test suite was not CPU-bound, it was walking 26,000 files it shouldn't

Raised as a direct question mid-session ("can tests run faster, we have hardware
headroom"). `go test -race ./...` was taking 34.3s cold. Root cause:
`TestNoEmptyErrorTripleLiteralsInRepo` (`internal/errortriple`) walks the entire repo
tree via `filepath.WalkDir`, which has no concept of `.gitignore` — this session's own
real bench runs had left 26,000+ files under `bench/results/`, and walking that dominated
the package's runtime for zero `.go` files examined. Fixed by skipping `bench/results`,
`bench/.bin`, `.worktrees`, and `__pycache__` (mirroring the existing `SampleApp`-under-
`test/fixtures` skip). Regression test plants a bait empty-literal `NewError` call inside
each bulk directory and asserts it's never examined, so a future silent regression fails
loudly instead of just getting slow again. **Result: 34.3s → 6.2s** (5.5×), and
`internal/broker`/`internal/ipc`'s own tests also sped up meaningfully (9.4s→3.3s,
14.5s→4.3s) — apparently from reduced disk I/O contention while running concurrently with
the repo-walking test, not from any change to their own code.

---

## Limitations

- **Single machine.** All numbers are from one Mac16,13, 10 CPU / 16 GB RAM, macOS 27.0.
  This machine ran genuinely tight on memory (never once cleared the plan's 8 GB
  preflight floor) and, during B6, genuinely tight on disk. Results — especially
  absolute latencies and B5's throughput-under-load numbers — should not be assumed to
  transfer to a machine with materially more headroom without re-measuring.
- **Two toy-fixture app variants only** (SampleApp, a ~50-line SwiftUI app) for B1–B5,
  plus one real app (Lancer) for B6 at reduced sample size.
- **B6 simurgh arm is n=4, not the planned n=6** (2 of 3 reps completed). The
  XcodeBuildMCP composition check was not run. Both were explicitly descoped, not
  silently dropped.
- **B4's residue-after-90s check has a known gap**: rep 1's victim-lease-info capture can
  race a very fast kill and come back empty, undercounting the clean-measurement sample
  by one rep out of five.
- **DIY's scratch DerivedData accumulates unbounded across separate `run_b6.sh`
  invocations** (~40 GB observed this session) — no automatic sweep exists yet, unlike
  `bench-*` simulators which `run.sh` cleans at startup. Logged as a v0.2 requirement.
- **Naive arm's "stray process" detector for B4 has a false-positive pattern**: its
  `pgrep -fl xcodebuild` check matches the unrelated `xcodebuildmcp` MCP server process
  name (substring match). Findings from that specific detector should be manually
  cross-checked, not taken at face value.
- **The recurring "5-second launch-health-window too tight under real load" finding**
  (B1, B5, B6) is a fixed harness measurement choice, identical across arms per the
  plan's own workload definition (§1) — it is not adjusted mid-benchmark to make any
  arm's number look better, but it does mean absolute failure counts under heavy
  concurrent load partly reflect this specific SLA choice rather than an unconditional
  launch-time ceiling.
- **The three B6 background-task kills are not fully root-caused.** The workaround
  (foreground, one-rep-at-a-time execution) reliably avoided the problem, but *why*
  background execution specifically failed for this one long-running scenario — after
  working correctly for every earlier B1–B5 background run in the same session — remains
  unexplained.
- **Raw JSON evidence (screenshots, per-rep JSON) is gitignored** (`bench/.gitignore`)
  and not force-added to this commit. The numbers in this document were read directly
  from those files during the session; re-running the harness will regenerate comparable
  (not identical) data, since machine load conditions are not reproducible byte-for-byte.

---

## Owner spot-checks still outstanding (BENCHMARK_PLAN.md §5 — do these manually)

- [ ] Two real Claude Code windows (not subagents), each with `simurgh init` run, MCP
      registered, told to build+launch+screenshot — the literal M3.3 fidelity gap.
- [ ] `--rm` behavior from a real agent session (lease dies with the session).
- [ ] `simurgh doctor` + `init` on a second Mac / fresh user account.

## Follow-up work implied by this run (not yet scheduled)

- Sweep `bench/results/.scratch/` the same way `run.sh` already sweeps `bench-*`
  simulators, to stop DIY's B6 scratch DerivedData from accumulating unbounded.
- Root-cause the background-task kill pattern from B6, or at minimum document the
  foreground-one-rep-at-a-time workaround as the recommended execution mode for any
  future B6 run.
- Fix the B4 naive-arm stray-process detector's false-positive match on `xcodebuildmcp`.
- ~~Measure warm-DD on Lancer~~ — done; **-0.2%** (no speedup). Do not cite the toy ~30%
  as the real-app payoff.
- Run B6's simurgh arm to its full planned n=6, and the XcodeBuildMCP composition check.

---

# v2 addendum — P0 blocker session (2026-07-13 evening)

Scope: `bench/REVIEW_AND_NEXT_STEPS.md` P0-1…P0-5 (+P1-6/8 groundwork), run in
worktree branch `claude/benchmark-blockers-2026-07-13-37773c`. Everything below
traces to committed code, committed evidence (`bench/evidence/`), or the
2026-07-13 raw JSONs; nothing is estimated.

## What is now closed

### P0-5 — receipts committed (`bench/evidence/`)

Per-cell scrubbed JSONs for every cell cited above (320 files), the two
wrong-badge PNGs, B4's residue log, B5's peak-RAM log, and the provenance
block. `python3 bench/report.py bench/evidence/<cell>` regenerates each
committed summary from the committed JSONs. Scrub = usernames only
(`/Users/<name>` → `/Users/OWNER`); verified free of token-like strings.

### P0-4 — tested behavior is now shipped behavior (code landed; confirming rep pending machine)

The B4 story relied on a bench-only `--inactivity-timeout 20s`. Now:

- **MCP `lease_acquire` defaults to session-bound** (`releaseOnPIDExit=true`
  against the MCP server process, which lives exactly as long as the agent
  session). `detach=true` opts out; `inactivity_timeout` is now exposed.
- **CLI `--rm` was broken as shipped**: it bound the lease to the one-shot
  `acquire` process's own PID, which exits immediately after printing the
  grant — every `--rm` lease was reclaimable within one watcher tick. It now
  binds to the acquire process's parent (the invoking shell/agent session).
- init snippet + AGENT_GUIDE document the defaults.

Full gates green (build / vet / `test -race` all 15 packages / staticcheck).
The one-rep B4 confirmation run with only shipped defaults is queued behind
the machine gate below; the bench harness now uses `--rm` as its victim
default (`BENCH_B4_VICTIM_FLAGS` overrides).

### P0-1 groundwork — root cause of the B1 ARM-S failures (from the existing JSONs)

The v1 "shared CoreSimulator contention" wording was under-evidenced. The
per-step data says:

- rep6 A and rep6 B failed **together, in the same rep**, with builds at
  46.8s/47.1s (~1.8× the ~27s typical) — a machine-wide load episode, not a
  per-agent effect.
- rep8-B failed with fully normal step timings (build 28.8s, install 20.7s).
- All three failures share one signature: a completely **empty
  `simctl launch --console-pty` log at the 5s deadline** — not a failed step,
  a blown fixed-latency SLA.
- Acquire was never the problem: 1.3–3.8s across all 20 tasks. The fresh
  clone+boot cost shows up instead as post-acquire settling (boot_wait 6–27s
  variance).

B6's two ARM-S failures have the identical shape (one rep, both agents,
756s builds vs 462s in the clean rep, Lancer not resident in launchctl within
5s). The rerun instrumentation (`time_to_badge_ns`, DIY provision timing,
per-rep loadavg) exists precisely to quantify the margin distribution;
"CoreSimulator contention" is retired as a conclusion until data shows it.

### P0-2 — retry-to-green metric (implemented; cells pending machine)

`run.sh --retries K` retries a failed agent-task under the same arm/rules
(cap 3), retry latency included; per-rep wall-clock-to-all-N-green,
per-agent attempts, and first-try-green counts land in `green-log.jsonl` and
report.py's new time-to-N-green table. Verified live on a smoke rep (agent A
failed attempt 1, green on attempt 2, rep all-green in 313s with 3/6 attempt
budget used).

### Product bugs found while closing P0-1 (to file, deliberately not fixed pre-launch)

1. **`--reuse-if-exists` is a silent no-op.** Parsed by the CLI, carried over
   the wire into `broker.AcquireRequest`, never read by `broker.Acquire`.
   The harness reuse mode therefore holds one lease per agent across reps
   (how real agents iterate anyway) and passes the flag for intent.
2. **Dying daemon orphans QUEUED leases.** A `--wait` acquire whose daemon
   exits gets a bare `INTERNAL/context canceled` (ipc serves requests off the
   daemon root context) and the restarted daemon reloads the queued lease
   with no living owner and no owner-liveness check for QUEUED state
   (observed live: lease-127).
3. **Client auto-start parents the daemon to the calling shell** — an
   auto-started daemon from a one-off command dies with that shell's session,
   cancelling every in-flight `--wait`. Bench now always starts an explicit
   detached daemon.

### P1-6 — late-acquire pattern implemented (option b)

New arm `L` (`bench/arm_simurgh_late.sh`): build outside the lease with
agent-managed isolated scratch paths, `acquire --wait --rm` only at
install/launch, release immediately after. Plus a health-window sensitivity
table so one 30s-window B5 run reports the 5s and 30s SLAs side by side.
Measurement queued behind the machine gate.

### P1-8 — B3 decomposition (from committed evidence, medians of 10 reps/arm)

| Component | naive | simurgh | Δ |
|---|---|---|---|
| acquire | — | 1.32s | +1.3s |
| build | 1.23s (warm global DD) | 15.21s (cold isolated DD) | +14.0s |
| boot-readiness wait | 0.12s | 9.39s (harness's own defensive bootstatus) | +9.3s |
| install | 0.26s | 10.79s | +10.5s |
| launch health window | 5.16s | 5.42s | ≈0 (fixed 5s both arms) |
| screenshot + terminate | 0.58s | 2.97s | +2.4s |
| **total** | **7.33s** | **48.67s** | **+41.3s** |

DIY's B1 median (38.64s) confirms the bulk is the cost of isolation in
general (cold DD + per-agent device), not Simurgh's mechanism; ~9s of the
delta is the harness's own belt-and-braces bootstatus wait.

### P2 hygiene landed

`bench/results/.scratch` startup sweep in both `run.sh` and `run_b6.sh`
(P2-9, the 40 GB lesson); B4 naive stray-process detector anchored so it no
longer substring-matches `xcodebuildmcp` (P2-10).

## What remains blocked, and why (recorded, not smoothed)

Every timed cell (B1 fresh/reuse, B2, B4 shipped-defaults rep, B5 dual-window,
B6 third ARM-S rep, XcodeBuildMCP composition check) is
queued behind a quiet-machine driver (`bench/p0_rerun_driver.sh`) because the
machine never met the plan's ground rule 4 during the evening session that
queued them. **P1-7 warm-DD on Lancer is no longer in that queue** — measured
2026-07-14 (see Feature section above; `-0.2%`). Remaining blockers as of the
original queue:

- **No restart happened** (booted 08:34, before the original bench window) —
  verified via `kern.boottime`, not assumed.
- **Active use all evening**: live keyboard input, an active cursor-agent on
  `~/Documents/command-center`, and ~23 GB of fresh Lancer DerivedData growth
  in the owner's global DerivedData that took the Data volume to **100% full**
  (117 MB free) mid-session. Nothing in the harness's safety envelope could
  free meaningful space (only the session's own 1.6 GB Go build cache was
  cleared).

The driver runs preflight before every scenario, requires ≥10 min input idle
and ≥15 GB free disk, and applies one documented exception: if the ONLY
preflight failure is a stray agent process measured dormant (<1s CPU over
60s), it proceeds and records the measurement next to the preflight JSON.
That exception is an orchestrator call, disclosed here; strike the affected
cells if you disagree. Broken reps, if any, stay logged broken.

## Updated safe-to-announce map

| Claim | Status |
|---|---|
| 4 agents, 100% vs 15%, zero incidents, install_clobber evidence (B2) | ✅ receipts committed |
| Agents screenshot each other's app without isolation (PNGs) | ✅ receipts committed |
| Crash cleanup: DIY orphans 5/5, Simurgh reclaims (B4) | ✅ defaults aligned in code; ⏳ one shipped-defaults rep pending machine |
| Dogfooding found+fixed real queue-stuck bug (Bug 1) | ✅ |
| Honest solo overhead ~41s with decomposition (B3) | ✅ table above |
| Zero collisions at 2 agents (B1) | ⏳ rerun queued (root cause documented; number unchanged until then: 17/20) |
| Time-to-green comparison (B1/B2) | ⏳ metric implemented, cells queued |
| Queueing beats thrash (B5) | ⏳ late-acquire pattern implemented, rerun queued |
| Works on our real app (B6) | ⏳ n=4 stands; third rep + XcodeBuildMCP check queued |
