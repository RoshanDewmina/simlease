# Simurgh session report — 2026-07-15 (overnight continuation)

**Report time:** 2026-07-15 ~08:35 EDT
**Session:** Claude Code, continuing from the 2026-07-14 Sol orchestrator handoff
(`docs/plans/SIMURGH_SOL_SESSION_REPORT_2026-07-14.md`)
**Master tip at report time:** `1f3d4f9` (80 commits ahead of `origin/master` @ `4447e3c`)
**Pushed anywhere:** **no** — nothing from this session has been pushed to `origin`

> **Hard claims (binding):** R-WP6 is merged to local `master` and MERGEABLE after four independent
> review rounds. R-WP5 has a first slice (audit-mode PATH shim) implemented but **uncommitted and
> not yet independently reviewed** — treat it as unverified. The leaked-credential object-DB cleanup
> is done and verified, but the credential's plaintext value still exists in one local commit's own
> diff (`a9443f9`) three commits back on `master` — **do not push `master` to `origin` until that is
> resolved**, on top of the pre-existing Phase A branch push-hold.

---

## 1. Executive summary

This session picked up the prior Sol handoff (R-WP6 paused at `bec8670`, one untracked/unverified
live test) and drove it to a reviewed, merged state, then continued into new work overnight at the
owner's explicit direction ("continue till you're done," "run this overnight"):

1. **Resumed and gated R-WP6** — ran the full gate suite, fixed a genuine compile bug in the
   untracked live test, got the live two-session runtime validation passing with real evidence.
