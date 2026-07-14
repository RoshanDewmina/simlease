# Simurgh Architecture & Technical Specification

**Version:** 1.0 (2026-07-12) · **Status:** approved for v0 build
**Language:** Go (≥1.23), no cgo (pure-Go deps only) · **Target:** macOS 14+, Apple Silicon primary
**Binary:** one static binary `simurgh`; the daemon is `simurgh daemon run` under a launchd LaunchAgent alias `simurghd`.

---

## 1. Component overview

```text
┌─────────────────────────────────────────────────────────────┐
│  Claude Code / Cursor / Codex / scripts / CI                │
└───────┬─────────────────────┬───────────────────────────────┘
        │ MCP (stdio)         │ CLI (--json)
        ▼                     ▼
┌───────────────┐     ┌───────────────┐
│ simurgh mcp   │     │ simurgh <cmd> │      (same binary)
└───────┬───────┘     └───────┬───────┘
        └──────────┬──────────┘
                   │ Unix domain socket  ~/.simurgh/simurghd.sock
                   ▼            versioned JSON protocol
┌─────────────────────────────────────────────────────────────┐
│ simurghd (daemon, launchd LaunchAgent, single writer)       │
│  ├── Broker        lease lifecycle, queue, capacity         │
│  ├── Provisioner   clone/boot/shutdown/delete sims          │
│  ├── Isolator      per-lease env dirs + flags emission      │
│  ├── Watcher       PID liveness, heartbeats, TTLs, pressure │
│  ├── Reaper        reclamation, orphan detection, cleanup   │
│  └── Store         state journal ~/.simurgh/state.json      │
└──────────┬──────────────────────────────────────────────────┘
           ▼ exec (public interfaces only)
   xcrun simctl · devicectl (post-v0) · sysctl/vm_stat
```

Rules:
- **The daemon is the single writer** of all Simurgh state. CLI/MCP are thin
  clients. No client ever calls `simctl` mutatingly on Simurgh-managed
  devices; reads (e.g. `doctor`) may.
- **Public Apple interfaces only** (`simctl`, `devicectl`, documented
  xcodebuild flags). No private frameworks, no CoreSimulator.framework
  linking. This is the Xcode-churn mitigation.
- **Location transparency:** every lease grant carries a `connection` object.
  v0 always returns `{"transport":"local"}` but clients must read it, so
  remote hosts later are additive, not breaking.

## 2. On-disk layout

```text
~/.simurgh/
├── simurghd.sock            # Unix socket, mode 0600
├── simurghd.pid
├── state.json               # daemon-owned journal (schemaVersion'd)
├── config.yaml              # user config (capacity, defaults, timeouts)
├── logs/
│   └── simurghd.log         # rotating
└── leases/
    └── <lease-id>/          # per-lease isolated environment
        ├── DerivedData/
        ├── ModuleCache/
        ├── SwiftPM/
        ├── Results/         # .xcresult bundles
        ├── tmp/
        └── lease.json       # grant snapshot (for debugging/agents)
```

Per-project (written by `simurgh init`, optional): `.simurgh.yaml` with
default device spec + labels; agent snippet.

**Deletion safety invariant (security-sensitive):** recursive deletion is only
ever permitted on (a) paths strictly under `~/.simurgh/leases/`, and
(b) simulator devices whose *name* matches `^simurgh-(clone|warm)\[.*\]$` AND
whose UDID appears in Simurgh state or matches the name pattern via
`simctl list -j`. Both checks are centralized in one `safety` package —
every destructive call goes through it. No exceptions.

## 3. Data model

