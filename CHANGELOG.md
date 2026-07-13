# Changelog

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
