# Bug fix — watcher reclaims on owner-PID-death unconditionally, breaking every CLI-acquired lease

## What's broken, proven live during the M3.3 dogfood run

`internal/watcher/watcher.go`'s `decide()`:
```go
func (w *Watcher) decide(lease broker.Lease, now time.Time) (string, bool) {
	alive := w.isProcessAlive(lease.Owner.PID)
	if lease.ReleaseOnPIDExit && !alive {
		return "owner process exited", true
	}
	if !alive {
		return "owner process not alive", true
	}
	...
```
The second `if !alive` block is unconditional and fires regardless of
`ReleaseOnPIDExit` — the first block is dead code, since anything it would
catch, the second catches too. Every CLI-acquired lease's `Owner.PID` is set
to `os.Getpid()` in `cmd/simurgh/lease.go`'s `acquire` command — the
**one-shot `simurgh acquire` process's own PID**, which exits the instant
the command returns. Combined, this means: **every plain `simurgh acquire`
call (no `--rm`) has its lease reclaimed — and its device deleted — within
one watcher tick (5s) of the `acquire` command itself returning**,
regardless of TTL, regardless of `ReleaseOnPIDExit` being `false`.

**Live-reproduced consequence:** two dogfood subagents each ran `simurgh
acquire` (5m TTL, `releaseOnPIDExit: false`), then tried to `simurgh exec`
a real `xcodebuild` against the leased device seconds later. One build
happened to finish in 12.8s and used the device before it was deleted; the
second build started slightly later and hit `xcodebuild: error: Unable to
find a device matching the provided destination specifier` because the
device was already gone. Both leases showed `state: "reclaimed"` in
`simurgh list` well before their 5-minute TTL. `simurgh release` on either
then failed with `INVALID_STATE: lease is reclaimed`.

The `--rm`/`ReleaseOnPIDExit` CLI flag documented in
`docs/ARCHITECTURE.md` §9 (`simurgh acquire ... [--rm] ...`) only makes
sense as an *opt-in* behavior — if PID-death reclaim already happens
unconditionally, the flag would be meaningless. The correct reading,
confirmed by this real failure, is: **PID-death is a reclaim trigger ONLY
when the lease's `ReleaseOnPIDExit` is `true`.** Without `--rm`, a lease
lives until TTL/inactivity/hard-cap — exactly the semantic a one-shot CLI
invocation followed by a longer-running build needs.

## Required fix

- `internal/watcher/watcher.go`: `decide()`'s PID check must be gated on
  `lease.ReleaseOnPIDExit`. Only call `w.isProcessAlive` and treat a dead
  owner PID as a reclaim trigger when `ReleaseOnPIDExit` is `true`. When
  `ReleaseOnPIDExit` is `false`, PID liveness must not be checked at all
  for that lease (skip straight to hard-cap/inactivity/TTL). The corrected
  shape:
  ```go
  func (w *Watcher) decide(lease broker.Lease, now time.Time) (string, bool) {
  	if lease.ReleaseOnPIDExit && !w.isProcessAlive(lease.Owner.PID) {
  		return "owner process exited", true
  	}
  	// ... hard/inactivity/ttl checks, unchanged
  }
  ```
- `internal/watcher/watcher_test.go`: this file almost certainly has a test
  from M1.3 asserting PID-death reclaims regardless of
  `ReleaseOnPIDExit` (the original, now-proven-wrong, design) — find it and
  fix its expectation to match the corrected behavior. Add explicit new
  coverage: (1) `ReleaseOnPIDExit: false`, owner PID dead → **no** reclaim
  decision from `Tick` (the lease survives); (2) `ReleaseOnPIDExit: true`,
  owner PID dead → reclaim with reason "owner process exited" (existing
  behavior, still correct); (3) `ReleaseOnPIDExit: false`, owner PID dead,
  but TTL has also expired → reclaim fires anyway, with reason "ttl
  expired" (proving the OTHER reclaim triggers still work independently of
  the PID gate — this isn't "PID-death checking is broken", it's "PID-death
  checking is now correctly scoped").
- Do not change `internal/broker`, `internal/reaper`, `internal/daemon`,
  `internal/safety`, `cmd/simurgh`, or any other package. This is a single,
  narrow, one-file logic fix plus its test file. **Do not** "fix" this by
  changing what PID the CLI records as owner (e.g. switching to
  `os.Getppid()`) — that's a different, unnecessary change; the correct
  fix is entirely in the watcher's decision gating, which also correctly
  scopes the MCP server's long-running-process case (MCP already behaves
  correctly today only because its process happens to stay alive across
  tool calls — that's incidental, not a design guarantee, and this fix
  makes the guarantee explicit and correct for both CLI and MCP callers).

## Acceptance commands

1. `gofmt -w internal/watcher`
2. `go test -race ./internal/watcher/... -v` (paste full test names/output)
3. `go build ./...`
4. `go vet ./...`
5. `go test -race ./...`
6. `$(go env GOPATH)/bin/staticcheck ./...`
7. `git diff --check`
8. **Live `[mac]` regression check — this is what actually matters** (paste
   verbatim; single shell, fresh `$SIMURGH_HOME`): build the daemon+CLI,
   start the daemon, `simurgh acquire --model "iPhone 17 Pro" --ttl 2m
   --wait --json` (no `--rm`), confirm the lease is `active` with a real
   device; **wait 15 seconds** (past 3 full watcher ticks); confirm via
   `simurgh list --json` the lease is STILL `active` (not reclaimed) and
   via `xcrun simctl list devices` the clone still exists and is `Booted`;
   THEN confirm the fix didn't break the legitimate case — acquire a
   second lease WITH `--rm`, note its owner PID, `kill -9` that PID
   directly (simulating a real crashed caller), wait 10 seconds, confirm
   via `simurgh list --json` THAT lease (and only that one) is now
   `reclaimed` and its device is gone. Clean up both, confirm final
   device state shows only `simurgh-base[...]`.

Return verbatim output, changed files, diff stat, and confirm explicitly
that the previously-broken scenario (acquire without `--rm`, wait, still
active) now passes.

## Risk class

`low` code-surface (a small, well-understood conditional fix, no new
destructive capability) but this is reclaim-decision logic that directly
gates real device deletion, so the orchestrator will personally verify the
fix (not just trust green tests) given how this exact class of bug just
caused a live failure.