```go
type Lease struct {
    ID           string        // "lease-" + short id
    State        LeaseState    // queued|provisioning|active|expiring|released|reclaimed
    Owner        Owner         // PID, PPID, agent label, session id (opaque)
    Labels       map[string]string // user labels, e.g. task=add-paywall agent=claude
    Spec         DeviceSpec    // requested: model, runtime, platform
    Device       *Device       // granted: UDID, name, runtime, cloneOf
    Env          IsolationEnv  // paths + emitted env/flags (see §7)
    Connection   Connection    // {transport:"local"} in v0
    AcquiredAt, ExpiresAt time.Time
    TTL          time.Duration // renewable
    HeartbeatAt  time.Time
    Timeouts     Timeouts      // inactivity, hard (Limrun pattern)
    ReleaseOnPIDExit bool      // --rm
}

type DeviceSpec struct { Platform, Model, Runtime string } // "iOS","iPhone 17 Pro","27.0" (runtime optional → latest)
type Device struct { UDID, Name, Runtime string; CloneOf string; Booted bool }
type Connection struct { Transport string; /* future: Host, Port, Auth */ }
```

Store: `state.json`, atomic rewrite (write temp + rename) on every mutation,
`schemaVersion: 1`. Single-writer daemon makes this safe; SQLite is deliberate
overkill for v0. On daemon start: load journal → reconcile against
`simctl list -j` reality (devices are truth for existence; journal is truth
for ownership) → reap inconsistencies.

## 4. Lease state machine

```text
acquire ──► QUEUED ──► PROVISIONING ──► ACTIVE ──► RELEASED
              │              │             │
              │ cancel       │ provision   ├─ ttl/heartbeat/pid-death
              ▼              ▼  failure    ▼
           CANCELLED       FAILED       RECLAIMED (reaper: shutdown+delete clone, purge env per policy)
```

- **QUEUED:** capacity full or matching device busy. Response includes queue
  position; clients may `wait` (long-poll) or poll.
- **PROVISIONING:** clone from warm template if one matches spec, else clone
  base device (create if missing), rename to
  `simurgh-clone[<lease-id>:<model>:<runtime>]`, boot, wait for `Booted`.
- **ACTIVE:** heartbeat window (default TTL 30m, renew extends). Watcher
  checks: owner PID alive (kill(pid,0)), heartbeat age, inactivity, hard cap.
- **Reclamation** is idempotent and crash-safe: reaper re-derives work from
  (journal ∪ device-name scan), so a daemon crash mid-cleanup self-heals on
  restart. Orphan scan = any `simurgh-clone[...]` device with no journal entry.

Queue policy v0: FIFO per device-spec, no priorities. Fairness/preemption is
v1 (ROADMAP).

Capacity default: `min(floor(totalRAM_GB/6), physicalCores/2, 4)` concurrent
active leases; override in config. Pressure check before grant: available RAM
> 4GB and thermal state nominal, else queue with reason.

## 5. IPC protocol

Unix socket, newline-delimited JSON frames, request/response + server events.

```json
→ {"v":1,"id":"r1","method":"lease.acquire",
   "params":{"spec":{"platform":"iOS","model":"iPhone 17 Pro"},
             "labels":{"task":"add-paywall"},"ttl":"30m",
             "reuseIfExists":true,"wait":true,"releaseOnPIDExit":true,
             "owner":{"pid":4242,"agent":"claude-code"}}}
← {"v":1,"id":"r1","ok":true,"result":{"lease":{ ...Lease... }}}
← {"v":1,"id":"r1","ok":false,
   "error":{"code":"CAPACITY_QUEUED","queuePosition":2,
            "message":"2 leases ahead of you (capacity 3/3 active)",
            "fix":"simurgh status --queue   # or pass wait:true"}}
```

`reuseIfExists=true` renews a compatible ACTIVE lease or attaches to compatible
PROVISIONING. Compatibility requires same Spec, exact Labels including a
non-empty `project`, and **non-empty** Owner.Agent+SessionID (PID ignored).
Missing identity/project or mismatch allocates a new resource. Acquire responses
expose top-level `reused` (`true` for renewal or attach, `false` for new).
Cancelled `--wait` acquires return `ACQUIRE_CANCELLED` and
CANCEL the still-QUEUED lease so capacity is not stranded.

