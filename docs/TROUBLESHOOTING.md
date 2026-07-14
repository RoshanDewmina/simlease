# Troubleshooting

Fixes below match behaviors this codebase actually implements and issues seen
during live dogfood / pressure / daemon work on macOS.

## Acquire hangs, or returns `CAPACITY_QUEUED`

Not always a bug. Grants are gated by pool capacity **and** host pressure
(M4.1: RAM free+inactive floor, thermal, disk free).

- With `--wait` / MCP `wait: true`, acquire blocks until a grant is possible
  (or until you interrupt). Under real RAM pressure this can look like a hang.
- Without `--wait`, acquire may return `CAPACITY_QUEUED` (live example:
  `reason: "ram"` when free+inactive RAM was below the floor).

Check:

```bash
simurgh status --json
```

Inspect the `pressure` block (OK flag + per-signal RAM/thermal/disk detail) and
active/queued lease counts from `pool.status`. Free memory, cool thermal state,
or release unused leases, then retry with `--wait`.

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

Reuse only matches ACTIVE leases with the same DeviceSpec, **exact** Labels map,
and Owner.Agent + Owner.SessionID (PID ignored). Different labels/agent/session,
or a RELEASED/FAILED/RECLAIMED prior lease, allocate a new resource. Encode
project/Xcode/scheme in Labels if those must bound reuse.

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
  `simurgh daemon run` once if the socket is missing or refused.
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