2. **Four independent review rounds on R-WP6**, each one blackbox and skeptical of the last fix.
   Every round found something real:
   - Round 1: unvalidated `sessionID` path traversal (B1), and the adapter's child process
     couldn't survive separate CLI invocations (B2) — both fixed.
   - Round 2 (of B2's own fix): a data race on a newly-shared map, two ungated env-var overrides
     live in the production daemon binary, and a `LEASE_NOT_FOUND` error-classification regression
     — all three fixed.
   - Round 3 (of round 2's fixes): all three confirmed fixed; one new finding — a same-session
     concurrent-`start` TOCTOU that could orphan a child process.
   - Round 4 (of the TOCTOU fix): confirmed fixed, re-derived independently (not trusted from the
     report) — including catching that my own first attempt at closing a related finding broke a
     different, already-tested error-classification contract; caught by running the full regression
     suite before committing, then corrected.
3. **Merged R-WP6 to local `master`** (`a5afb8a`, later commits on top). Not pushed.
4. **Security remediation, round 2**: the Phase A credential-leak rewrite from the prior session's
   report had an unverified caveat — a second independent check found the actual leaked credential
   blob was still physically retrievable from the local git object database (packed as a delta
   base, kept alive by one branch's reflog). Cleaned up surgically (inventoried every dangling
   object first, confirmed all but the leak chain were inert stash byproducts, scoped reflog expiry
   to just the affected branch, repacked, pruned, independently re-verified gone).
5. **Prod-readiness pass**: fixed stale version/help strings, a `.gitignore` gap, a CI Go-version
   mismatch, a stale `CHANGELOG`, and — most importantly — a **false README claim** ("closed source,
   no public remote, do not add a license file") that contradicted the repo's actual state (a real,
   private GitHub remote already exists).
6. **A self-caught mistake**: a final audit found that my own credential-cleanup writeup had quoted
   the actual (already-revoked) secret value in plaintext, undermining its own "zero matches"
   claim. Redacted immediately; documented what the redaction does and doesn't fix (see §5).
7. **Started R-WP5** (PATH shims for `xcodebuild`/`xcrun`), scoped deliberately narrow: an
   audit-mode-only first slice that logs every intercepted call and always execs the real binary
   completely unmodified — zero flag injection, zero command blocking, matching the master
   design's own staged rollout (audit → warn → enforce). **Implemented, unit-tested (18/18
   passing including the recursion guard), but uncommitted and not yet independently reviewed.**
8. **Wired Simurgh into the owner's Lancer project** for actual use: built and installed the
   `simurgh` binary to `~/bin` (on `PATH`), ran `simurgh init` against
   `~/Documents/command-center` (registered the MCP server in `.mcp.json`, wrote `.simurgh.yaml`),
   and added the agent-rules snippet to Lancer's `AGENTS.md`. Smoke-tested `simurgh mcp` actually
   starts and shuts down cleanly.

---

## 2. Owner decisions this session

| # | Decision | Binding effect |
|---|---|---|
| A | "Go with your recommendation" on R-WP5 (retain as scoped) | R-WP5 stays in scope, not shrunk/deferred |
| B | "Fan out subagents... be aggressive... speed is a priority" | Authorized parallel subagent dispatch + local merge of R-WP0-R-WP4 |
| C | Declined SSD migration for disk space (asked twice, declined both times) | Disk-space work done via deletion of regenerable caches instead, not the SSD |
| D | "Delete everything we don't need and continue" | Authorized deleting ~123GB of regenerable Xcode/XcodeBuildMCP/DerivedData cache |
| E | "Continue with the work" (after B2-review round 3's non-blocking finding) | Authorized proceeding to merge R-WP6 rather than a 4th round immediately |
| F | Repo visibility: "check and tell me, don't act on it either way" | Confirmed private; visibility never changed |
| G | Credential rewrite: "verify and resolve it tonight if I can verify it's safe" | Authorized the object-DB cleanup once independently verified as safe (see §5) |
| H | "Start R-WP5 too if R-WP6 wraps up with time to spare" | Authorized starting R-WP5's audit-mode slice |
| I | "Passing this to Fable to use while building Lancer" | Prompted the MCP wiring + AGENTS.md snippet work in §7 |
| J | No push authorized at any point | **Master remains unpushed; explicit owner action required before any push** |

---

## 3. R-WP6 — final state

**Code tip:** `bec8670` (original adapter) → fix commits `84a0ce7`, `740abdb`, `8042efc`, `9494367`,
`428d05a` → merged into `master` at `a5afb8a`.

**What shipped:**
- `simurgh integration xcodebuildmcp start|stop|status --session <id> [--json]` — daemon-owned
  (not one-shot-CLI-owned) child process lifecycle for an XcodeBuildMCP session scoped to one lease.
- `sessionID` validated via the same `internal/safety.ValidateLeasePath` allowlist used elsewhere
  in the codebase (closes B1: path traversal).
- Adapter `Start`/`Stop`/`Status` moved into daemon-owned RPC handlers (`integration.xcodebuildmcp.*`
  on the existing IPC server), so the child process persists across separate CLI invocations
  (closes B2). New env-var test seams are compiled out of production builds entirely via a
  build-tag split (`darwin_sim` vs not), not left as a runtime check.
- `stdinHolders` map access is mutex-guarded (closes the round-2 data race). Lease-not-found
  errors reuse the broker's own typed error instead of a fragile string-matched `fmt.Errorf`
  (closes the round-2 error-classification regression).
- Per-session locking closes the same-session `Start`/`Start` and `Start`/`Stop` TOCTOU (round 3
  finding, round 4 confirmed fix). `Stop`'s lock-map growth is now bounded (validates before
  locking); `Start`'s is **not** — deliberately, to preserve `LEASE_NOT_FOUND` classification parity
  for malformed session IDs (a fix attempt that closed this for `Start` too was tried, broke that
  parity, and was reverted — see `docs/plans/orchestrator-state.md`'s 2026-07-15 entries for the
  full story).

**Known, documented, non-blocking debt:** `Start`'s `sessionLocks` map can grow unboundedly from a
flood of distinct malformed session IDs. Same-user local Unix socket only (independently verified
at the socket-permission level: `0o700` dir, `0o600` socket file). No state corruption, no
isolation-boundary crossing. Fixing it properly needs map eviction, not format-based rejection.

**Verdict:** MERGEABLE, zero blocking findings, four independent review rounds deep.

---

## 4. R-WP5 — audit-mode PATH shim (first slice, uncommitted)

**Scope, deliberately narrow:** stage 1 of the master design's own audit → warn → enforce rollout.
`internal/integrations/pathshim/` + `cmd/simurgh-shim/` — a shim binary meant to be invoked as
`xcodebuild`/`xcrun` (via `os.Args[0]` dispatch) that:
- Resolves the real binary via an **absolute path** (`DEVELOPER_DIR` env var, else
  `xcode-select -p` + well-known suffix) — never a bare `exec.LookPath` while the shim's own
  directory could still be in `PATH`.
- Has a **recursion guard**: sets `SIMURGH_SHIM_ACTIVE=1` before re-exec; refuses to proceed if
  already set, rather than looping. Tested directly (`TestRunRecursionGuardRefusesInProcess`,
  `TestShimBinaryRecursionGuardRefusesInsteadOfLooping`).
- Writes one structured JSON audit-log line per invocation (timestamp, binary, argv, resolved
  path, exit code) — the evidence a future warn/enforce decision would need.
- **Injects nothing, rejects nothing, blocks nothing.** Functionally transparent except for the
  audit-log side channel. Explicitly does not intercept absolute-path callers (a known, documented
  coverage gap, not a bug).

**Verification done:** 18/18 unit tests pass (`internal/integrations/pathshim` +
`cmd/simurgh-shim`), including the recursion guard. `go build ./...` clean with this code present.

**Verification NOT done:** no independent review round yet (unlike R-WP6, which got four). No
`darwin_sim`-tagged live test confirmed run in this final state. **Not committed** — sitting as
uncommitted changes on `master`'s working tree as of this report. Treat as unverified until
reviewed.

**Incident during this work:** partway through, the whole `~/Documents` folder became briefly
inaccessible at the OS level (macOS privacy/TCC-style lockout, not a POSIX permission issue — even
a direct `chmod` as the file owner was refused). Self-resolved or was resolved externally before
diagnosis completed. Plausible but unconfirmed cause: something in this package's testing touched
real paths/permissions incorrectly. **Recommend the owner personally inspect
`cmd/simurgh-shim`/`internal/integrations/pathshim` before trusting it in a live shell**, given this
open question.

---

## 5. Security — credential leak, both rounds

**Round 1 (prior session, 2026-07-14):** history rewrite on
`claude/benchmark-blockers-2026-07-13-37773c` after a `ps aux`-argv-capture leak. Verified this
session: the rewrite correctly replaced *reachable* history (old tip `40a245a` confirmed not an
ancestor of current HEAD `593a051`).

**Round 2 (this session):** the same verification found the real leaked credential body
**still physically retrievable** from the local git object database — a dangling, packed blob kept
alive only by that one branch's own reflog entry. Remediation: inventoried every dangling object
repo-wide (20 total; every one was either a git-stash byproduct from this session's own work or the
exact leak-chain commits), confirmed zero live stashes anywhere, scoped a reflog expiry to just the
affected branch (not repo-wide), repacked, pruned, then independently re-verified via `git cat-file`
and `git fsck --full` that the blob and both leak commits are completely gone. Every worktree/branch
tip re-verified unchanged after the operation.

**Round 3 (this session, self-caught):** a final audit found that my own round-2 writeup
(`docs/plans/orchestrator-state.md`, commit `a9443f9`) had quoted the actual leaked credential value
in plaintext — reintroducing it into reachable history the same night it was supposedly cleaned up.
Redacted in commit `1f3d4f9`. **This redaction fixes current file content only** — the value still
exists in `a9443f9`'s own historical diff, three commits back on `master` with other work built on
top. Excising it fully needs a rebase of those commits, which was deliberately **not** done
autonomously (bigger, more disruptive operation than the round-2 cleanup, and the credential is
already confirmed revoked/rotated, so the residual risk of an unpushed local commit is low, not an
active-compromise scenario).

**Owner decision needed:** accept `a9443f9`'s historical diff as known, low-risk, local-only debt
(simplest), or authorize a deliberate rebase before any push. **Either way: master must not be
pushed to `origin` until this is decided**, independent of the separate Phase A branch push-hold.

---

## 6. Prod-readiness fixes (all committed)

- `cmd/simurgh/main.go`: dropped the false "pre-v0 scaffold; no commands implemented yet" framing
  (16 subcommands are implemented).
- `.gitignore`: covers the bare `simurgh` binary the README tells users to build at repo root, and
  `.claude/worktrees/` (previously only locally excluded, not committed).
- `.github/workflows/ci.yml`: Go version aligned to `1.25` (was `1.23`, mismatched `go.mod`).
- `CHANGELOG.md`: added an `Unreleased` section covering R-WP0 through R-WP6.
- `README.md`: corrected the false "closed source, no public remote, do not add a license file"
  claim to reflect reality (private GitHub remote exists; no LICENSE file yet; licensing is a
  pending owner decision, not a permanent ban). Added the missing `integration xcodebuildmcp` row
  to the CLI reference table.
- `docs/ROADMAP.md`, `docs/plans/MASTER_BUILD_STATUS.md`, `docs/plans/orchestrator-state.md`:
  corrected stale claims that R-WP6 was unmerged / that the TOCTOU was still open.
- Fixed pre-existing cosmetic `gofmt` drift in three files (`internal/broker/errors.go`,
  `internal/integrations/xcodebuildmcp/bindings.go`, `config.go`).

**Still open, owner-decision items (not mine to resolve):**
- No LICENSE file — repo is private; licensing is a deliberate future decision.
- CI has no real macOS/Xcode/Simulator coverage (`ci.yml`'s macOS job is a stub echo) — all
  `darwin_sim`-tagged live-test coverage happens via manual runs only. Flagged, not fixed — adding
  real macOS CI is a bigger, more expensive decision (real Xcode/simulator runners) than tonight's
  scope.

---

## 7. Lancer / Fable wiring (this morning, in direct response to the owner's question)

The owner asked whether Fable would actually know to use Simurgh while building Lancer. It would
not have, as of this session's earlier state — the pieces existed but weren't connected:

- Built and installed the `simurgh` binary to `~/bin` (already on `PATH`) — it was previously only
  buildable from source, not runnable as a bare command.
- Ran `simurgh init` from `~/Documents/command-center` (Lancer): wrote `.simurgh.yaml` (default
  spec `iPhone 17 Pro`), registered `simurgh` under `mcpServers` in Lancer's `.mcp.json` (alongside
  the existing `XcodeBuildMCP`, `xcode`, `apple-docs`, `ios-simulator`, `context7`, `supabase`
  entries).
- Appended the printed agent-rules snippet as a new `## Simulator provisioning (Simurgh)` section
  in Lancer's `AGENTS.md` (which Claude Code already imports via `CLAUDE.md`'s `@AGENTS.md`, and
  which Codex/OpenCode read natively per that file's own header).
- Smoke-tested `simurgh mcp` directly: stays alive while a client connection (stdin) is held open,
  shuts down cleanly on signal — confirmed it's a real, working MCP stdio server, not just a
  registered-but-broken entry.

**What the owner (or Fable, on next session start) needs to do:** nothing further for MCP —
Claude Code loads `.mcp.json` at session start, so the next fresh Lancer session should pick up the
`simurgh` server automatically. **This was NOT done yet this session:** the Lancer `.mcp.json`
change and `AGENTS.md` addition are uncommitted in the `command-center` repo — the owner should
review and commit them there (a separate repo from `simurgh`, not something this session pushed or
committed on the owner's behalf without a look-over).

---

## 8. Current git / process state

| Item | Value |
|---|---|
| `simurgh` repo, `master` | `1f3d4f9`, 80 commits ahead of `origin/master` (`4447e3c`) |
| Pushed to `origin` | **No, nothing** |
| R-WP5 (`pathshim`, `simurgh-shim`) | Implemented, tested, **uncommitted** on `master`'s working tree |
| `simurgh` binary | Built, installed at `~/bin/simurgh`, confirmed on `PATH` |
| Lancer `.mcp.json` / `.simurgh.yaml` / `AGENTS.md` | Updated, **uncommitted** in `command-center` repo |
| Phase A branch (`claude/benchmark-blockers-2026-07-13-37773c`) | Credential-leak object-DB cleanup done and verified; still not pushed; rewrite itself still needs owner review |

---

## 9. Prioritized next steps

1. **Owner review of `a9443f9`'s historical diff** (§5) — decide accept-as-debt vs. rebase, before
   any push of `master`.
2. **Owner review of the R-WP5 shim code** (§4) given the unexplained `~/Documents` access incident
   during its development — inspect before trusting it in a live shell, independent of whether it's
   formally reviewed yet.
3. **Independent review round on R-WP5** (same rigor as R-WP6's four rounds) before committing/
   merging it, if the owner wants it held to the same bar.
4. **Commit or discard the Lancer `.mcp.json`/`AGENTS.md` changes** in `command-center` — done here,
   not committed there.
5. **License decision** (or explicit "not yet") before any public-facing move.
6. Everything else in §6's "still open" list is lower priority, explicitly deferred, not forgotten.
