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
`DeviceSpec` + exact `Labels` + `Owner.Agent` + `Owner.SessionID` are equal.
PID/PPID excluded. Project/Xcode/scheme are **not** first-class — encode in Labels.
RELEASED / FAILED / RECLAIMED / CANCELLED never reused.

### QUEUED liveness (R-WP1)

- Cancelled `Acquire(Wait=true)` → `ACQUIRE_CANCELLED` + QUEUED→CANCELLED (persisted).
- Watcher always checks QUEUED owner PID; dead → Release→CANCELLED via ReclaimLoop.
- Wait RPC cancel does **not** cancel another client's QUEUED lease.

## Completed packages

| Package | Commit / note | State |
|---|---|---|
| M0–M4 (v0 scaffold) | prior `master` lineage | historical complete |
| Phase A P0 harness (partial) | `593a051` STOPPED/BLOCKED | not a Phase A pass |
| **R-WP0** | `0757144` | complete |
| **R-WP1** | `e980e40` broker; `194b0d1` watcher/daemon/live; `ac21244` docs/status (tip) | complete |

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
