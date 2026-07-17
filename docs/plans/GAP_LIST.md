# Simurgh W-matrix gap list

**HEAD:** `dcc6bf737c097cf7fdffe5ff0864ea43bda8e12c` (`merge: harden daemon survival test polling`)  
**Scan date:** 2026-07-17 (local evening / UTC 03:19–03:31Z)  
**Final summary:** 17 PASS · 1 BLOCKED · 0 FAIL (W1–W18)

Evidence roots:

| Root | Cells |
|---|---|
| `/tmp/simurgh-w-evidence` | W1–W10, W13, W16 (W16 exact **5/5**) |
| `/tmp/simurgh-resilience-evidence` | W11, W12, W14, W15, issue #2, issue #3 |

---

## W-matrix

| ID | Status | Evidence | Repro or blocker |
|---|---|---|---|
| W1 | PASS | `/tmp/simurgh-w-evidence/w1-*` — `lease-1` BUILD SUCCEEDED in 45s | — |
| W2 | PASS | `/tmp/simurgh-w-evidence/w2-*` — `simurgh init` wrote `.simurgh.yaml` + `.mcp.json` | — |
| W3 | PASS | `/tmp/simurgh-w-evidence/w3-*` — MCP stdio: **7 tools**, all expected calls OK (`w3-analysis.txt`) | — |
| W4 | PASS | `/tmp/simurgh-w-evidence/w4-*` — two concurrent leases: distinct UDIDs + DerivedData, builds OK | — |
| W5 | PASS | `/tmp/simurgh-w-evidence/w5-*` — `simurgh exec` BUILD + per-lease DD isolation | — |
| W6 | PASS | `/tmp/simurgh-w-evidence/w6-*` — managed `simurgh shell` JSONL audit (`xcodebuild`/`xcrun`, audit mode) | — |
| W7 | PASS | `/tmp/simurgh-w-evidence/w7-*` — `--reuse-if-exists`: second acquire `reused: true`, same lease/UDID | — |
| W8 | PASS | `/tmp/simurgh-w-evidence/w8-*` — `--rm` parent-exit reclaim in ~2s | — |
| W9 | PASS | `/tmp/simurgh-w-evidence/w9-*` — `hold` suppresses inactivity; TTL reclaim path OK | — |
| W10 | PASS | `/tmp/simurgh-w-evidence/w10-*` — two XcodeBuildMCP sessions (pids 17365/17367); B survived A stop | — |
| W11 | PASS | `/tmp/simurgh-resilience-evidence/w11/` — daemon run/status, SIGTERM stop, ipc.Dial auto-start, stale-socket reclaim | — |
| W12 | BLOCKED | `/tmp/simurgh-resilience-evidence/w12/` — live pressure-queue not triggerable: `kern.memorystatus_vm_pressure_level=1` (normal), `free+inactive+purgeable_GiB=5.839` vs `pressure_floor_GiB=4.0`; unit supplement PASS (`w12/unit-supplement.txt`) | Cannot safely manufacture warn/critical RAM pressure on this host for production-path queue-under-pressure proof |
| W13 | PASS | `/tmp/simurgh-w-evidence/w13-*` — `warm create|list|acquire|remove` round-trip | — |
| W14 | PASS | `/tmp/simurgh-resilience-evidence/w14/` — HOME/`SIMURGH_HOME` mismatch exit codes + malformed-path negatives | — |
| W15 | PASS | `/tmp/simurgh-resilience-evidence/w15/` — prune plan + `--all` label guard; non-Simurgh devices untouched | **APPLY blocked:** plan listed foreign booted orphan `0C54124D…` from another home — refused `prune --apply` (`w15/apply-blocked-cleanup.txt`) |
| W16 | PASS | `/tmp/simurgh-w-evidence/w16-*` — **5/5** acquire+release (`w16-summary.txt`: `PASS_COUNT=5/5`) | — |
| W17 | PASS | Active README, AGENT_GUIDE, PRD, ARCHITECTURE, CLI/init help and `internal/mcp/doc.go` agree on seven MCP tools; `bench/run.sh` matches shipped reuse semantics | Historical dated records retain their original five-tool/open-issue wording by design |
| W18 | PASS | Issues [#1](https://github.com/RoshanDewmina/simurgh/issues/1), [#2](https://github.com/RoshanDewmina/simurgh/issues/2), and [#3](https://github.com/RoshanDewmina/simurgh/issues/3) live-verified and closed; #2 fix `d403a51`, merge `2c8332e` | Original #2 failure evidence: `/tmp/simurgh-resilience-evidence/issue2/`; final blackbox evidence: `/tmp/simurgh-issue2-live-evidence/` |

---

## Bugs found and fixed

### Issue #2 — orphaned QUEUED lease after daemon death (CLOSED)

**URL:** https://github.com/RoshanDewmina/simurgh/issues/2  
**Status:** CLOSED — implementation `d403a51`, merged locally as `2c8332e`, evidence comment https://github.com/RoshanDewmina/simurgh/issues/2#issuecomment-4998804106

**Exact repro (live, `ae676d7`, evidence `/tmp/simurgh-resilience-evidence/issue2/`):**

1. Isolated `SIMURGH_HOME`, `SIMURGH_CAPACITY=1`.
2. Acquire filler lease (ACTIVE, holds capacity).
3. Start second `simurgh acquire --wait` (QUEUED waiter, pid `12295`).
4. Kill daemon during QUEUED wait (`02-kill-and-persist.txt`: `daemon_dead`, waiter stderr `simurgh: EOF`).
5. Restart daemon same home.
6. **Immediately after restart** (`03-restart-inspect.txt`): `list` shows `lease-2` **QUEUED** with dead owner pid `12295`; `status` counts `queued: 1`. Waiter got bare **EOF**, not actionable daemon-disconnected triple.
7. Watcher cancelled `lease-2` ~5s later (`issue2/simurghd.log`: `23:23:36` restart → `23:23:41` `queued owner process exited`).

**Fix proof:** fresh blackbox SIGKILL acceptance returned structured
`DAEMON_DISCONNECTED`, then the first post-restart list reported
`QUEUED_AFTER_RESTART=0` and `CANCELLED_AFTER_RESTART=1`. Full
`go test -race -count=1 ./...`, focused 10x race tests, builds, vet, and
CLI/MCP/doctor negative tests passed. Evidence:
`/tmp/simurgh-issue2-live-evidence/`.

---

## Doc drift (residual)

| Drift | Where | Resolution in this packet |
|---|---|---|
| MCP tool count “five” vs seven shipped | `internal/mcp/doc.go`, `docs/PRD.md` §5 | Updated to seven tools (`lease_hold`, `lease_unhold` added post-M3.1) |
| `--reuse-if-exists` “no-op” bench comment | `bench/run.sh` header | Corrected — R-WP1 ships real reuse; bench reuse economics still from harness-held lease + stable session/project labels |
| Historical orchestrator / session reports still say issues #1–#3 OPEN or five MCP tools | `docs/plans/orchestrator-state.md` (older entries), `SIMURGH_SOL_SESSION_REPORT_2026-07-14.md`, `MASTER_BUILD_STATUS.md` | Left as dated history; this gap list + new orchestrator append supersede for matrix state |
| `docs/ROADMAP.md` M3.1 says “five tools” | `docs/ROADMAP.md` | Intentionally historical: M3.1 originally shipped five; current sections and active user docs say seven |
| W12 live pressure-queue | — | BLOCKED on host; unit-only supplement recorded |
| W15 `prune --apply` cross-home orphan listing | `w15/plan-and-guard.txt` | Product/doc follow-up: scope prune plans to known homes or require owner confirmation |

---

## Issue dispositions

| Issue | Title | Disposition | Evidence / link |
|---|---|---|---|
| [#1](https://github.com/RoshanDewmina/simurgh/issues/1) | `--reuse-if-exists` silent no-op | **CLOSED** — live-verified on `ae676d7`; owner comment [2026-07-17](https://github.com/RoshanDewmina/simurgh/issues/1#issuecomment-4998707056); W7 PASS | `/tmp/simurgh-w-evidence/w7-*` |
| [#2](https://github.com/RoshanDewmina/simurgh/issues/2) | Dying daemon leaves orphaned QUEUED leases | **CLOSED** — fixed by `d403a51`, merged as `2c8332e`; W18 PASS | [Evidence comment](https://github.com/RoshanDewmina/simurgh/issues/2#issuecomment-4998804106), `/tmp/simurgh-issue2-live-evidence/` |
| [#3](https://github.com/RoshanDewmina/simurgh/issues/3) | `ipc.Dial` auto-started daemons die with parent shell | **CLOSED** — live-verified on `ae676d7`; owner comment; W17 PASS | `/tmp/simurgh-resilience-evidence/issue3/` |

---

## Shipped since last matrix (context)

**R-WP5** at `ae676d7`: audit-mode PATH shims + `simurgh shell`; focused race tests + `darwin_sim` live managed-shell gate (`cmd/simurgh/shell_live_test.go`, `shell_test.go`).

**Final-gate hardening:** `be6fb2f` scopes real provisioner-test cleanup to
the exact owned UDID; `dcc6bf7` replaces one-shot daemon survival pings with
hard-bounded condition polling. On the combined tree, both full race suites,
including `darwin_sim`, passed on the first run.

## Bench Phase A

**BLOCKED — not run.** Guarded preflight exited 1 because owner
`cursor-agent` PID `1202` was active. Passing measurements: 109 GB free on
Data, user idle about 1987s (minimum 600s), pressure OK, zero active
`xcodebuild`/XcodeBuildMCP jobs, zero booted simulators, and zero
`simurgh-clone`/`bench-*` residue. No process was killed and no timed cell
started. Evidence: `/tmp/pf-driver.json`, `/tmp/pf-driver.stderr`,
`/tmp/pf-decision.json`.
