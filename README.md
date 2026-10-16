# SimLease

**A lease broker that gives parallel AI coding agents their own iOS simulators.**

*Formerly Simurgh: the binary, Go module and commands still use the name `simurgh`.*

When several coding agents (Claude Code, Cursor, Codex, scripts) build and test
the same iOS app on one Mac, they share one simulator, one DerivedData folder and
one SwiftPM cache. They install over each other's builds, launch the wrong app
variant, and screenshot each other's screens. Simulators left behind by a crashed
agent pile up until someone cleans them by hand.

SimLease is a small Go daemon that makes the simulator a leased resource. An agent
asks for a device, gets an exclusive simulator clone and its own isolated build
directories, and gives it back when it finishes. If the agent dies, the daemon
reclaims the lease and deletes the clone.

## Results

Measured on one 16 GB MacBook Air (Apple silicon), Xcode 27, building and
launching a fixture app whose build flag stamps a per-agent badge on screen, so
cross-contamination is visible in screenshots. Every number traces to committed
JSON in [`bench/evidence/`](bench/evidence/); the full write-up, including the
unflattering results, is in [`bench/RESULTS.md`](bench/RESULTS.md).

| Scenario | Shared simulator (naive) | Hand-written isolation script | SimLease |
|---|---|---|---|
| **4 agents in parallel**, 20 tasks (B2) | 3/20 passed; 5 install clobbers, 9 screenshots showing another agent's app | 20/20 | **20/20, zero incidents, zero leftover devices** |
| **Agent killed mid-build** (B4) | — | killed agent's simulator orphaned in 5/5 runs | lease reclaimed and clone deleted in 4/4 cleanly measured runs |
| **Solo overhead** (B3), median per task | 7.3 s | — | 48.7 s (cold DerivedData + clean install; a careful hand-written script pays about the same) |

Not everything was a clean win, and the write-up says so: at 2 agents (B1) the
first-attempt pass rate was 17/20, and with 6 agents on 3 slots (B5) only 10/18
tasks finished. Dogfooding under 4-agent load also found a real daemon bug (a
queued lease could stall after a transient memory-pressure dip); it was
root-caused, fixed with a regression test, and B2 was re-run to 20/20.

## How it works

```text
 Claude Code / Cursor / Codex / scripts
        │ MCP (stdio)         │ CLI (--json)
        ▼                     ▼
   simurgh mcp           simurgh <cmd>          (one binary)
        └──────────┬──────────┘
                   │ Unix socket, versioned JSON protocol
                   ▼
   simurgh daemon (single writer of all state)
     Broker       lease queue, capacity, build slots, memory pressure
     Provisioner  clone / boot / delete simulators (simctl only)
     Isolator     per-lease DerivedData, module cache, SwiftPM, results
     Watcher      client PID liveness, heartbeats, TTLs
     Reaper       reclaims dead leases, finds orphaned clones
     Store        journaled state, reconciled on restart
```

- **Leases, not locks.** Each lease is an exclusive simulator clone with a TTL.
  MCP leases are bound to the requesting process by default (CLI: `acquire --rm`),
  so the lease is released when that process exits. Requests that can't be
  granted wait in a queue instead of colliding.
- **Isolation is emitted, not hoped for.** A lease carries its own
  `-derivedDataPath`, `-clonedSourcePackagesDirPath` and `-destination`;
  `simurgh exec` injects them into any `xcodebuild` call.
- **Safe deletion.** Every destructive call goes through one `safety` package
  that only deletes paths under `~/.simurgh/leases/` and devices whose names and
  UDIDs SimLease created. It never touches a developer's own simulators.
- **Public Apple interfaces only** (`xcrun simctl`, documented `xcodebuild`
  flags). No private frameworks.
- **Agent-native.** A stdio MCP server exposes seven tools (`lease_acquire`,
  `lease_renew`, `lease_hold`, `lease_unhold`, `lease_release`, `lease_status`,
  `pool_status`); `simurgh init` registers it in a project's `.mcp.json`.

Details: [Architecture](docs/ARCHITECTURE.md).

## Codebase

