# Simurgh PRD — Product Requirements Document

**Version:** 1.0 (2026-07-12)
**Owner:** Roshan
**Status:** approved for v0 build
**Companion docs:** `ARCHITECTURE.md` (how), `ROADMAP.md` (when), `COMPETITOR_NOTES.md` (who else)

---

## 1. One-sentence definition

Simurgh is a local macOS control plane that lets multiple AI coding agents work
on iOS apps **in parallel on one Mac** by handing each agent exclusive,
isolated, automatically-cleaned-up access to its own simulator — queue + lease,
not "hope nobody collides."

## 2. Problem

AI coding agents (Claude Code, Cursor, Codex) increasingly run in parallel —
multiple sessions, background agents, worktrees. Git worktrees isolate *source
code*, but Apple's toolchain shares critical state outside the worktree:

- **CoreSimulator is a shared service.** Two agents can independently discover
  and operate the same simulator UDID: install over each other's builds,
  terminate each other's app, screenshot the wrong run, consume the shared
  pasteboard, erase each other's state.
- **Xcode build state is global by default.** Shared `DerivedData`
  (`build.db` races), `ModuleCache.noindex` corruption, SwiftPM cache write
  conflicts, last-writer-wins `.xcresult` bundles — nondeterministic failures
  under concurrent `xcodebuild`.
- **Nobody owns the machine.** There is no machine-wide authority that knows
  which agent owns which simulator, queues contention, reclaims resources when
  an agent dies, or enforces capacity so a fifth parallel build doesn't melt
  the laptop.

Evidence this is real (see report + COMPETITOR_NOTES.md): XcodeBuildMCP issue
#340 (DerivedData concurrency corruption, since fixed per-workspace but not
machine-wide), VibeChard's entire existence (productized per-task isolation),
SandVault's per-session scratch sims, Limrun's commercial traction on cloud
sim infrastructure.

**The gap:** every existing tool isolates or automates; none *brokers*. Per the
consolidated competitive matrix, no shipped product provides cross-process,
machine-wide simulator leases with queueing, heartbeats, and reclamation.

## 3. Target user

> A developer (or small team) running **2+ concurrent AI coding agent sessions**
> against **native iOS projects** on **one Apple Silicon Mac**, who has hit
> simulator collisions, build-state races, or "which sim is mine?" confusion.

Primary personas:
1. AI-first solo iOS dev running Claude Code + Cursor side by side.
2. Small mobile team (2–10) delegating parallel feature work to agents.
3. Orchestrator setups (e.g. a Fable/Claude swarm) dispatching N coding
   subagents that each need to build + run + screenshot.

Non-targets (v0): web/backend teams, cloud-CI-only teams, enterprises with
Mac-farm orchestration, developers running a single foreground agent who never
parallelize.

## 4. Product pillars

### Pillar 1 — Sim broker / leases (the core)

A resident daemon (`simurghd`) is the machine-wide authority for simulator
ownership.

- `acquire` a device by spec (model + runtime) → get an exclusive lease:
  lease ID + UDID + isolated environment.
- Contention → **queue** (with position feedback), never silent sharing,
  never hard failure by default.
- Leases carry owner PID, agent/session label, TTL, heartbeat. Dead PID or
  missed heartbeat → automatic reclamation + cleanup.
- Capacity limits: the daemon refuses/queues beyond N concurrent booted sims
  (default derived from RAM/CPU), so agent #5 waits instead of thrashing
  everyone.

### Pillar 2 — Host-state isolation

A lease grant includes an isolated build environment so parallel builds don't
race:

- Per-lease `DerivedData`, Clang module cache, SwiftPM clone dir, result
  bundle path, log/tmp dirs (mechanics per VibeChard's proven flag set — see
  ARCHITECTURE §7 and COMPETITOR_NOTES §1).
- Per-lease **simulator clone** (`simctl clone`) so each agent gets its own
  device instance — its own installed apps, data containers, pasteboard —
  even when everyone asks for "iPhone 17 Pro".
- Warm templates (pre-primed clone sources) to cut boot latency.

### Pillar 3 — Orchestration (grows over v1)

Fleet policy on one Mac: pool sizing, queue fairness, resource-pressure
throttling (RAM/CPU/thermal/disk), orphan detection and pruning, `devicectl`
integration for physical devices (post-v0).

### Deferred pillars (explicit, on the roadmap, not in v0)

- **Proof/verification gate** ("done means evidence") — later optional layer
  *on top of* leases. Kept out of v0 deliberately: standalone Simurgh owns no
  completion choke point, and the broker is the whitespace.
- **Remote/hosted sims** — the lease protocol is location-transparent from
  day one (a grant returns connection info, not an assumption of localhost),
  but v0 ships local-only. Ladder: local → your own second Mac → hosted
  (only with demand evidence; Limrun already occupies hosted).