- Methods: `lease.acquire|renew|release|get|list|wait`, `pool.status`,
  `warm.create|list|remove`, `daemon.ping|shutdown`, `prune.plan|apply`.
- Every error: `code` (stable, machine-readable) + `message` (human) +
  `fix` (exact next command). This triple is a product requirement.
- Versioning: `v` field; daemon rejects newer-major with upgrade hint.
- Access boundary: parent directory 0700 + socket 0600 (same-user only); no
  network listener in v0. Owner/SessionID/Labels partition reuse but are not
  authentication—a same-user raw client can forge them and can already
  renew/release a known lease ID.

## 6. Provisioning & warm templates

- **Clone-per-lease** is the default: every ACTIVE lease gets its own device
  instance even for identical specs → N agents × "iPhone 17 Pro" works, and a
  non-cooperating process that grabs the *base* device never touches leased
  clones (blast-radius mitigation for advisory leasing).
- **Warm templates** (VibeChard pattern, see COMPETITOR_NOTES §1): a primed,
  shut-down device named `simurgh-warm[<model>:<runtime>]` used as the clone
  source — cuts clone+boot latency substantially. `simurgh warm create|list|remove`;
  reaper never touches warm templates; the *name pattern is the source of
  truth* for what Simurgh owns (survives journal loss).
- Boot-on-demand v0; optional warm *pool* (pre-booted clones) is v1.
- Runtime resolution: omitted runtime → newest installed matching runtime;
  missing runtime/device type → error with `fix` (exact `xcodebuild
  -downloadPlatform iOS` / Xcode Settings hint).

## 7. Isolation environment (per lease)

Grant returns (and writes to `<lease-dir>/lease.json`):

```json
{"env":{
  "SIMURGH_LEASE_ID":"lease-86dd",
  "SIMURGH_UDID":"4E2A…9F",
  "SIMURGH_DERIVED_DATA":"~/.simurgh/leases/lease-86dd/DerivedData",
  "CLANG_MODULE_CACHE_PATH":"~/.simurgh/leases/lease-86dd/ModuleCache",
  "SIMURGH_RESULT_BUNDLE_DIR":"~/.simurgh/leases/lease-86dd/Results",
  "SIMURGH_SWIFTPM_DIR":"~/.simurgh/leases/lease-86dd/SwiftPM"},
 "xcodebuildFlags":[
  "-derivedDataPath","…/DerivedData",
  "-clonedSourcePackagesDirPath","…/SwiftPM",
  "-destination","platform=iOS Simulator,id=<UDID>"],
 "shellSnippet":"eval \"$(simurgh env lease-86dd)\""}
```

Flag set is VibeChard's proven seven-resource matrix (attribution: VibeChard,
Apache-2.0 — pattern reuse with attribution comment in source). v0 emits
env + flags + `simurgh env <id>` (eval-able exports) and `simurgh exec
<id> -- <cmd>` (runs a command with the env injected). The **PATH shim**
(auto-injecting flags into any `xcodebuild` call, VibeChard-style) is v0.1:
same emission machinery, extra intercept binary — designed for now, not built.

Result-bundle discipline: `-resultBundlePath` requires a *new* path per run;
`simurgh exec` allocates `Results/run-<timestamp>.xcresult` automatically when
it detects an xcodebuild test invocation.

## 8. MCP server (`simurgh mcp`, stdio)

Tools (names stable, results mirror IPC):

