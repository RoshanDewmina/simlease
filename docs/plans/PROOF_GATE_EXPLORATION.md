# Proof-gate exploration — 2026-07-17

**Context:** Morning packet commit `1b7a425` (`docs: publish overnight stress-test packet`) on
product HEAD `dcc6bf7`. W-matrix: **17 PASS · 1 BLOCKED · 0 FAIL** ([GAP_LIST.md](GAP_LIST.md)).
This document proposes how Simurgh could layer proof-before-done on top of today's execution
evidence. It is a plan only.

---

## 1. Evidence layering (report §17)

The local `SIMURGH_MASTER_IMPLEMENTATION_REPORT.md` §17 says proof-before-done is a **layer
over execution**, not the first product boundary. The controller should retain a chronological
event timeline; a later repository policy may require `quick` vs `full` verification tiers; a
completion gate must re-check that evidence matches the **current source fingerprint and policy
version**.

### What exists tonight (grounded)

| Layer | Current source | What it proves today |
|---|---|---|
| **Bench JSON / evidence promotion** | `bench/evidence/` copies scrubbed JSON from gitignored `bench/results/<ts>/` ([bench/evidence/README.md](../../bench/evidence/README.md)); `python3 bench/report.py` regenerates summaries | Cross-agent collision economics (B1 wrong-badge PNGs), cleanup residue (B4 `b4-residue-log.jsonl`), capacity (B5), time-to-green. **B6 XcodeBuildMCP composition is not in committed evidence** — live gate still required per README. Phase A **BLOCKED** on guarded preflight (owner `cursor-agent` PID 1202; [MORNING_PUBLISH.md](MORNING_PUBLISH.md)). |
| **PATH-shim audit JSONL** | R-WP5 stage 1: `<leaseRoot>/pathshim/audit.log` ([`internal/integrations/pathshim/audit.go`](../../internal/integrations/pathshim/audit.go), W6 PASS) | Every bare `xcodebuild`/`xcrun` invocation: timestamp, argv, resolved real binary, exit code, `mode: audit`. **Does not** inject flags or block bypass. |
| **Per-lease Results / xcresult** | [`isolator.AllocateResultBundlePath`](../../internal/isolator/isolator.go) → `Results/run-<timestamp>.xcresult` (fresh path per call) | Path allocation exists; **no manifest links builds to lease/session/job or records xcresult in release output**. |
| **XcodeBuildMCP adapter attribution** | R-WP6: ephemeral `config.yaml` + MCP JSON under lease root; per-session DerivedData/UDID bindings ([`internal/integrations/xcodebuildmcp/`](../../internal/integrations/xcodebuildmcp/)); W10 PASS (two sessions, B survived A stop) | Session-scoped child process, config, and isolation env. **Functional/isolation proof** — not a persisted evidence bundle or completion gate. |
| **Cleanup outcomes** | `lease_release` MCP returns `cleanupSummary` ([`internal/mcp/server.go`](../../internal/mcp/server.go)); W8 `--rm` reclaim; W15 prune plan guard; issue #2 QUEUED cancel on daemon death | Teardown requested / lease state transitions. **No structured post-cleanup residue manifest** tied to proof policy. |
| **Matrix / issue evidence** | `/tmp/simurgh-w-evidence`, `/tmp/simurgh-resilience-evidence` (GAP_LIST); GitHub #1–#3 closed with live comments | Operational acceptance for v0 broker behavior. Ephemeral — not a product evidence schema. |

### What §17 requires but is **not implemented**

- Source commit + **dirty fingerprint** (report §8.4, §17 — **no Go code** records this today).
- Worker/Xcode version, runtime identity, policy version on artifacts.
- Unified event timeline across shim audit, builds, tests, adapter lifecycle.
- Optional `verification:` policy (quick/full tiers) and **completion gate** that rejects stale
  evidence.

### Proposed layering (future)

```text
execution (leases, isolation, shims, adapter)  ← v0 shipped
        ↓ append-only artifact + audit sinks
evidence manifest (per lease/session/job)      ← v1.x target
        ↓ policy file defines required tiers
completion gate (freshness + policy match)     ← optional enforcement
```

Sink order: (1) existing per-lease files (audit JSONL, Results/, adapter meta), (2) manifest
aggregator that exports to a retained path before optional `--rm`, (3) policy evaluator. Do not
block acquire/build until sinks exist — match PRD deferral.

