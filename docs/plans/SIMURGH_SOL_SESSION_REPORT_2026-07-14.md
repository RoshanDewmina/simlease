# Simurgh Sol session report — 2026-07-14

**Report time:** 2026-07-14 17:16 UTC (America/Toronto 13:16 EDT)
**Orchestrator session:** Cursor Sol (Phase A close → Phase B R-WP0…R-WP6)
**Stop trigger:** owner directive to stop during R-WP6 (coder pause handoff)
**Branch state after report review:** `feat/r-wp6-xcodebuildmcp` contains report commit `c534a79` plus this correction commit; use `git log -1 --oneline` for the resulting docs tip. The last implementation/code tip remains `bec8670f24feb9da6e4043c7fdc707f41e336f0d`.
**Master (unchanged):** `4447e3c267a25d09ea20c3779c400dfa71330bef` — **nothing from this session is merged or pushed**

> **Hard claims (binding):** R-WP6 is **not** complete and **not** reviewed. Phase A is **STOPPED/BLOCKED**, not passed. Benchmarks must not restart without a new owner authorization. Master remains `4447e3c`.

---

## 1. Executive summary

This Sol session:

1. Continued Phase A benchmark-blocker work on `claude/benchmark-blockers-2026-07-13-37773c`, then **stopped Phase A by explicit owner directive** (2026-07-14 14:25 UTC) and recorded that stop at `593a051`.
2. Started Phase B **only by that owner override**, under `R-WP*` naming, on stacked local worktrees.
3. Delivered gate-verified packages **R-WP0 → R-WP4** (R-WP4 implementation-complete / benchmark-deferred).
4. Started **R-WP6** (XcodeBuildMCP adapter; R-WP5 intentionally skipped/gated), committed adapter code at `bec8670`, then **paused** on owner stop with an **untracked, unverified** `runtime_live_test.go`.
5. Contained a Cursor API credential leak earlier on the Phase A branch (`182fdfa` / `87be7f8`), with a remaining **unreachable-object** cleanup caveat and **no push**.

**Current safe stop point:** R-WP6 implementation is paused at code tip `bec8670` in `.worktrees/r-wp6-xcodebuildmcp`; the branch also contains docs commit `c534a79` and this correction commit. Stacked feature branches exist locally and are **not** on `master` / **not** pushed.

---

## 2. Owner decisions / overrides

| # | Decision | Binding effect |
|---|---|---|
| A | **Stop Phase A** (2026-07-14 14:25 UTC) — taking too long; continue to Phase B | Phase A is STOPPED/BLOCKED, **not** complete. Do not call it a pass. Do not restart timed drivers/`BENCH_START_AT`/B* cells unless newly authorized. |
| B | **Correction #1 — naming** | Roadmap packages logged/committed as `R-WP*`, distinct from Phase A `P0-*` / harness `WP*`. |
| C | **Correction #2 — fold lifecycle bugs** | Orphaned QUEUED owner-liveness + daemon-dies-with-parent fold into R-WP1/R-WP2 scope (not “file only”). |
| D | **Correction #3 — gate R-WP5** | R-WP5 (PATH shims / managed shell) starts **only after** R-WP6 reassessment; do not build reflexively in sequence order. |
| E | **Stop R-WP6 now** (≈2026-07-14 17:11 UTC / 13:11 EDT) | No further R-WP6 implementation/testing/commits beyond what was already committed; preserve dirty/untracked exactly; safe cleanup of owned resources only. |
| F | **Security** | Revoke/rotate leaked credential; local unpublished history rewrite on Phase A branch; **no push** until owner review of rewrite. |
| G | **Publication** | Only claims backed by committed `bench/evidence/` (see `bench/LAUNCH_NUMBERS.md`); unmet cells stay unmet. |
| H | **Merge/push** | Do not merge stacked R-WP branches to `master` and do not push from package completion alone. |

---

## 3. Security incident, remediation, remaining caveat

### Incident

A `crsr_` Cursor API credential was captured into committed warm-DD evidence because `bench/preflight.sh` embedded full `ps aux` command lines for stray `cursor-agent` workers (argv included `--api-key`).

### Remediation (committed / owner actions)