| Tool | Params | Returns |
|---|---|---|
| `lease_acquire` | model, runtime?, labels?, ttl?, reuse_if_exists?, wait? | full lease grant (UDID, env, flags, expiry) |
| `lease_renew` | lease_id, ttl? | new expiry |
| `lease_release` | lease_id | confirmation + cleanup summary |
| `lease_status` | lease_id? (default: caller's newest) | lease detail |
| `pool_status` | — | capacity, active/queued leases, sims, pressure |

Tool descriptions are written for agent consumption: they state *when* to call
(`"Call lease_acquire before any simulator interaction. Never pick a UDID
from simctl yourself."`). Descriptions are part of the spec and reviewed like
API surface. Implementation: official Go MCP SDK (`modelcontextprotocol/go-sdk`);
pin version in go.mod.

## 9. CLI surface

```text
simurgh acquire --model "iPhone 17 Pro" [--runtime 27.0] [--label k=v]...
        [--ttl 30m] [--reuse-if-exists] [--wait] [--rm]
        [--inactivity-timeout 10m] [--hard-timeout 3h] [--json]
simurgh release [<lease-id>] [--all --label k=v]
simurgh renew <lease-id> [--ttl 30m]
simurgh status [--queue] [--json]        # pool + leases overview
simurgh list [--label-selector k=v,...]  # leases (Limrun-style selectors)
simurgh env <lease-id>                   # eval-able exports
simurgh exec <lease-id> -- <cmd...>      # run with env injected
simurgh warm create|list|remove --model ... --runtime ...
simurgh prune [--apply]                  # orphan plan, dry-run default
simurgh doctor                           # env checks: Xcode, simctl, runtimes, daemon, socket perms
simurgh init                             # project setup: .simurgh.yaml + agent snippet + .mcp.json entry
simurgh mcp                              # stdio MCP server
simurgh daemon run|status|stop|install   # install = write LaunchAgent plist
```

Conventions: no lease-id argument → most recent lease owned by caller
(pointer in `~/.simurgh/last-lease`, Limrun pattern) · `--json` on every
command · exit codes: 0 ok, 2 queued/blocked (with structured reason on
stdout), 3 invalid input, 4 environment problem, 5 internal.

## 10. Daemon lifecycle

- `simurgh daemon install` writes
  `~/Library/LaunchAgents/com.simurgh.daemon.plist` (KeepAlive, socket
  activation not required — plain launch at login). Clients auto-start the
  daemon on connection failure by `launchctl kickstart` falling back to
  direct spawn, then retry once.
- Graceful shutdown: stop granting, persist journal, leave ACTIVE leases and
  devices untouched (restart reconciles). Reaper never runs destructive
  actions during the first 10s after start (reconciliation window).
- Logging: structured (slog), rotating file + `simurgh daemon status --logs`.

## 11. Testing strategy

- **Unit (CI, Linux-able):** all broker/queue/state-machine/reaper logic
  behind a `SimctlClient` interface with a scripted fake. Target ≥80% on
  `broker`, `store`, `safety` packages. `go vet` + `staticcheck` clean.
- **Integration (macOS runner or dev Mac):** tagged `//go:build darwin_sim`
  suite against real `simctl`: full lease lifecycle, crash reclamation
  (SIGKILL the client), double-acquire exclusion, orphan prune. These are the
  ROADMAP acceptance gates.
- **Concurrency proof tests (the product's whole point):**
  1. two clients acquire same spec concurrently → two distinct UDIDs;
  2. same *device* requested exclusively → second queues;
  3. SIGKILL owner → lease reclaimed ≤ watcher-interval, clone deleted;
  4. daemon SIGKILL mid-provision → restart reconciles, no orphans, no
     deleted user devices;
  5. `prune` never plans deletion of a non-`simurgh-*` device (property test
     over adversarial `simctl list` fixtures).
- Race detector on in CI (`go test -race ./...`).

## 12. Security & safety review classes

`sensitive` (strongest-model full-diff review, mandatory — see HANDOFF_PROMPT):
`safety` package (deletion guards) · reaper/prune actions · socket setup +
permissions · anything invoking `simctl delete|erase` or `os.RemoveAll` ·
LaunchAgent install. Everything else: standard pipeline.

## 13. Deliberate non-designs (v0)

No SQLite · no gRPC/protobuf (JSON frames suffice at this scale) · no network
transport · no plugin system · no config UI · no telemetry (decide at release;
closed source for now) · no Android/desktop abstractions leaking into core
types (DeviceSpec.Platform is a string enum, that's the extent of it).