---

## 2. Scope boundaries

### v0.1 (shipped — morning packet `1b7a425`)

Per [PRD.md](../PRD.md) §6 and [MORNING_PUBLISH.md](MORNING_PUBLISH.md):

- Daemon + lease lifecycle, sim clone isolation, per-lease build env, seven MCP tools.
- R-WP5 **stage 1** audit-only PATH shims via `simurgh shell` (stage 2 flag injection **out**).
- R-WP6 XcodeBuildMCP session adapter (start/stop/status under lease).
- Cleanup/reclaim, `--reuse-if-exists`, agent onboarding.

**Explicitly not in v0.1:** proof gate, completion choke point, source fingerprinting, evidence
manifest schema, merge policies.

### Deferred (roadmap — not v0)

[PRD.md](../PRD.md) deferred pillar: **Proof/verification gate** ("done means evidence") as an
optional layer *on top of* leases — broker whitespace first.

[ROADMAP.md](../ROADMAP.md): **v1.x** proof-gate layer + evidence manifest schema (report §11.3
/ session detail §14.2).

Report **Phase 7 — Strict isolation and proof policies**: evidence manifest, source freshness,
optional completion/merge policies — after core broker + adapter validation.

### Post-v0 / out of Simurgh-core

- Dashboard / SSE session detail with fingerprint + timeline (report §14.2) — **Someday**.
- Lancer **Verification Contract** / Proof Bundle / mobile proof-to-ship loop
  (local research report `LANCER_PROOF_TO_SHIP_MASTER_REPORT.md` §4.4) — consumer of Simurgh
  evidence, not the broker.
- Hosted workers, signed preview links, billing (report Phase 8).
- PATH-shim **stage 2 enforcement** — separate deferred track (PRD §6 "Out"); audit must precede
  enforce per report §10.

---

## 3. Integration options

### Option A — Local policy file (repo-scoped)

**Shape:** `.simurgh/verification.yaml` (or project convention) beside `.simurgh.yaml`:

```yaml
verification:
  policy_version: 1
  quick: [build, unit_tests]
  full: [clean_build, unit_tests, ui_tests]
  required_for_done: quick
```

**How it works:** Agent or CI reads policy; after work, checks local artifacts (xcresult paths,
audit log lines, test exit codes) before marking done. Simurgh optionally documents paths via
`simurgh env` / lease layout — **no daemon change required for v1**.

| Pros | Cons |
|---|---|
| Lowest coupling; matches "layer over execution" | No enforcement if agent bypasses Simurgh |
| Familiar to Lancer "Verification Contract" pattern | Fingerprint freshness is caller's job unless Simurgh helps compute hash |
| Easy to dogfood in one repo | Policy drift across repos without shared schema |

### Option B — MCP tool(s)