| Step | Evidence |
|---|---|
| Forward containment | `182fdfa` — stop argv capture; scrub evidence paths; `[REDACTED:CURSOR_API_KEY]` markers; `internal/redact` + `bench/redact_secrets_test.py` |
| Fixture clarification | `5fda230` — leaked-body vs synthetic `crsr_` fixtures |
| Owner revoke/rotate | Recorded complete in orchestrator-state (`87be7f8` entry) |
| Local unpublished history rewrite | Branch tip rewritten; orchestrator-state records zero matches for the *leaked credential body* on commits reachable from `HEAD` (`87be7f8`) |
| Pre-rewrite reference | `40a245a` cited in state as prior tip before sanitized rebuild |

### Remaining caveat (binding)

**Unreachable objects may remain** in local reflogs / object DB pending separate cleanup (not performed in the security step). **No push** (local or remote) until owner review of the rewrite. Synthetic `crsr_TESTFAKE…` fixtures that match the *generic* token shape are intentional and are not the leaked body.

This report does **not** claim a full object-DB GC or that unreachable objects were audited after `87be7f8`.

---

## 4. Phase A — exact completed / stopped cells

**Handoff commit:** `593a051ac36539d8f76483246e3b17baf3d3f7d5`
`docs(bench): Phase A STOPPED/BLOCKED handoff (owner 14:25 UTC)`
**Branch/worktree:** `claude/benchmark-blockers-2026-07-13-37773c` @ `.claude/worktrees/benchmark-blockers-2026-07-13-37773c`

### Driver termination

- Owner stop killed known `p0_rerun_driver` PIDs `39025` / `39043`.
- Unauthorized brief resume `pid 61780` (`BENCH_START_AT=B2` at `14:27:25Z`) was also killed; **no B2/B4/B5/B6 cell started after the directive**.
- Leftover note (not a driver): `bench/.bin/simurgh daemon run` pid `46001` was recorded at handoff time as still bound to `~/.simurgh` from earlier lease work; left untouched by docs closure.

### Committed evidence / outcomes

| Item | Commit(s) | Evidence path | Outcome |
|---|---|---|---|
| B1-fresh (retries) | `e057dd3`; folded `1bdeafd` | `bench/evidence/B1-fresh-20260714T111219Z/` | simurgh first-attempt 14/20; **all-green reps 10/10**; median to-N-green 265.3s; DIY 20/20 / 10/10 |
| B1-reuse | `54509ba`; folded `1bdeafd` | `bench/evidence/B1-reuse-20260714T131116Z/` | first-attempt **18/20**; **all-green 10/10**; median to-N-green **17.0s** |
| Warm-DD Lancer | `b41d24d`; folded `8d638fd` | `bench/evidence/warmdd-lancer-20260714T033925Z.json` | cold 318.2s vs warm 319.0s; **−0.2%** (do not cite toy ~30%) |
| B2 (2026-07-14 retries) | blocked snapshot in `593a051` | `bench/evidence/blocked-b2-2026-07-14T14:16:20Z.json` | **BLOCKED** — driver wait loop; HID idle ≪600s; dormant strays 1366/34120; **no B2 cell started** |
| Prior launch cells | `6cec09b` | `B2/B3/B4/B5/B6-*-20260713T*` under `bench/evidence/` | stand as previously published; B6 simurgh still **n=4** (2/3 reps) |

### GitHub issues filed (P0 product bugs)

Still **OPEN** on `github.com/RoshanDewmina/simurgh` as of this report’s evidence pull:

