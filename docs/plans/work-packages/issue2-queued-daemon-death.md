# Issue #2 — queued acquire survives daemon death

**Goal:** A daemon killed during a queued `acquire --wait` must not leave an
observable orphaned QUEUED lease after restart, and the waiting CLI must emit an
actionable error triple rather than bare `EOF`.

**Why:** W18 reproduced GitHub #2 on `ae676d7`: the wait client printed `EOF`;
the restarted daemon exposed the dead-owner lease as QUEUED until the 5-second
watcher tick cancelled it. Startup truth and CLI recovery must be immediate.

**Write-set:**
- `cmd/simurgh/daemon.go`
- `cmd/simurgh/daemon_test.go` (create if a command-level test is needed)
- `cmd/simurgh/doctor.go`
- `cmd/simurgh/doctor_test.go`
- `cmd/simurgh/lease.go`
- `cmd/simurgh/lease_test.go`
- `internal/daemon/daemon.go`
- `internal/daemon/daemon_test.go`
- `internal/ipc/client.go`
- `internal/ipc/client_test.go`
- `internal/mcp/server.go`
- `internal/mcp/server_test.go`

No other production or documentation files. Do not change watcher cadence and
do not delete simulator devices as part of queued-lease cancellation.

**Required behavior:**
1. After `Broker.Rehydrate` and before serving clients, synchronously cancel and
   persist every QUEUED lease whose owner PID is dead. Keep live-owner queues.
   Re-check state when applying decisions so a concurrently promoted lease is
   never cancelled.
2. A connection lost after a request was sent maps `EOF`, reset, or closed-pipe
   response failures to a stable daemon-disconnected error triple with an exact
   retry/status fix at CLI, MCP, and doctor response-read boundaries. Do not
   misclassify malformed/truncated protocol frames.
3. Preserve normal watcher cleanup as defense in depth.

**Acceptance commands:**
```bash
go test -race -count=1 ./internal/daemon ./internal/ipc ./cmd/simurgh
go test -race -count=1 ./internal/mcp
go test -race -count=10 ./internal/daemon -run 'Queued|Rehydrate|DeadOwner'
go test -race -count=10 ./cmd/simurgh -run 'Daemon.*Disconnect|EOF|IPCError'
go build -o /tmp/simurgh-issue2 ./cmd/simurgh
go vet ./...
```

**Required negative tests:**
- A rehydrated QUEUED lease with a live owner remains QUEUED.
- A stale dead-owner decision cannot cancel a lease promoted to ACTIVE.
- A truncated non-empty IPC frame remains a protocol error, not
  daemon-disconnected.

**Live acceptance:** With `SIMURGH_CAPACITY=1`, hold one ACTIVE lease, start a
second `acquire --wait`, kill the daemon, restart with the same isolated home,
then prove (a) the waiter receives the structured error and (b) the dead-owner
lease is terminal before the first post-restart `list`.

**Risk:** `sensitive` — daemon lifecycle, persisted lease state, IPC errors.

**Evidence clause:**
> Before reporting progress, audit each claim against a tool result from this session. Only
> report work you can point to evidence for; if tests fail, say so with the output; if a step
> was skipped, say that. Paste real command output, never paraphrase it.