**Shape:** Report §21.2 proposes `capture_evidence`, `session_finish` high-level tools (alongside
today's seven lease tools). Tool responses would return artifact refs + `cleanupSummary`-style
status ([`lease_release`](../../internal/mcp/server.go) already models short summaries).

**How it works:** Agent calls `capture_evidence` after build/test; daemon or CLI aggregates
lease-root sinks into a manifest JSON; `session_finish` runs policy check (if configured) before
`lease_release`.

| Pros | Cons |
|---|---|
| Agent-native; structured IDs + remediation fit existing MCP design | New API surface + versioning |
| Can attach manifest to release without blocking acquire | Still optional unless paired with enforcement |
| Composes with XcodeBuildMCP adapter session dirs | Requires schema design and backward compatibility |

### Option C — Daemon hook (lease/session terminal)

**Shape:** A future `session.finish` validates manifest against policy + source fingerprint and
returns `PROOF_INCOMPLETE` or `PROOF_STALE` (PRD §5 actionable failures). `lease.release` appends
the cleanup result but must remain an unconditional cleanup escape hatch.

**How it works:** The daemon evaluates proof at the explicit finish boundary. The existing
reaper/control-plane terminal path
([`cleanupTerminal`](../../internal/controlplane/controlplane.go)) may append teardown status to
the exported manifest, but failed proof cannot strand a simulator lease.

| Pros | Cons |
|---|---|
| Single choke point for explicit "done means evidence" when enabled | Agents can bypass `session.finish`; release must remain available for safety |
| Freshness check at terminal event | Touches sensitive daemon lifecycle (issue #2 class) |
| Aligns with §17 completion gate | Hardest to ship; needs opt-in default-off |

### Recommended sequencing

1. **Policy file + documented lease artifact layout** (Option A) — schema only, no product gate.
2. **MCP `capture_evidence` / manifest writer** (Option B) — aggregate existing sinks; still
   advisory.
3. **Daemon hook** (Option C) — opt-in `required_for_finish` after dogfood proves false-positive
   rate is acceptable; never make resource cleanup conditional on proof.

Do **not** start with Option C. Report and PRD both defer proof gate until broker + isolation
evidence is trustworthy (W-matrix largely confirms; W12 BLOCKED on live pressure only).

---

## 4. Risks and owner decisions

| Risk | Evidence / mitigation |
|---|---|
| **False "done" without bypass detection** | W6 proves audit logging for managed shell only; agents can still invoke real `xcodebuild` outside shim (report §24 bypass risk). Owner: is stage 2 enforcement a prerequisite for hard gates? |
| **Stale evidence after dirty tree** | No fingerprint implementation. Owner: git dirty-hash scope (tracked only vs include untracked)? |
| **Flaky UI tests at finish** | W16 5/5 acquire/release PASS; no UI-test proof gate exists. Owner: advisory vs blocking `session.finish` default? Release remains unconditional. |
| **Bench / B6 gap** | Committed `bench/evidence/` lacks B6 XcodeBuildMCP composition JSON ([orchestrator-state.md](orchestrator-state.md) P0-3 UNMET). Owner: require quiet-host B6 before claiming adapter evidence in proof manifests? |
| **Ephemeral matrix evidence** | W1–W18 logs under `/tmp/` — not product storage. Owner: promote schema to lease-root manifests vs external store? |
| **Cross-product scope** | Lancer proof-to-ship is mobile control plane; Simurgh stays local broker. Owner: manifest export format for Lancer vs Simurgh-only? |
| **Credential / push policy** | MORNING_PUBLISH: historical `a9443f9` credential material — unrelated to proof gate but blocks public evidence publishing until resolved. |

---

## 5. Future acceptance bar (checkable)

When proof-gate v1 is claimed shipped, **all** of the following must be demonstrable on a quiet
Mac (not merely unit-tested):

1. **Manifest:** After acquire → build (via managed shell or XcodeBuildMCP) → test → finish →
   release, a machine-readable manifest exported to a retained evidence path before optional
   lease-root deletion lists: git commit, dirty fingerprint,
   `xcodebuild`/`xcrun` audit line count or hash, allocated `Results/*.xcresult` path(s), adapter
   session PID/config path (if used), and `cleanupSummary`-equivalent teardown result.
2. **Policy:** A checked-in `.simurgh/verification.yaml` with `policy_version` is evaluated;
   `quick` tier passes on SampleApp fixture; `full` tier fails when a required step is omitted.
3. **Freshness:** Re-run with one dirty tracked file after manifest creation → completion gate
   returns stale fingerprint (structured error, not bare failure).
4. **Non-interference:** W10-class proof: two concurrent sessions still pass attribution; proof
   aggregation does not widen Stop/kill blast radius.
5. **Bench linkage:** At least one promoted `bench/evidence/xcodebuildmcp-composition-<ts>.json`
   (per [bench/evidence/README.md](../../bench/evidence/README.md)) referenced from manifest or
   docs — closes current B6 committed-evidence gap.
6. **Regression gates:** `go test ./... -race`, `go test -tags=darwin_sim ./... -race`, and W6/W7
   spot checks still PASS.

---

## 6. Summary

Simurgh v0.1 already emits **fragmented execution evidence** (PATH-shim JSONL, per-lease Results
paths, XcodeBuildMCP bindings, release summaries, bench JSON promotion workflow). It does **not**
yet implement §17's manifest, fingerprint, policy tiers, or completion gate. The lowest-risk path
is advisory policy file → MCP manifest tools → optional daemon terminal hook, with enforcement
default-off until PATH-shim audit history and B6 composition evidence justify stricter gates.

---

No product code was written for this exploration tonight.