| # | Title | Scope note in issue |
|---|---|---|
| [#1](https://github.com/RoshanDewmina/simurgh/issues/1) | `broker.Acquire` ignores `--reuse-if-exists` (silent no-op) | Tracked for R-WP1 |
| [#2](https://github.com/RoshanDewmina/simurgh/issues/2) | Dying daemon leaves orphaned QUEUED leases | Fold into R-WP1/R-WP2 |
| [#3](https://github.com/RoshanDewmina/simurgh/issues/3) | `ipc.Dial` auto-started daemons die with parent shell | Related to R-WP2 lifecycle |

Code on the stacked R-WP1/R-WP2 branches addresses these behaviors, but the GitHub issues themselves remain open (no close evidenced in this session).

### REVIEW_AND_NEXT_STEPS P0 exit criteria — met vs unmet

| # | Criterion | Status |
|---|---|---|
| P0-1 | B1 ARM-S ≥ DIY success at 2 agents, or root-caused reason | **MET** — retries all-green 10/10; reuse first-try 18/20 |
| P0-2 | time-to-N-green in report.py + B1/B2 rerun cells | **PARTIAL** — B1 committed; **B2 retries unmet** |
| P0-3 | B6 third simurgh rep + XcodeBuildMCP composition with evidence | **UNMET** — B6 n=4; no committed `bench/evidence/*xbmcp*` |
| P0-4 | snippet/defaults aligned + one B4 rep on shipped defaults | **PARTIAL** — code `2438c80`; **B4-defaults-rep unmet** |
| P0-5 | receipts committed | **MET** |

Also unmet: P1-6 B5 dual-window cell (pattern code `b1026ed`). P1-7 warm-DD on Lancer: **MET**.

### Cell status at Phase A stop

- B2 retries: STOPPED/BLOCKED (never started 2026-07-14 cell)
- B4 shipped-defaults rep: not started
- B5 dual-window: not started
- B6 third ARM-S: not started (n=4 stands)
- XcodeBuildMCP composition: **not committed**; raw gitignored `bench/results/20260714T141626Z/` is **not** committed proof

---

## 5. Roadmap stacked branches / worktrees (R-WP0 → R-WP6)

Implementation/package ancestry (each listed code tip’s merge-base with the prior tip equals that prior tip; `master` is an ancestor of the stack):

`master (4447e3c)` → … → `593a051` → `0757144` → `e948d26` → `23c2dce` → `b811736` → `f395061` → `bec8670`

On R-WP6, docs commit `c534a79` and this correction commit follow `bec8670`; they do not change the implementation ancestry above.

| Package | Branch | Worktree | Base | Code/package tip | Branch docs tip | Status | Review | Gates |
|---|---|---|---|---|---|---|---|---|
| Phase A stop | `claude/benchmark-blockers-2026-07-13-37773c` | `.claude/worktrees/benchmark-blockers-2026-07-13-37773c` | (pre-handoff `1bdeafd`) | `593a051` | `593a051` | STOPPED/BLOCKED | n/a (docs handoff) | docs + blocked evidence |
| **R-WP0** | `feat/r-wp0-baseline` | `.worktrees/r-wp0-baseline` | `593a051` | `0757144` | `0757144` | **complete** | docs-only | build/vet/race + darwin_sim live |
| **R-WP1** | `feat/r-wp1-reuse-liveness` | `.worktrees/r-wp1-reuse-liveness` | `0757144` | `e948d26` | `e948d26` | **complete** | review-fix + final arbitration | package race + live reuse |
| **R-WP2** | `feat/r-wp2-global-capacity` | `.worktrees/r-wp2-global-capacity` | `e948d26` | `23c2dce` | `23c2dce` | **complete** | bounded review-fix | capacity + 30s detach + live reuse |
| **R-WP3** | `feat/r-wp3-session-jobs` | `.worktrees/r-wp3-session-jobs` | `23c2dce` | `b811736` | `b811736` | **complete** | review-fix + escalated resolution | controlplane race proofs + regressions |
| **R-WP4** | `feat/r-wp4-readiness-cache` | `.worktrees/r-wp4-readiness-cache` | `b811736` | `4e500f1` | `f395061` | **implementation-complete / benchmark-deferred** | bounded review-fix closed | impl gates green; **no** timed bench |
| **R-WP5** | — | — | — | — | — | **not started** (gated) | — | awaiting R-WP6 reassessment |
| **R-WP6** | `feat/r-wp6-xcodebuildmcp` | `.worktrees/r-wp6-xcodebuildmcp` | `f395061` | `bec8670` | `c534a79` + this correction commit (discover via `git log -1`) | **PAUSED** (not complete) | **not reviewed** | pause-worker handoff reports unit/CLI/full race at `bec8670`; live runtime **not** run |

**Merge/push:** every row above is **NOT merged into `master`** and the stacked tips are **unpushed** relative to `origin/master` (`4447e3c`). This was measured at report ref `c534a79`; this correction adds one docs-only local commit, so use `git rev-list --count origin/master..HEAD` for the current count rather than relying on a frozen number.

---

## 6. Per-package technical changes and key review findings/fixes

### R-WP0 — baseline protect (`0757144`)

- Docs-only: `MASTER_BUILD_STATUS.md`, orchestrator-state, narrative B2 typo `17/18→17/20` in `RESULTS.md`.
- No product code; no `bench/evidence` / `bench/results` / `docs/dogfood` mutation.
- Baseline gates: `go build` / `vet` / `test -race` (16 packages) + darwin_sim provisioner/safety/reaper live tests **PASS**.

### R-WP1 — ReuseIfExists + QUEUED liveness (`e948d26`)

- Real `ReuseIfExists`; orphaned QUEUED cancel/reap; identity rules evolved through review → final arbitration (`b69a685` / `6485c30`): exact Spec + non-empty Labels.`project` + Agent/SessionID; random per-process session IDs (no PID derivation).
- Daemon-dies-with-parent **deferred to R-WP2** with concrete ≥30s acceptance obligation.
- Final live gate: `TestReuseIfExistsLive` PASS (arbitration: test 3.25s / package 4.412s).

### R-WP2 — global capacity + Dial detach (`23c2dce`)

- Host-global capacity (`SIMURGH_CAPACITY`), optional per-spec/provisioning limits, explicit queue `reason`/`resourceClass`, fairness; Dial detach (`/dev/null` stdin + Setsid + SIGHUP ignore) with `TestDialDaemonSurvivesParentExit` (PID **80395** at package completion; review-fix later PID **92313**).
- Review-fix: MCP structured CAPACITY_QUEUED fields; startup `AdvanceRehydratedQueues`; fix commits `c069e81`, `8d374fa`.
- **Approved minor debt (carried forward, untouched by later packages):** capacity migration surprise; LaunchAgent still optional; global queue-position semantics.

### R-WP3 — session/job + build slots (`b811736`)

- New `internal/controlplane`; independent `SIMURGH_BUILD_SLOTS`; device held only in device phases; additive pool/MCP build fields; no public session RPCs.
- Review-fix `4050a5e` then escalated `f49a258` (atomic terminal claim, retained cleanup, promoted waiter ownership) with `-count=50` race proofs.
- Carry-forward: Session/Job state non-durable (SQLite etc. deferred).

### R-WP4 — readiness + warm-cache (`f395061`)

- Warm-seed formal keys + CoW; writable-cache confinement; bounded `PollReadiness` / `READINESS_TIMEOUT`; phase aliases acquire/build/install/launch/test/capture.
- Review-fix commits: `25f2926`, `0d07d60`, `0b47e3d`, `42174fc`, `4e500f1`; status tip `f395061`.
- **Explicitly deferred:** all timed B1/B2/B4/B5/B6 / XcodeBuildMCP composition / new `bench/evidence` / latency claims.

### R-WP6 — XcodeBuildMCP adapter (**PAUSED**; code tip `bec8670`)

**Committed:** `bec8670` is the **sole implementation commit beyond R-WP4**. It is followed on the branch by docs commit `c534a79` and this docs correction commit.

- New package `internal/integrations/xcodebuildmcp` (adapter/bindings/config/version/doc + unit tests)
- CLI `cmd/simurgh/integration.go` + tests; `main.go` wiring
- Behavior (committed): `simurgh integration xcodebuildmcp start|stop|status --session <lease-id>`; ACTIVE lease/device via daemon IPC; lease-root isolation bindings; ephemeral MCP config; XcodeBuildMCP **2.6.2** recording; detached process-session + PID metadata; lease-specific process-group cleanup; two-session config/env attribution tests; no committed machine UDID

**Not done / not claimed:**

- Independent reviewer pass — **none**
- Architecture / AGENT_GUIDE / MASTER / orchestrator updates for R-WP6 — **none before this docs pause report**
- Evidence-backed R-WP5 defer/shrink/retain decision — **not made**
- Bounded two-clone live validation — **not run**

**Untracked (preserved byte-for-byte; not part of `bec8670`):**

- `internal/integrations/xcodebuildmcp/runtime_live_test.go`
  SHA-256 `9582650a420aa69976fe03a15a81a799d136eadfb8f71796fca7ce0dda13f5f3` (270 lines)
  Not formatted, compiled, or executed per pause handoff.

---

## 7. Verification evidence

### Last verified gates for R-WP6 (`bec8670`, before untracked runtime test)

The following results are **reported by the R-WP6 pause-worker handoff** (coder transcript). They were not independently reproduced for this report and are not sourced from a committed repository gate log:

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

Untracked runtime test: **not** formatted/compiled/run.

### Last verified gates for R-WP4 tip (`f395061` lineage)

See `docs/plans/MASTER_BUILD_STATUS.md` on `feat/r-wp4-readiness-cache`: warm-seed / isolator / readiness / controlplane focused gates, full race (17 packages), capacity, detach (PID **4277** at review-fix close), live reuse — all **PASS**. No timed benchmark cells.

### Phase A publication bar

`bench/LAUNCH_NUMBERS.md` on the Phase A / stacked lineage lists the only claims safe to publish; unmet cells (B2 retries timed, B4-defaults-rep, B5, B6 n=3 + xbmcp composition) remain unpublished.

---

## 8. Benchmark publication / defer rules

**Publish only** numbers that trace to committed `bench/evidence/` and are listed as safe in `bench/LAUNCH_NUMBERS.md`.

**Do not publish / do not restart without new owner auth:**

- B2 2026-07-14 retries timed cell
- B4 shipped-defaults rep
- B5 dual-window
- B6 third ARM-S (n=4 stands)
- XcodeBuildMCP composition as bench evidence
- Any new latency / time-to-green claims from R-WP4/R-WP6 implementation work
- Treating gitignored `bench/results/*` as committed proof

**R-WP4/R-WP6 rule:** implementation may proceed; **benchmark-deferred** means no `p0_rerun_driver`, no `BENCH_START_AT`, no new `bench/evidence` publishes from those packages unless owner redirects.

---

## 9. Current git / process / resource state

### Git

| Item | Value |
|---|---|
| Active R-WP6 worktree | `/Users/roshansilva/Documents/simurgh/.worktrees/r-wp6-xcodebuildmcp` |
| Branch | `feat/r-wp6-xcodebuildmcp` |
| Branch docs tip | `c534a79` + this correction commit; discover the resulting tip with `git log -1 --oneline` |
| Last implementation/code tip | `bec8670f24feb9da6e4043c7fdc707f41e336f0d` |
| Dirty tracked | none (at pause) |
| Untracked | `internal/integrations/xcodebuildmcp/runtime_live_test.go` only |
| `master` checkout | `/Users/roshansilva/Documents/simurgh` @ `4447e3c` tracking `origin/master` |
| Remote | `origin` exists → `https://github.com/RoshanDewmina/simurgh.git` |
| Stacked R-WP merges to master | **none** |
| Stacked R-WP pushes | **none** (tips ahead of `origin/master` locally only) |

### R-WP6 pause cleanup (from handoff)

- Runtime validation **not** started; no real R-WP6 clones created
- No owned XcodeBuildMCP/fake adapter processes remain from R-WP6
- Owned queued probe lease `lease-163` released → final state `cancelled`
- Removed `/tmp/r-wp6-runtime`, `/tmp/xcodebuildmcp-2.6.2.tgz`
- Existing Simurgh daemon and unrelated processes left untouched

### Host note (not claimed as R-WP6-owned)

At report authoring, unrelated `npx xcodebuildmcp@latest mcp` processes were observed on the host (started earlier than the R-WP6 window). Pause handoff asserts R-WP6-owned children were cleaned; this report does **not** treat those host processes as R-WP6 leftovers.

---

## 10. Unmerged / unpushed implications

1. **`master` users still see `4447e3c`** — no R-WP0…R-WP6 product behavior is on master.
2. **Local stacked worktrees are the only carriers** of Phase B work; losing a worktree without push loses that tip unless another clone has the objects.
3. **Security rewrite** on the Phase A branch remains local-policy: do not push until owner reviews.
4. **GitHub issues #1–#3** still open — closing them is a separate process decision even though stacked code addresses the bugs.
5. **R-WP6 is mid-package** — merging the R-WP6 branch (whose last code tip is `bec8670`) without review/runtime validation would violate package gates.

---

## 11. Open risks / debt (including approved minors)

| Risk / debt | Status |
|---|---|
| Phase A unmet cells (B2/B4/B5/B6/xbmcp composition) | Open; owner-stopped |
| Unreachable git objects after credential rewrite | Caveat remains; no GC evidenced |
| R-WP2 approved minors (capacity migration surprise; LaunchAgent optional; global queue-position semantics) | Carried forward |
| R-WP3 Session/Job non-durable | Carry-forward |
| R-WP4/R-WP6 unpublished performance | Intentional |
| R-WP5 PATH shim risk (recursion / absolute paths) | Not started; gated on R-WP6 reassessment |
| R-WP6 untracked live test | Unverified; needs review before run |
| R-WP6 not reviewed | Blocking for any “complete” claim |
| Open GitHub #1–#3 | Still OPEN |
| Historical “no remote” state text | Superseded: `origin` exists; stacked tips remain unpushed; rewritten Phase A history must not be pushed until owner review |

---

## 12. Exact stop point (R-WP6)

| Layer | State |
|---|---|
| Committed behavior | Adapter + CLI + unit/CLI attribution tests at `bec8670` |
| Reported gates | Listed in §7; attributed to the pause-worker handoff against `bec8670`, not a repository gate log |
| Untracked | `runtime_live_test.go` preserved; **unverified** |
| Review | **Not started / not approved** |
| Runtime two-clone validation | **Not run** |
| Docs status before this report | MASTER/orchestrator still described R-WP4 as current |
| Claim allowed | **PAUSED**, not complete |

---

## 13. Prioritized restart plan (literal commands)

Do **not** resume Phase A benchmarks. Resume only R-WP6 (or stop after docs).

```bash
# 0) Enter the paused worktree and confirm branch, ancestry, and untracked test
cd /Users/roshansilva/Documents/simurgh/.worktrees/r-wp6-xcodebuildmcp
git status --short --branch
git log -1 --oneline
git merge-base --is-ancestor bec8670 HEAD
# exit 0 means the last implementation tip is present beneath the docs tip
git status --porcelain internal/integrations/xcodebuildmcp/runtime_live_test.go
shasum -a 256 internal/integrations/xcodebuildmcp/runtime_live_test.go
# expect 9582650a420aa69976fe03a15a81a799d136eadfb8f71796fca7ce0dda13f5f3

# 1) Inspect untracked live test without modifying it until reviewed
git diff -- internal/integrations/xcodebuildmcp/runtime_live_test.go
# (file is untracked — use `less`/`bat` rather than assuming staged diff)

# 2) Only after ownership/cleanup review of the live test:
gofmt -w internal/integrations/xcodebuildmcp/runtime_live_test.go
go test -race ./internal/integrations/xcodebuildmcp/... -count=1 -v
go test -race ./cmd/simurgh -run 'TestXcodeBuildMCP|TestIntegrationXcodeBuildMCP' -count=1 -v

# 3) Re-run package regression gates used at pause (expected commands)
go build ./...
go vet ./...
go test -race ./...
go test -race ./internal/broker -run 'TestGlobalCapacity|TestPerSpecCap|TestProvisioning|TestQueueReason|TestFairness|TestPressure' -count=1 -v
go test -race ./internal/ipc -run 'TestDialDaemonSurvivesParentExit' -count=1 -v
go test -race -tags darwin_sim ./internal/daemon -run TestReuseIfExistsLive -count=1 -v

# 4) Do NOT run tagged live runtime test until ownership/cleanup reviewed:
# go test -race -tags darwin_sim ./internal/integrations/xcodebuildmcp -run TestXcodeBuildMCPRuntimeTwoSessionAttribution -count=1 -v

# 5) After runtime evidence + review: update AGENT_GUIDE/ARCHITECTURE/MASTER/orchestrator,
#    then make the evidence-backed R-WP5 defer/shrink/retain decision.
# 6) Still do not merge/push unless owner explicitly authorizes.
```

---

## 14. R-WP5 gated decision and later roadmap order

**R-WP5** (managed shell / PATH shims for `xcodebuild`/`xcrun`) remains **gated behind R-WP6 reassessment** (owner correction #3). No defer/shrink/retain decision is evidence-backed yet because R-WP6 runtime validation did not finish.

**Later roadmap order** (from `docs/plans/SIMURGH_CLAUDE_CODE_MASTER_PROMPT.md`, with R-WP prefix): after a finished R-WP6 + R-WP5 decision → R-WP7 worker boundary → R-WP8 remote worker → R-WP9 worktree sync → R-WP10 scheduler → … (unchanged content; not started).

---

## 15. Definition of safe next package boundary

A **safe next boundary** is one of:

1. **Docs/process only** (this report / status updates), with `bec8670` remaining an ancestor of the current docs tip and the untracked live test untouched; **or**
2. **Resume R-WP6 only** in `.worktrees/r-wp6-xcodebuildmcp` on `feat/r-wp6-xcodebuildmcp`, without starting R-WP5, without merging/pushing, and without restarting Phase A timed cells.

Crossing into R-WP5, R-WP7+, merge-to-master, push, or benchmark restart is **out of bound** until the corresponding owner gate is satisfied.

---

## 16. Report provenance (source refs)

| Source | Ref / path |
|---|---|
| Master tip | `4447e3c267a25d09ea20c3779c400dfa71330bef` (`git rev-parse master` / `origin/master`) |
| Phase A STOPPED handoff | `593a051ac36539d8f76483246e3b17baf3d3f7d5` — `docs/plans/orchestrator-state.md`, `bench/RESULTS.md`, `bench/LAUNCH_NUMBERS.md`, `bench/evidence/blocked-b2-2026-07-14T14:16:20Z.json` |
| R-WP0…R-WP4 tips | `0757144`, `e948d26`, `23c2dce`, `b811736`, `f395061` (full SHAs in §5) |
| R-WP6 implementation/code tip | `bec8670f24feb9da6e4043c7fdc707f41e336f0d` |
| R-WP6 report/docs tip | `c534a79` plus this correction commit; obtain the actual tip with `git log -1 --oneline` |
| R-WP6 pause handoff | coder transcript `5f62f0e5-b533-4ae5-a29d-2f3d2fe19a13` (owner stop → handoff text) |
| Stacked status docs | `docs/plans/MASTER_BUILD_STATUS.md`, `docs/plans/orchestrator-state.md` at each tip (read on this worktree + `git show <tip>:…`) |
| Security | `182fdfa`, `5fda230`, `87be7f8`; pre-rewrite cite `40a245a` |
| GitHub issues | `gh issue list/view` #1 #2 #3 on `RoshanDewmina/simurgh` |
| Worktree inventory | `git worktree list` / `git branch -vv` at authoring |
| Roadmap WP6 text | `/Users/roshansilva/Documents/simurgh/docs/plans/SIMURGH_CLAUDE_CODE_MASTER_PROMPT.md` (present in main checkout; **not** tracked in this worktree’s git tree) |
| Untracked live test checksum | SHA-256 `9582650a420aa69976fe03a15a81a799d136eadfb8f71796fca7ce0dda13f5f3` |

---

## 17. Facts this report could not prove from file evidence

1. Whether host `xcodebuildmcp@latest` processes observed at report time are related to any non-R-WP6 session (timestamps precede R-WP6; ownership not proven beyond pause handoff negatives).
2. Whether unreachable objects after the security rewrite still contain the leaked credential body (caveat recorded; GC not evidenced).
3. Exact focused test *names* behind the R-WP6 “7 tests / 6.722s” package gate (handoff recorded aggregate only).
4. Why GitHub issues #1–#3 remain OPEN after R-WP1/R-WP2 code fixes (no close/comment evidenced).
5. Live wall-clock PID `46001` daemon from Phase A handoff — not re-verified at this report’s authoring.
6. `SIMURGH_CLAUDE_CODE_MASTER_PROMPT.md` / `SIMURGH_MASTER_IMPLEMENTATION_REPORT.md` are readable in the main checkout but are **not** git-tracked on `feat/r-wp6-xcodebuildmcp` (untracked local copies); WP6 task text was taken from that local file.
