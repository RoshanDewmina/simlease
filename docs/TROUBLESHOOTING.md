# Troubleshooting

Fixes below match behaviors this codebase actually implements and issues seen
during live dogfood / pressure / daemon work on macOS.

## Acquire hangs, or returns `CAPACITY_QUEUED`

Not always a bug. Grants are gated by **host-global** pool capacity (optional
per-spec and provisioning caps) **and** host pressure (M4.1: RAM free+inactive
floor, thermal, disk free).

- With `--wait` / MCP `wait: true`, acquire blocks until a grant is possible
  (or until you interrupt). Under real RAM pressure this can look like a hang.
- Without `--wait`, acquire may return `CAPACITY_QUEUED` with machine-readable
  `reason` / `resourceClass`: `simulator_slot`, `provisioning_slot`,
  `per_spec_slot`, or `pressure` (detail may still appear in `message`).

`SIMURGH_CAPACITY` is the host-global ACTIVE+PROVISIONING cap (migrated from
the old per-DeviceSpec meaning). Optional `SIMURGH_PER_SPEC_CAPACITY` and
`SIMURGH_MAX_PROVISIONING` tighten further.

Check:

```bash
simurgh status --json
```

Inspect `capacity` / `perSpecCapacity` / `maxProvisioningOperations`, the
`pressure` block, and active/queued lease counts from `pool.status`. Free
memory, cool thermal state, or release unused leases, then retry with
`--wait`.

## `--wait` acquire returns `ACQUIRE_CANCELLED`

Client disconnect, Ctrl-C, or request-context cancel while still QUEUED cancels
that lease (`QUEUED → CANCELLED`) so capacity is not stranded. The error triple
is `ACQUIRE_CANCELLED` (not bare `INTERNAL` / `context canceled`). Dead owner
PIDs on QUEUED leases are also cancelled by the watcher on the next tick after
daemon restart.

```bash
simurgh list --json
simurgh status --json
```

## `--reuse-if-exists` allocated a new lease

Reuse renews compatible ACTIVE leases or attaches to compatible PROVISIONING.
It requires the same DeviceSpec, **exact** Labels map including non-empty
`project`, and non-empty Owner.Agent + Owner.SessionID (PID ignored). Missing
identity/project, different labels/agent/session, or a terminal prior lease
allocates new. CLI/MCP honor `SIMURGH_SESSION_ID`; without it, CLI gets a fresh
random identity per invocation while MCP's random identity lasts only for that
server process. Set the variable for CLI cross-invocation reuse. Encode
Xcode/scheme in Labels if those must also bound reuse. Check top-level `reused`.

This identity is partitioning, not authentication. Socket/directory modes
0600/0700 limit access to the local user, but another same-user raw IPC client
can forge Owner/Labels and can already renew/release a known lease ID. Treat
session IDs and labels as coordination metadata, not secrets or credentials.

## Daemon not running

```bash
simurgh doctor
simurgh daemon status
simurgh daemon run &
```

Notes:

- `doctor` and `daemon status` dial the socket **without** auto-start — they
  report reality, they never spawn a daemon as a side effect.
- Most other client commands use `ipc.Dial`, which auto-starts
  `simurgh daemon run` once if the socket is missing or refused. Auto-start
  detaches the child (new session, stdin from `/dev/null`); the daemon
  ignores `SIGHUP` so it survives the parent shell exiting. Prefer a long-
  lived `daemon run` or LaunchAgent for production, but Dial auto-start must
  not die with the calling shell.
- `daemon status` exits `4` when unreachable.

There is no `daemon stop` / `daemon install` yet — stop with `kill` / Ctrl-C.

## Lease reclaimed unexpectedly

`--rm` maps to `ReleaseOnPIDExit`:

| Acquire | Behavior |
|---|---|
| **Without `--rm`** (default) | Lease survives the acquiring process exiting. It ends on TTL, inactivity timeout, hard-cap, or explicit `release`. |
| **With `--rm`** | Watcher reclaims when the recorded owner PID exits. |

CLI `acquire` records its own one-shot PID as owner. That process exits as soon
as acquire returns — so `--rm` on a plain CLI acquire is almost always wrong
for multi-step agent work.

M3.3 dogfood hit a bug where PID liveness was checked **unconditionally**;
leases without `--rm` were deleted within ~5s of acquire returning. That is
fixed: PID reclaim runs only when `ReleaseOnPIDExit` is true. If a lease still
dies early today, check TTL / inactivity / hard-timeout flags before assuming a
watcher bug.

## Leftover simulator clones

After a crash or abandoned lease, clones named like `simurgh-clone[…]` may
remain.

```bash
simurgh prune          # plan only — held + orphan buckets
simurgh prune --apply  # actually reclaim / delete
```

`prune` never deletes an ACTIVE lease’s device from the apply path. Stuck
ACTIVE leases need `simurgh release`, not prune. Warm templates
(`simurgh-warm[…]`) and base devices (`simurgh-base[…]`) are not orphans.

## How do I see daemon logs?

```bash
simurgh daemon status --logs
```

Or read the log file directly (default home):

```bash
tail -f ~/.simurgh/logs/simurghd.log
```

Logs rotate by size (`internal/logrotate`, default 10MiB / 3 backups). Override
home with `SIMURGH_HOME` if you use an isolated scratch directory.

## Doctor says something’s wrong

`simurgh doctor [--json]` checks Xcode, simctl, iOS runtimes, socket
permissions, daemon liveness, stale leases, and disk usage. Follow each
finding’s `fix` string — e.g. install a runtime with the cited
`xcodebuild -downloadPlatform iOS` hint, or `simurgh release` a stale ACTIVE
lease (prune cannot fix that).
