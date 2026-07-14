# Simurgh Master Build Status

**Updated:** 2026-07-14 14:50 UTC (America/Toronto 10:50 EDT)  
**Roadmap naming:** Phase B packages use `R-WP*` (not Phase A `P0-*` / harness `WP*`).

## Baseline

- Prior baseline (R-WP0 tip): `0757144fd80d69fd7cb45827fe087ca3f1dd57da`
- Branch/worktree: `feat/r-wp1-reuse-liveness` @ `/Users/roshansilva/Documents/simurgh/.worktrees/r-wp1-reuse-liveness`
- Go: `go1.26.5 darwin/arm64`
- macOS: `27.0` (Build `26A5378n`), `arm64`
- Xcode: `27.0` (Build `27A5194q`); `xcrun simctl help` exit 0

## Current work package

- ID: **R-WP1**
- Goal: Real `AcquireRequest.ReuseIfExists` + orphaned QUEUED owner/waiter liveness
- State: **complete** (gates + live smoke green; daemon-dies-with-parent deferred to R-WP2 with obligation)
- Files owned (product): `internal/broker/*`, `internal/watcher/*`, `internal/daemon/*`, `internal/mcp/server.go`, `cmd/simurgh/lease.go`, docs (`AGENT_GUIDE`, `ARCHITECTURE`, `TROUBLESHOOTING`), this status + orchestrator-state
- Non-goals held: global capacity/resource classes, sessions/build slots, PATH shims, XcodeBuildMCP adapter, dashboard

### Acceptance commands (exact)

```bash
go test -race ./internal/broker/... ./internal/watcher/... ./internal/daemon/... ./internal/mcp/... ./cmd/simurgh/...
go build ./...
go vet ./...
go test -race ./...
go test -race -tags darwin_sim ./internal/daemon -run TestReuseIfExistsLive -count=1 -v
```

### Gate results (R-WP1 worktree)

| Gate | Result |
|---|---|
| package race (broker/watcher/daemon/mcp/cmd) | **PASS** exit 0 |
| `go build ./...` | **PASS** exit 0 |
| `go vet ./...` | **PASS** exit 0 |
| `go test -race ./...` | **PASS** exit 0 (16 packages) |
| `TestReuseIfExistsLive` (`darwin_sim`) | **PASS** ~2.17s — two live acquires, same lease ID/UDID/paths |

### Runtime proof

- Live Mac smoke: `TestReuseIfExistsLive` provisioned a real simctl clone, second `reuseIfExists` acquire returned the same lease ID + UDID + DerivedData path, cleaned up only that UDID.

### Matching key (documented)

Reuse matches **ACTIVE** (renew) or in-flight **PROVISIONING** (concurrent attach) when:
`DeviceSpec` + exact non-empty `Labels` containing non-empty `project` +
non-empty `Owner.Agent` + non-empty `Owner.SessionID` are equal.
PID/PPID excluded. Project/Xcode/scheme are **not** first-class — encode in Labels.
RELEASED / FAILED / RECLAIMED / CANCELLED never reused.

### QUEUED liveness (R-WP1)

- Cancelled `Acquire(Wait=true)` → `ACQUIRE_CANCELLED` + QUEUED→CANCELLED (persisted).
- Watcher always checks QUEUED owner PID; dead → atomic `CancelQueued` via ReclaimLoop.
- Wait RPC cancel does **not** cancel another client's QUEUED lease.

## Completed packages

| Package | Commit / note | State |
|---|---|---|
| M0–M4 (v0 scaffold) | prior `master` lineage | historical complete |
| Phase A P0 harness (partial) | `593a051` STOPPED/BLOCKED | not a Phase A pass |
| **R-WP0** | `0757144` | complete |
| **R-WP1** | prior tip `a7d8c78`; final arbitration `b69a685` code/tests + `6485c30` docs | complete |

## Open failures/risks

### Deferred to R-WP2 (concrete acceptance — not merely an issue)

**Daemon-dies-with-parent-shell** remains open.

- **Current Setsid evidence:** `internal/ipc/client.go` `startDetachedDaemon` already sets
  `cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}` (line ~126) before `Start`+`Process.Release`.
- Bench/RESULTS still recorded auto-started daemons dying with the calling shell session
  despite that attribute — Setsid alone is **not** accepted as proof of survival.
- **R-WP2 acceptance obligation:** after `ipc.Dial` auto-start from a short-lived parent
  process/shell that then exits (or receives SIGHUP), the daemon PID must still answer
  `daemon.ping` on the socket for ≥30s without relaunch. Add a focused regression test
  (or documented live script with command+output) that fails if the child dies with the
  parent. Do not claim fixed until that gate is green. May require stronger detach
  (double-fork / ignore SIGHUP / LaunchAgent-only path) beyond Setsid.