- Go 1.25, no cgo. About 15k lines of Go plus 17k lines of tests (393 test
  functions across 22 packages), built with the official MCP Go SDK.
- Unit tests run against a scripted fake of `simctl`. An integration suite and
  the benchmark harness run against real simulators on a Mac.
- Built in July 2026 with AI coding agents (Claude Code and Cursor) working
  under Roshan Silva's direction: he set the architecture, scope and acceptance
  gates and verified the results.

## Run it

Requires macOS with Xcode and an iOS Simulator runtime, plus Go 1.25+.

```bash
go build -o simurgh ./cmd/simurgh
go build -o simurgh-shim ./cmd/simurgh-shim   # needed beside simurgh for `simurgh shell`
export PATH="$PWD:$PATH"

simurgh doctor                 # checks Xcode, simctl, runtimes, daemon
simurgh daemon run &           # start the daemon

simurgh acquire --model "iPhone 17 Pro" --wait --json   # note .lease.id, e.g. lease-1
simurgh exec lease-1 -- xcodebuild \
  -project test/fixtures/SampleApp/SampleApp.xcodeproj -scheme SampleApp build
simurgh release lease-1        # tears down the clone
```

`simurgh exec` injects the lease's build flags and renews the lease during long
builds. `simurgh env <lease>` prints the same isolation as `export` lines.

To use it from an MCP client, run `simurgh init` in your project, then restart
the agent session so it loads the `simurgh` server from `.mcp.json`.

Run the tests with `go test ./...`.

## CLI reference

| Command | Description |
|---|---|
| `simurgh acquire --model NAME [flags]` | Request a lease (clone + boot); `--wait` blocks on queue/pressure; `--json` for machine output |
| `simurgh release [<lease-id>] [--all --label k=v] [--json]` | Release lease(s); tears down the clone device |
| `simurgh renew <lease-id> [--ttl DUR] [--json]` | Extend an active lease's TTL |
| `simurgh hold <lease-id> [--duration DUR] [--json]` | Suppress inactivity reclaim while busy (TTL still applies) |
| `simurgh unhold <lease-id> [--json]` | Clear a hold so inactivity reclaim resumes |
| `simurgh status [--queue] [--json]` | Capacity, build slots, lease counts, memory pressure |
| `simurgh list [--label-selector k=v,...] [--json]` | List leases |
| `simurgh env <lease-id> [--json]` | Print eval-able isolation `export` lines (or JSON env/flags) |
| `simurgh shell <lease-id>` | Interactive shell with lease env + audit-mode PATH shims for `xcodebuild`/`xcrun` |
| `simurgh exec <lease-id> -- <cmd...>` | Run a command with isolation env; injects xcodebuild flags |
| `simurgh warm create\|list\|remove --model NAME [--runtime VER] [--json]` | Manage warm clone-source templates |
| `simurgh integration xcodebuildmcp start\|stop\|status --session <id> [--json]` | Run an XcodeBuildMCP instance scoped to an active lease |
| `simurgh prune [--apply] [--json]` | Plan (default) or apply orphan/held cleanup |
| `simurgh doctor [--json]` | Environment diagnostics (does not auto-start the daemon) |
| `simurgh init [--force] [--json]` | Project `.simurgh.yaml` + `.mcp.json` registration |
| `simurgh mcp` | Stdio MCP server |
| `simurgh daemon run` / `simurgh daemon status [--logs] [--json]` | Run the daemon in the foreground / probe it without starting one |

Not built yet: `daemon stop` and launchd install helpers (stop the daemon with a
signal), and flag injection for bare `xcodebuild` calls outside `simurgh exec`.

## Docs

- [Architecture](docs/ARCHITECTURE.md)
- [Benchmark results](bench/RESULTS.md) and [publishable numbers](bench/LAUNCH_NUMBERS.md)
- [Agent guide](docs/AGENT_GUIDE.md), for AI agents using leases or MCP
- [Troubleshooting](docs/TROUBLESHOOTING.md)
- [Roadmap](docs/ROADMAP.md) and [changelog](CHANGELOG.md)

## Status and license

Version `v0.0.1`. No license file has been added yet, so no license is granted;
that decision is pending with the owner.
