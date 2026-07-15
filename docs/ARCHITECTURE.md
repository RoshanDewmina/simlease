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
│  ├── ControlPlane  session/job + independent build slots     │
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

Queue policy v0: FIFO per device-spec under a **host-global** capacity gate,
with deterministic oldest-first promotion across specs when a slot frees.
Full priority/aging remains later (not R-WP3).

Capacity default: `min(floor(totalRAM_GB/6), physicalCores/2, 4)` concurrent
**host-global** ACTIVE+PROVISIONING leases (not per DeviceSpec). Override with
`SIMURGH_CAPACITY` (same env name as before; semantics migrated from per-spec
to global — a previous value of `2` no longer allows `2 × N specs`). Optional
`SIMURGH_PER_SPEC_CAPACITY` and `SIMURGH_MAX_PROVISIONING` add independent
limits. Grant checks run in order: global simulator slots → provisioning
slots → per-spec → pressure. Pressure check before grant: available RAM
> 4GB and thermal state nominal, else queue with reason `pressure`.

`CAPACITY_QUEUED` carries machine-readable `reason` / `resourceClass`:
`simulator_slot`, `provisioning_slot`, `per_spec_slot`, or `pressure`.
Device deny order is unchanged by R-WP3.

### Build slots (R-WP3)

Independent of simulator capacity, the daemon hosts an in-memory control plane
(`internal/controlplane`) with configurable build slots via
`SIMURGH_BUILD_SLOTS` (default 2). Build grants use resource class `build_slot`
and never alter `lease.acquire` error shapes. `pool.status` / MCP `pool_status`
additively expose `buildSlots`, `activeBuilds`, and `queuedBuilds`.
Only `Wait:true` creates a FIFO build waiter counted by `queuedBuilds`.
`Wait:false` returns structured `BUILD_QUEUED` / `build_slot` retry metadata
without leaving a waiter and can never consume a later-freed slot.
A waiter remains scheduler-owned after FIFO promotion until its caller accepts
the exact grant. If context cancellation wins that ready/cancel race, the
scheduler atomically releases that promoted grant and advances the next waiter.

Session / Job / Worker / Workspace / Artifact / Event types and the job FSM
(`queued` → … → `succeeded`, plus `failed` / `cancelled` / `orphaned` /
`retrying`) live in that package. State is **non-durable** for this package
(no SQLite). Existing lease CLI/MCP/IPC methods remain the public compatibility
adapter; no new public session/job RPCs in R-WP3. Simulator leases are held
only during device phases (`waiting_for_device` … `testing`).
Worker and Artifact are intentionally domain types only in R-WP3; lifecycle
and persistence arrive in their roadmap packages rather than speculative APIs.

Job state/resource mutations use a private generation check. Cancel/Fail claim
their terminal state and timings under the control-plane lock before cleanup;
they then release only the snapshotted resource IDs. IDs remain on the terminal
Job until exact release succeeds, so an idempotent terminal call can retry
cleanup. Device acquisition also commits atomically; a losing acquisition race
releases only its newly acquired lease. Retry edges are exactly
`failed → retrying → queued`.

`pool.status` exposes `capacity`, `perSpecCapacity`,
`maxProvisioningOperations`, `buildSlots`, `activeBuilds`, and `queuedBuilds`
alongside lease counts and pressure.

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
            "reason":"simulator_slot","resourceClass":"simulator_slot",
            "message":"2 leases ahead of you (capacity 3/3 active) [simulator_slot]",
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
- **Readiness (R-WP4):** after `simctl boot`, wait via bounded
  `PollReadiness` (never an infinite loop). Primary probe is device state
  `Booted` from `simctl list`; timeout is classified as `READINESS_TIMEOUT`
  (provision maps it to `BOOT_TIMEOUT`). An optional weaker fallback probe
  may accept after the primary deadline — still bounded.

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

**Writable-cache isolation (R-WP4):** DerivedData / ModuleCache / SwiftPM /
Results / tmp are always under the lease root. `ValidateWritableCachesUnderLeaseRoot`
rejects escapes and default `~/Library/Developer/Xcode/DerivedData` (and
SwiftPM cache) paths. Default Xcode trees stay untouched.

**Warm DerivedData seeds (R-WP4):** `simurgh exec` may CoW-clone
(`cp -Rc`) an immutable shared seed into an empty lease DerivedData before
build. Compatibility key (exact match required — any mismatch → cold/no-seed):

```text
project/workspace identity | scheme | configuration |
Xcode version/build | SDK | runtime | Package.resolved hash | policy version
```

Shared seed is immutable; each lease gets a private writable clone. Non-empty
destinations are never overwritten. Clone failure / unsupported volume →
bounded cold fallback (empty lease DerivedData restored). After a successful
build, the lease DerivedData is published back as a new immutable seed for
that key. No latency claims are published from this package
(**implementation-complete / benchmark-deferred**).