## 5. Agent-first product requirements

Agents are the user. Humans configure; agents consume.

1. **MCP server built in** (`simurgh mcp`): `lease_acquire`, `lease_release`,
   `lease_renew`, `lease_status`, `pool_status`. Tool results carry
   everything the agent needs (UDID, env, expiry) in structured form.
2. **CLI with `--json` everywhere** for bash-driven agents, hooks, CI.
   Machine-readable exit codes; human-readable tables by default.
3. **Paste-ready agent onboarding**: `simurgh init` writes/offers a
   `CLAUDE.md`/rules snippet + `.mcp.json` entry. Agents only use tools
   they're told about — the snippet is product, not documentation garnish.
4. **Actionable failures, never bare errors.** Every refusal explains what
   happened and prints the exact command to fix it (pattern: report §12.6).
5. **DX conventions proven by Limrun** (see COMPETITOR_NOTES §5):
   `--label key=value` + `--reuse-if-exists` (retries land on the same warm
   lease), `--inactivity-timeout` / `--hard-timeout`, `--rm` (release on
   process exit), last-lease pointer so bare commands default sensibly.

## 6. What v0 ships (scope contract)

**In:** daemon (launchd LaunchAgent, Unix-socket IPC) · lease lifecycle
(acquire/queue/renew/release/reclaim) · sim clone provisioning + warm
templates · per-lease build-env isolation (env + flags emission) · capacity
limits + basic queue · CLI (`acquire`, `release`, `renew`, `status`, `list`,
`prune`, `doctor`, `init`, `mcp`, `daemon`) · MCP server · cleanup/orphan
detection · agent onboarding snippet.

**Out (v0):** PATH shim (v0.1, opt-in) · physical devices · proof gate ·
remote hosts · dashboard/GUI · Android · multi-Mac · teams/auth/billing.

## 7. Success metrics (from the report, adapted)

Product health: collisions prevented (queued acquires that would have raced) ·
leases per active day · mean acquire latency (cold vs warm) · orphaned-resource
rate after crashes (target ~0) · % leases cleaned automatically · concurrent
lease high-water mark.

Validation (post-dogfood, 5–10 external AI-heavy iOS devs):
- **Strong:** kept installed + enabled for multiple weeks; agents acquire
  leases without human prompting; users report collision debugging went away;
  requests for the deferred layers (shim, proof gate, remote).
- **Weak:** used only in demos; disabled for latency; "I just use one agent."

Kill criteria (inherited from report §20, still binding): users won't keep
the daemon running · overhead causes disabling · an incumbent (VibeChard,
XcodeBuildMCP, Apple) ships machine-wide leasing · CoreSimulator instability
makes reliable brokering impossible across Xcode versions.

## 8. Competitive positioning

> "Your agents already share your Mac. Simurgh makes them take turns —
> or better, gives each one its own phone."

- vs **VibeChard**: they isolate per-task inside their CLI's world; we broker
  machine-wide across *any* tool, and can sit above VibeChard. Integrate,
  don't rebuild (their isolation flag set is the reference implementation).
- vs **XcodeBuildMCP**: execution tools, per-workspace daemon; no global
  resource authority. Complementary — their builds can run inside our leases.
- vs **Apple Device Hub / devicectl**: human-facing inventory + control
  primitives; no ownership, queueing, or policy. We orchestrate above it.
- vs **Limrun**: they solve it with cloud Macs; we solve it on the Mac you
  already own. Their DX is the bar to meet.

## 9. Monetization posture

Closed source for now (owner decision 2026-07-12; revisit at first release).
No pricing in v0. The commercial thesis if validation succeeds: free/cheap
local core, paid team features (shared policy, fleet view, remote Macs,
audit). Do not build any of that before the validation metrics in §7 are met.

## 10. Risks (top 5, with mitigations)

1. **CoreSimulator churn across Xcode versions** → public `simctl`/
   `devicectl` interfaces only; version-tagged adapters; `simurgh doctor`
   verifies environment.
2. **Advisory leases** (tools that never call us can still grab a sim) →
   v0.1 PATH shim; clone-per-lease also shrinks the blast radius (a rogue
   process grabbing the *base* device doesn't touch leased clones).
3. **Resource exhaustion** (N sims + N builds) → conservative defaults,
   queue-not-overcommit, pressure monitoring before grant.
4. **Destructive cleanup bugs** (deleting a user's real simulator or wrong
   directory) → name-prefix discipline: Simurgh only ever deletes devices
   matching `simurgh-…` naming and paths under `~/.simurgh/` — enforced in
   code and review (security-sensitive path, see HANDOFF_PROMPT).
5. **Small market** → cost stays small: one repo, one binary, dogfood-first.