### Phase A carry-forward

Unmet cells (B2 retries, B4-defaults-rep, B5 dual-window, B6 third ARM-S, XcodeBuildMCP
composition) remain unmet. Do not restart benchmarks unless owner directs.

### Product risks status after R-WP1

1. `--reuse-if-exists` silent no-op → **fixed in R-WP1**.
2. Dying daemon / cancelled wait orphans QUEUED → **fixed in R-WP1** (cancel + watcher).
3. `ipc.Dial` auto-start dies with parent shell → **deferred to R-WP2** (obligation above).

## Next package

- **R-WP2** — Correct global capacity and resource classes; **and** satisfy the
  daemon-dies-with-parent Setsid/detach acceptance obligation above.
- Do **not** merge/push R-WP1 from this package alone.
- Gate R-WP5 behind R-WP6 reassessment (owner correction #3).

## R-WP1 bounded review-fix loop (2026-07-14 14:49 UTC)

All four blocking reviewer findings addressed:

1. **Identity:** reuse now requires both non-empty `Owner.Agent` and
   `Owner.SessionID`; missing identity is a no-match/new allocation. This entry
   is superseded by the final arbitration below for project and fallback rules.
2. **Queued-owner race:** public `Broker.CancelQueued` performs an atomic
   QUEUED→CANCELLED transition. A stale `"queued owner process exited"` decision
   after promotion is skipped and never falls through to Reclaim/reaper.
3. **Observable outcome:** acquire response wrappers include top-level
   `reused`; true for ACTIVE renewal or PROVISIONING attach, false for new.
   CLI human/JSON and MCP structured results surface it.
4. **Docs:** ACTIVE renewal and PROVISIONING attach are both documented,
   including identity requirements and stable path fallbacks.

### Review-fix commits

- `5699780` — identity, reused boundary, atomic stale-decision fix, tests.
- `b9ce49a` — CLI/MCP/API documentation corrections.

### Exact review-fix gates

| Command | Result |
|---|---|
| `go test -race ./internal/broker/... ./internal/watcher/... ./internal/daemon/... ./internal/mcp/... ./cmd/simurgh/...` | **PASS**, exit 0 |
| `go build ./...` | **PASS**, exit 0 |
| `go vet ./...` | **PASS**, exit 0 |
| `go test -race ./...` | **PASS**, exit 0 (16 packages) |
| `go test -race -tags darwin_sim ./internal/daemon -run TestReuseIfExistsLive -count=1 -v` | **PASS**, exit 0; `TestReuseIfExistsLive` 7.24s, package 8.458s |

R-WP2 was not started. Its daemon-parent-shell obligation remains unchanged.

## Final R-WP1 orchestrator arbitration (2026-07-14 15:09 UTC)

This is the final bounded correction; no further review loop was requested or
dispatched.

- **Reuse partition:** exact `DeviceSpec`; exact non-empty Labels containing a
  non-empty `project`; exact non-empty Agent and SessionID. Any missing
  partition is a no-match/new allocation, not an error.
- **Session generation:** explicit `SIMURGH_SESSION_ID` is honored. CLI fallback
  is cryptographically random per one-shot process, so cross-invocation reuse
  requires the environment variable. MCP fallback is cryptographically random
  once per MCP server process and stable only within it. No PID-derived IDs.
- **Trust boundary:** Owner/SessionID/Labels are partitioning metadata, **not
  authentication**. The parent directory/socket are 0700/0600, but a same-user
  raw IPC client can forge metadata and can already renew/release a known lease
  ID. No unforgeable-identity claim is made.
- **Live smoke:** uses explicit `SIMURGH_SESSION_ID=r-wp1-live-session` and
  `project=r-wp1-live`; first result `reused=false`, second `reused=true`.

### Arbitration commits

- `b69a685` — mandatory project partition, random process IDs, tests/live smoke.
- `6485c30` — CLI/MCP/agent/troubleshooting trust and usage docs.

### Exact arbitration gates

| Command | Result |
|---|---|
| `go test -race ./internal/broker/... ./internal/watcher/... ./internal/daemon/... ./internal/mcp/... ./cmd/simurgh/... -count=1` | **PASS**, exit 0 |
| `go build ./...` | **PASS**, exit 0 |
| `go vet ./...` | **PASS**, exit 0 |
| `go test -race ./...` | **PASS**, exit 0 (16 packages) |
| `go test -race -tags darwin_sim ./internal/daemon -run TestReuseIfExistsLive -count=1 -v` | **PASS**, exit 0; test 3.25s, package 4.412s |

One initial non-race focused compile attempt failed only on two stale test
imports (`fmt` in CLI/MCP tests); those imports were removed before all gates
above passed. R-WP2 was not started; no merge/push occurred.
