# Changelog

## Unreleased

Post-v0.0.1 work, tracked as `R-WP*` packages (Phase B). Not yet tagged.

### R-WP0 — baseline protect

- Status/report-only baseline gate before Phase B work started; no
  functional change.

### R-WP1 — reuse + liveness

- `ReuseIfExists`: acquire can attach to an existing compatible lease
  instead of always provisioning a new device.
- Reap dead `QUEUED` owners; cancel stranded queued waits when the
  requesting process has exited.

### R-WP2 — global capacity

- Host-global capacity accounting across specs, with explicit queue
  reasons surfaced on `pool.status` / `simurgh status`.
- Structured capacity-queue metadata exposed over MCP.
- Auto-started daemon detaches from the parent shell (fixes IPC handshake
  hangs when the daemon is spawned by a client process).

### R-WP3 — session/job control-plane

- Session/job model with independent build slots; build-slot status
  surfaced on `pool.status`.
- Race-safe job transitions and atomic claiming of terminal resources.

### R-WP4 — readiness cache

- Bounded readiness polling with classified timeouts (provisioner).
- Additive acquire/build/install/launch/test/capture phase timings
  (control-plane).
- Writable caches confined under the lease root; symlinks resolved before
  cache-confinement checks (isolator).
- Warm-seed compatibility keys and CoW seed/fallback made stricter
  (complete keys required, seed replacement serialized).
- Benchmark cells for this package were explicitly deferred by owner
  directive — implementation and tests are complete, no timed numbers were
  published.

### R-WP6 — XcodeBuildMCP session adapter

- `simurgh integration xcodebuildmcp start|stop|status --session <id>`:
  bounded adapter that owns ephemeral MCP config and child process
  lifecycle for an active lease, moved into the daemon so start/stop/status
  are daemon-owned rather than CLI-process-owned.
- `sessionID` validated on Stop/Status; a live two-session runtime test
  proved XcodeBuildMCP composes with Simurgh's isolation boundaries.
- Went through four independent review rounds. Rounds 1-2 fixed path
  validation, cross-process lifecycle, a data race, two env-var backdoors,
  and an error-classification regression. Round 3 found a same-session
  concurrent-`start` TOCTOU; round 4 confirmed the fix and closed it. One
  narrower, non-blocking finding remains tracked (unbounded lock-map
  growth from malformed session IDs, same-user local IPC only).

R-WP5 (PATH shim) remains gated/not started; no entry here because no code
shipped for it.

## v0.0.1 — 2026-07-13

### M0 — skeleton

- Go module, `cmd/simurgh` CLI entry, Makefile, CI.
- Daemon Unix-socket listener (`simurgh daemon run`), IPC framing, `daemon.ping`.
- `simurgh doctor` environment checks (Xcode, simctl, runtimes, socket, daemon).

### M1 — lease broker

- In-process lease broker: acquire / renew / release, capacity queue, TTL /
  inactivity / hard-cap, optional `ReleaseOnPIDExit`.
- Provisioner: clone + boot simulators via `simctl` (named base / clone devices).
- Safety invariant for clone deletion; watcher + reaper for reclaim decisions.
- Daemon lease / pool RPCs; persistence under `$SIMURGH_HOME`.
- CLI: `acquire`, `release`, `renew`, `status`, `list`.

### M2 — isolation, warm, prune

- Per-lease dirs and env/flag emission (`simurgh env`, `simurgh exec`); SampleApp
  fixture for live builds.
- Warm templates: `warm create|list|remove`, clone-from-warm when present.
- `prune` / `prune --apply`; release triggers device teardown; doctor stale-lease
  and disk-usage checks.

### M3 — MCP + dogfood

- Stdio MCP server (`simurgh mcp`): `lease_acquire`, `lease_renew`,
  `lease_release`, `lease_status`, `pool_status`.
- `simurgh init`: `.simurgh.yaml`, `.mcp.json` upsert, agent-rules snippet.
- Two-agent concurrent dogfood (CLI path): isolated builds, install, launch,
  screenshot, release; watcher PID-reclaim bug fixed (`ReleaseOnPIDExit` only).

### M4 — ops hardening

- Real host pressure gate before grant (RAM / thermal / disk); surfaced on
  `pool.status` / `simurgh status`.
- Log rotation; `simurgh daemon status [--logs] [--json]` (non-auto-starting).
- Static audit: no empty `code`/`message`/`fix` error-triple literals.
- Documentation: README quickstart, agent guide, troubleshooting.
