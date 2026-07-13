# Competitor & prior-art notes (from primary-source code study, 2026-07-12)

Repos cloned and inspected directly. Licenses verified from LICENSE files.
Rule (inherited from Lancer process): **pattern reuse with attribution
comment in source; no wholesale code copying**; all licenses below are
permissive but Simurgh is closed for now — attribution comments still
mandatory for borrowed patterns.

## 1. VibeChard — Apache-2.0, Swift — the isolation reference implementation

The closest prior art. Study target for M1/M2 implementers.

**Their seven-resource isolation matrix (adopt wholesale as our §7 flag set):**

| Shared resource | Failure under concurrency | Fix |
|---|---|---|
| `DerivedData` (global default) | build.db locks, module thrash | `-derivedDataPath <per-task>` |
| `ModuleCache.noindex` | Clang module corruption | `CLANG_MODULE_CACHE_PATH` per task |
| SwiftPM global cache | Package.resolved write conflicts | `-clonedSourcePackagesDirPath` per task |
| `.xcresult` bundles | last writer wins | `-resultBundlePath` per run (must be fresh path) |
| Simulator devices | installs/launches collide | `simctl clone` per task |
| PATH lookup of `xcodebuild` | agents bypass flags | PATH shim auto-injecting flags |
| Source tree | standard | git worktree |

**Patterns worth stealing:**
- **Name-pattern as source of truth** for owned devices
  (`vch-warm[<device>:<runtime>]`): survives state-file loss; doctor/reaper
  trust `simctl list` + name, not the journal. → our `simurgh-clone[...]` /
  `simurgh-warm[...]` convention.
- **Warm templates**: primed shut-down device as clone source; create/remove
  explicit; cleaner never touches them; guard against half-primed orphans
  "masquerading" as templates.
- **Prune buckets** (prunable/unmerged/dirty/held) with lsof-based *holder
  detection* (is a process holding files?) and dry-run default. → our prune
  plan/apply.
- **Shim placement**: shim binary in libexec, deliberately *off* PATH,
  reached only via a per-task symlinked bin dir. (For our v0.1.)
- Frozen on-disk state schema (`state.json` schemaVersion) decoupled from app
  version.

**What they conspicuously lack (our whitespace, verified in source):** no
flock/lock/lease anywhere in the codebase; no daemon; no cross-process
arbitration; isolation only exists *inside* a vch-created shell/worktree.
Two vch-ignorant processes still collide.

## 2. XcodeBuildMCP — MIT, TypeScript (Sentry)

Big MCP + CLI toolset for build/test/run/UI-automation; per-workspace daemon
for stateful ops (log capture, debugging).

Take: their per-workspace daemon confirms the need for a resident authority —
but scoped per workspace, not per machine; no leases. Their tool-count (~80)
is the anti-pattern the report warns about: we ship five MCP tools, not
eighty. Their skills/ + manifests/ layout is a decent reference for shipping
agent-facing docs alongside an MCP server. Potential future integration:
their build tools running inside our leases via our env/flags.

## 3. agent-device — MIT, TypeScript (Callstack)

"Hands, eyes, evidence collector — not the brain." Session-aware automation
(XCTest-backed on iOS), token-efficient accessibility snapshots with semantic
refs (`@e3`), evidence captured only when needed, replayable `.ad` scripts.

Take: explicitly *not* an ownership authority (their own framing) — perfect
complement, not competitor. Post-v0: document "agent-device inside a Simurgh
lease" as the recommended verification stack. Their token-efficiency framing
(snapshots sized for agent context windows) applies to our `status --json`
output too: keep payloads lean.

## 4. SandVault — Apache-2.0, shell (Patrick Wyatt)

Sandboxed macOS user account for agents; fresh scratch simulator per session,
deleted on exit; `sandbox-exec` defense in depth.

Take: validates per-session disposable sims and appetite for local agent
containment. Different axis (security sandboxing vs. resource brokering) —
composable with us, not competing. Their uninstall-completeness discipline
(`sv uninstall` removes everything) is a bar our `daemon install`/uninstall
should meet.

## 5. Limrun — commercial, closed; docs studied (docs.limrun.com)

Cloud iOS sandboxes for agents. The **DX bar** for v0, adopted directly:

- `--label key=value` + `--reuse-if-exists` → retries land on the same
  instance; unlabeled reuse matches anything (documented footgun — we copy
  the flag semantics *and* the warning).
- `--inactivity-timeout` / `--hard-timeout` / `--rm` lifecycle flags.
- Last-instance pointer (`~/.lim/last-instances.json`) so bare commands
  default to the newest instance → our `~/.simurgh/last-lease`.
- Agent-first docs page ("CLI for coding agents") that IS the skill, plus
  `lim skills install` distributing agent skills from a repo. → our
  `simurgh init` snippet; possibly `simurgh skills install` later.
- "Watch out for these" section listing agent-specific footguns (big element
  trees vs context windows, delete-defaults) — copy this docs pattern.
- Signed Stream URL (watch the sim in a browser, token embedded) — their
  killer demo feature; our future dashboard equivalent, not v0.

Strategic: they prove willingness-to-pay for agent sim infrastructure, in
cloud form. We are the local complement; if remote execution is ever built,
their instance-lifecycle API shape is the reference.

## 6. Also aware of (not cloned)

- **mobile-mcp** (Apache-2.0): mobile automation via MCP; overlaps
  agent-device; no leases.
- **qckfx**: recorded-flow iOS regression checks; candidate future
  verification backend on top of leases.
- **Apple Device Hub + `devicectl` (Xcode 27)**: inventory/control/diagnostics
  + JSON scripting. No ownership/queueing/policy. Our physical-device path
  (post-v0) goes through `devicectl`; we never clone Device Hub UI.
