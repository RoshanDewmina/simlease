# Simurgh

Local macOS control plane for parallel AI agent access to isolated iOS
simulators. A daemon leases exclusive simulator clones (queue + TTL instead of
collisions), emits per-lease DerivedData / module-cache / SwiftPM isolation, and
exposes the same surface as a CLI and a stdio MCP server.

**Status:** `v0.0.1`. A private GitHub remote (`origin`) exists; the repo is
currently private on GitHub. No LICENSE file exists yet — licensing/publication
is a pending decision by the owner, not a permanent prohibition. Do not treat
this as open-source, redistribute it, or add a license file until the owner
decides and explicitly confirms.

## Requirements

- macOS with Xcode + an iOS Simulator runtime installed
- Go 1.25+ (see `go.mod`)

## Quickstart

Commands below match the live CLI in `cmd/simurgh` and the syntax used in this
repo’s dogfood / orchestrator evidence. The Makefile’s `build` target only runs
`go build ./...` (compile check) — it does **not** install a binary. Build the
CLI yourself:

```bash
go build -o simurgh ./cmd/simurgh
# optional: install somewhere on PATH, e.g.
#   sudo cp simurgh /usr/local/bin/simurgh
export PATH="$PWD:$PATH"   # if you keep the binary in the repo root
```

Confirm the environment (Xcode, simctl, runtimes, socket, daemon liveness).
`doctor` dials the socket **without** auto-starting a daemon:

```bash
simurgh doctor
```

Start the daemon explicitly (accepted path for a fresh machine):

```bash
simurgh daemon run &
```

Client commands (`acquire`, `env`, `exec`, …) call `ipc.Dial`, which **will**
auto-start `simurgh daemon run` once if the socket is missing or refused. Prefer
an explicit `daemon run &` when you want a known-up daemon; use `simurgh daemon
status` / `simurgh doctor` to check without spawning one.

Acquire a lease (blocks under pool or host pressure until grantable):

```bash
simurgh acquire --model "iPhone 17 Pro" --wait --json
```

Pick the lease id from the JSON (field `.lease.id`, e.g. `lease-1`), then load
isolation exports:

```bash
L=lease-1   # replace with .lease.id from acquire
simurgh env "$L"
eval "$(simurgh env "$L")"
```

`simurgh env` prints `export …` lines for the isolation contract. The same
acquire JSON also carries `.lease.env.xcodebuildFlags` — typically:

`-derivedDataPath`, `-clonedSourcePackagesDirPath`, and
`-destination platform=iOS Simulator,id=<udid>`.

Easiest way to apply those flags: `simurgh exec` injects them for any
`xcodebuild` argv. Against this repo’s fixture (live-proven in M2.1 / M3.3):

```bash
simurgh exec "$L" -- xcodebuild \
  -project test/fixtures/SampleApp/SampleApp.xcodeproj \
  -scheme SampleApp \
  build
```

Expect `** BUILD SUCCEEDED **`. DerivedData lands under the lease directory
(`$SIMURGH_HOME/leases/<id>/DerivedData`, default home `~/.simurgh`), not the
shared Xcode default.

Release when finished — release tears down the clone device (M2.3):

```bash
simurgh release "$L"
```

## MCP setup (Claude Code / other MCP clients)

From a project directory:

```bash
simurgh init
```

That command:

1. Writes `.simurgh.yaml` if missing (default model `iPhone 17 Pro`; use
   `--force` to overwrite).
2. Upserts a `simurgh` entry under `mcpServers` in `.mcp.json`.
3. Prints a short agent-rules snippet to paste into `AGENTS.md` / `CLAUDE.md`.

Confirmed `.mcp.json` shape (M3.2):

```json
{
  "mcpServers": {
    "simurgh": {
      "type": "stdio",
      "command": "simurgh",
      "args": ["mcp"],
      "env": {}
    }
  }
}
```

Claude Code loads MCP servers from `.mcp.json` at session start. After `init`,
restart the session (or open the project fresh) so the `simurgh` server
attaches. Then use the five tools: `lease_acquire`, `lease_renew`,
`lease_release`, `lease_status`, `pool_status`.

## CLI reference

| Command | Description |
|---|---|
| `simurgh acquire --model NAME [flags]` | Request a lease (clone + boot); `--wait` blocks on queue/pressure; `--json` for machine output |
| `simurgh release [<lease-id>] [--all --label k=v] [--json]` | Release lease(s); tears down the clone device |
| `simurgh renew <lease-id> [--ttl DUR] [--json]` | Extend an active lease’s TTL |
| `simurgh status [--queue] [--json]` | Host-global capacity (+ optional per-spec/provisioning caps), buildSlots/activeBuilds/queuedBuilds, lease counts, `pressure` |
| `simurgh list [--label-selector k=v,...] [--json]` | List leases |
| `simurgh env <lease-id> [--json]` | Print eval-able isolation `export` lines (or JSON env/flags) |
| `simurgh exec <lease-id> -- <cmd...>` | Run a command with isolation env; injects xcodebuild flags |
| `simurgh warm create --model NAME [--runtime VER] [--json]` | Create a warm clone-source template |
| `simurgh warm list [--json]` | List warm templates |
| `simurgh warm remove --model NAME [--runtime VER] [--json]` | Delete a warm template |
| `simurgh integration xcodebuildmcp start\|stop\|status --session <id> [--json]` | Own XcodeBuildMCP lifecycle for an ACTIVE lease (session id accepts the lease id); `start` writes ephemeral MCP config under the lease root and launches an owned child, `stop` kills only that child |
| `simurgh prune [--apply] [--json]` | Plan (default) or apply orphan/held cleanup |
| `simurgh doctor [--json]` | Environment diagnostics (does not auto-start the daemon) |
| `simurgh init [--force] [--json]` | Project `.simurgh.yaml` + `.mcp.json` registration |
| `simurgh mcp` | Stdio MCP server (blocks until stdin closes) |
| `simurgh daemon run` | Run the daemon in the foreground |
| `simurgh daemon status [--logs] [--json]` | Reachability probe (does not auto-start); optional log tail |

Not built (deliberately deferred): `daemon stop`, `daemon install` / launchd
helpers. Stop a foreground/background daemon with the usual process signal
(`kill` / Ctrl-C). PATH shim for bare `xcodebuild` is deferred to v0.1.

## Docs

- [Architecture](docs/ARCHITECTURE.md)
- [Roadmap](docs/ROADMAP.md)
- [Agent guide](docs/AGENT_GUIDE.md) — for AI agents using leases / MCP
- [Troubleshooting](docs/TROUBLESHOOTING.md)
- [Changelog](CHANGELOG.md)

## License / distribution

No LICENSE file exists yet, so no license is granted — this is not
open-source and must not be redistributed. A private GitHub remote
(`origin`) exists and the repo is currently private on GitHub; publishing
publicly, adding a license file, and any package-registry publish are
pending decisions by the owner, not yet made.