**Phase instrumentation (R-WP4):** control-plane job timings record real
clock samples for JobState phases and, additively, the aliases
`acquire` / `build` / `install` / `launch` / `test` / `capture`. No fabricated
measurements.

## 8. MCP server (`simurgh mcp`, stdio)

Tools (names stable, results mirror IPC):

| Tool | Params | Returns |
|---|---|---|
| `lease_acquire` | model, runtime?, labels?, ttl?, reuse_if_exists?, wait? | full lease grant (UDID, env, flags, expiry) |
| `lease_renew` | lease_id, ttl? | new expiry |
| `lease_release` | lease_id | confirmation + cleanup summary |
| `lease_status` | lease_id? (default: caller's newest) | lease detail |
| `pool_status` | — | global capacity, optional per-spec/provisioning caps, buildSlots/activeBuilds/queuedBuilds, active/queued leases, pressure |

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

## 14. XcodeBuildMCP integration adapter (R-WP6)

`simurgh integration xcodebuildmcp start|stop|status --session <id> [--json]`
is a **bounded session adapter**: it starts/stops an XcodeBuildMCP child
process scoped to one lease, binding it to that lease's isolated
`Env`/`Bindings` (own UDID, own `DerivedData`/`SwiftPM`/module-cache paths)
so a second concurrent session's adapter cannot see or mutate the first's
resources. `Stop` rejects a foreign PID; ownership is tracked per lease, not
globally. Implementation: `internal/integrations/xcodebuildmcp`.

**Ownership lives in the daemon, not the CLI process (review finding B2,
fixed).** `Adapter.Start` spawns the real `npx xcodebuildmcp mcp` child and
keeps it alive across separate `start`/`stop`/`status` invocations via
`stdinHolders` — an in-process map holding the write end of the child's
stdin pipe open, because a stdio-transport MCP server typically exits on
stdin EOF. Before the fix, `cmd/simurgh/integration.go` constructed a
brand-new `*xbmcp.Adapter` (empty `stdinHolders`) on every one-shot CLI
process (`main.go`'s `os.Exit(runIntegration(...))` model). The instant the
`start` process exited — right after printing its own status — the OS
closed the stdin fd it held, the child saw EOF, and the adapter typically
exited with it: the documented "start now, stop/status later as separate
invocations" contract could not work through the actual public interface.
Every existing test was blind to this because they all called
`Start`/`Stop` within one long-lived Go test process, never through
separate real CLI invocations.

The fix moves `Adapter.Start`/`Stop`/`Status` execution into the **daemon**
(`internal/daemon`), which already owns leases/capacity for exactly this
reason — long-lived state reached via IPC from one-shot CLI commands, not
reconstructed per call:

- `cmd/simurgh`'s `runDaemon()` constructs exactly **one** `*xbmcp.Adapter`
  at daemon startup (alongside the broker/reaper/control-plane), whose
  `GetLease` closure calls `broker.Broker.Get` directly in-process (no IPC
  round-trip needed — the adapter now lives where the broker lives). That
  single `Adapter`'s `stdinHolders` map persists for the daemon process's
  actual lifetime, so a child started by one CLI invocation stays owned and
  alive when a later, separate CLI invocation calls `stop` or `status`.
  `GetLease` returns `broker.NewLeaseNotFoundError` (not a bare
  `fmt.Errorf`) on an unknown lease, so an unknown-session `start` is
  classified as `LEASE_NOT_FOUND`/exit 3 — the same broker-typed error the
  daemon's own `lease.get` handler returns — instead of falling into
  `xbmcpErrorToIPC`'s message-matching fallback as generic `INTERNAL`/exit 5
  (second-review Fix 3; regression-tested by
  `TestXBMCPStartUnknownSessionOverRealIPCReturnsLeaseNotFound` in
  `internal/daemon` and `TestXcodeBuildMCPCLIStartUnknownSessionReturnsLeaseNotFound`
  in `cmd/simurgh`).
- `internal/daemon.RegisterXcodeBuildMCPHandlers` registers
  `integration.xcodebuildmcp.start|stop|status` on the `*ipc.Server`,
  following the exact pattern `RegisterLeaseHandlers` already established
  (a `Deps`-backed `handlers` struct method per method, `ipc.Error` wire
  triples on failure). `Deps.XBMCPAdapter` carries the one long-lived
  `*xbmcp.Adapter` in.
- `cmd/simurgh/integration.go`'s `runXBMCPAction` is now a thin IPC client:
  it calls `dialAndCall(ctx, "integration.xcodebuildmcp."+action, ...)` —
  the same helper `lease.*` commands already use — and renders the result
  with the same `printXBMCPStatus`/`printXBMCPErr`/`printErrorTriple`
  functions as before. The CLI's flags, JSON shape, and error-triple format
  are unchanged; only where the `Status`/error value comes from moved.
- `Adapter.stdinHolders` is guarded by an unexported `Adapter.stdinMu
  sync.Mutex` (second-review Fix 1). Before B2, every CLI call got a
  throwaway `*Adapter`, so this in-process map was never touched by more
  than one goroutine; after B2, the daemon's single shared `*Adapter` is
  reached from a goroutine per IPC connection, so concurrent `start`/`stop`
  calls (different sessions, or the same one) write/read/delete it
  concurrently. All other `Adapter` fields are set once at construction and
  only read afterward, so only `stdinHolders` needed a lock.
  `TestXcodeBuildMCPStdinHoldersConcurrentAccessRace`
  (`internal/integrations/xcodebuildmcp`) reproduces `WARNING: DATA RACE`
  reliably on the pre-mutex code under `go test -race` and passes with the
  mutex in place.

**B2 regression proof (`TestXcodeBuildMCPStatusSurvivesSeparateCLIProcess`,
`darwin_sim`-tagged, `cmd/simurgh`):** this is the test that would have
caught B2 and every future regression of it — it does what no prior test
did: run `start` as one real subprocess that is allowed to fully exit, then
run `status` as a genuinely SEPARATE, later subprocess against a real
daemon and a real ACTIVE lease/device, and assert `Running: true` with the
same owned PID. It also confirms `stop` (a further separate subprocess)
still reaches the same daemon-owned adapter and status flips to
`Running: false` afterward. Two test-only env seams make this practical on
a shared dev host without weakening the proof: `SIMURGH_XBMCP_TEST_COMMAND`
substitutes a fake long-lived child for the real `npx xcodebuildmcp mcp`
server (mirrors the `Adapter.Command` override the live runtime test below
already uses), and `SIMURGH_TEST_SKIP_PRESSURE` bypasses the host RAM
pressure gate (`internal/pressure`), which is unrelated to B2 and was
otherwise the test's bottleneck on a loaded machine.

**Both env seams are compiled out of the real shipped binary at build time
(second-review Fix 2).** The first version of this fix read both env vars
unconditionally inside `runDaemon()` with no gate at all — reachable in the
real `simurgh daemon run` binary via any process's environment (a detached
daemon inherits its triggering CLI subprocess's env), not just deliberate
test harnesses: one substituted an arbitrary binary for XcodeBuildMCP inside
the lease isolation env, the other silently and permanently disabled the
pressure gate. The fix follows the same build-tag split this codebase
already uses for `darwin_sim`-gated live test files: the two env-var reads
live only in `cmd/simurgh/xbmcp_test_seams_darwin_sim.go`
(`//go:build darwin_sim`), behind `applyXBMCPTestCommandOverride` and
`applyTestSkipPressureOverride`; `cmd/simurgh/xbmcp_test_seams_other.go`
(`//go:build !darwin_sim`) provides pure no-op implementations of the same
two functions. `runDaemon()` calls both unconditionally; which
implementation links in is decided purely by the build tag. A tagless
`go build ./...` — how the real binary ships — links the no-op file, so
neither env var is ever read in production, regardless of what any calling
process's environment contains. `TestXcodeBuildMCPStatusSurvivesSeparateCLIProcess`
now builds its nested test binary with `-tags darwin_sim` explicitly (it
depends on the real, gated implementation to substitute the fake XcodeBuildMCP
command and skip the pressure gate); a tagless build of that same test binary
would silently no-op both seams instead.

**Live runtime validation (`TestXcodeBuildMCPRuntimeTwoSessionAttribution`,
`darwin_sim`-tagged, real `xcrun simctl` + real `npx xcodebuildmcp`):** two
concurrent leases, two adapter instances, two real `xcodebuildmcp simulator
build` invocations against a fixture project. Result: distinct UDIDs,
distinct `DerivedData` roots, distinct adapter PIDs, zero growth in the
shared `~/Library/Developer/Xcode/DerivedData` (no global pollution), and
stopping session A's adapter left session B's adapter and DerivedData
intact. Verdict recorded by the test as `"composes"` — XcodeBuildMCP
sessions compose with Simurgh's per-lease isolation model under this
one bounded two-session scenario.

**Scope of the claim (do not over-read):** this is a functional/isolation
proof only, run once, on one host, with one fixture project and two
sessions. It is explicitly **not** a latency or performance claim, not a
stress/concurrency-scale test, and not an independent-review pass — see
`docs/plans/orchestrator-state.md` for the exact commands and dated entry.
