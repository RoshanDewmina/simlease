# Simurgh orchestrator state

**Updated:** 2026-07-12 22:35 America/Toronto  
**Owner:** Roshan  
**Orchestrator:** Claude Code (Sonnet 5) engineering-manager session, resumed from a Codex
engineering-manager session (thread `019f5929-05b3-7b12-a8d3-d4e10f39034f`) that was cut off
mid-redispatch of the M0.2a fix loop. The uncommitted fix in the worktree was verified intact,
independently reviewed, and merged — see evidence log below.  
**Repository:** `/Users/roshansilva/Documents/simurgh`  
**Current phase:** M0 complete — starting M1 (Lease core)

## Operating decisions

- `docs/PRD.md`, `docs/ARCHITECTURE.md`, `docs/ROADMAP.md`, and `docs/COMPETITOR_NOTES.md` are the living source of truth, in that order.
- Phase order M0 → M1 → M2 → M3 → M4 is binding. No post-v0 work may enter v0.
- Git module owner is inferred from the authenticated GitHub account: `github.com/RoshanDewmina/simurgh`.
- The repository is local-only and has no remote. Do not push publicly. Until Roshan supplies a private remote, reviewed branches merge locally and GitHub PR / `claude-code-action` stages are recorded as unavailable rather than fabricated.
- Routine implementation uses Cursor CLI `cursor-agent`; the shell command `agent` is a different, unauthenticated Grok CLI and must not be used for Cursor work.
- Low-risk scaffolding/mechanical work routes to `composer-2.5`; routine implementation routes to `grok-4.5-high`; sensitive fallback/review routes to `claude-sonnet-5-high`.
- Destructive and socket/LaunchAgent paths remain owner-visible `sensitive` work and require full-diff Sonnet-or-orchestrator review.

## Toolchain inventory

| Tool | Exact result | Status |
|---|---|---|
| Cursor Agent | `cursor-agent 2026.07.09-a3815c0` | ready |
| Cursor default implementer | `grok-4.5-high` | ready |
| Cursor mechanical model | `composer-2.5` | ready |
| Cursor sensitive fallback/reviewer | `claude-sonnet-5-high` | ready |
| Conflicting `agent` binary | Grok Build TUI `0.2.93`; unauthenticated; only `grok-4.5` | do not use |
| GitHub CLI | authenticated as `RoshanDewmina`; no repo remote configured | local-only |
| Go | `go1.26.5 darwin/arm64` | ready; exceeds 1.23 minimum |
| Staticcheck | `2026.1 (v0.7.0)` | ready |
| Xcode | `27.0` build `27A5194q` | ready |
| CoreSimulator | iOS 27.0 installed; iPhone 17 Pro available | ready |

## Active work packages

| Package | Branch | Worktree | Model | Risk | Status | Gate |
|---|---|---|---|---|---|---|
| M0.1 repo scaffold | merged locally as `93b6d84` | cleaned | `composer-2.5` | low | merged | green |
| M0.2a IPC protocol/framing | merged locally as `c0098bf` | cleaned | `grok-4.5-high` | low | merged | green |
| M0.2b daemon socket/startup | merged locally as `1983874` | cleaned | `grok-4.5-high` + Sonnet full-diff review | sensitive | merged | green |
| M0.3 store | merged locally as `8e4a185` | cleaned | `grok-4.5-high` | low | merged | green |
| M0.4 doctor | merged locally as `ca76ddc` | cleaned | `grok-4.5-high` | low | merged | green |

## Milestone gates

| Milestone | Mechanical gates | macOS/integration gate | Status |
|---|---|---|---|
| M0 | build, vet, race tests, staticcheck — **PASS** | `daemon run` + `doctor` exit 0, all 5 checks green — **PASS**, live-run 2026-07-12 | **COMPLETE** |
| M1 | broker/safety race + concurrency proofs | tagged real-sim lifecycle/reconciliation | not started |
| M2 | full Go gates | isolated concurrent sample builds + warm latency | not started |
| M3 | MCP tests | two-agent dogfood + screenshots/status evidence | not started |
| M4 | full suite + error audit | clean dogfood rerun + fresh-account quickstart | not started |

## Blockers

- No private Git remote has been supplied. This does not block local milestone implementation, but it blocks real PRs and the hosted third-review action.
- M0.1 cross-model review attempt 1 (`grok-4.5-high`, fresh Cursor ask session) emitted no output for more than four minutes and was terminated with exit 130. The bounded fresh-session retry approved the diff with one non-blocking minor: pin Staticcheck instead of `@latest` in CI.

## Evidence log

### 2026-07-12 — initializer

- Created an empty `master` repository at `/Users/roshansilva/Documents/simurgh`; `git remote -v` returned no remotes.
- Copied the four approved SSOT documents from `/Users/roshansilva/Documents/command-center/simurgh/docs`; `diff -qr` returned no differences.
- Verified Cursor model slugs from live `cursor-agent models`; no model names were guessed.
- Installed Staticcheck using its official Go installation command; version is `2026.1 (v0.7.0)`.
- `xcrun simctl list devices` showed iOS 27.0 and an available iPhone 17 Pro simulator.
- M0.1 coder and orchestrator mechanical gates were green. The first independent review attempt timed out without a verdict; this is not treated as approval or as a code finding.

### 2026-07-12 — M0.1 merged

- Branch commit: `7774c57 chore: scaffold Simurgh Go module`.
- Local merge commit on `master`: `93b6d84 merge: M0.1 repository scaffold`.
- Orchestrator rerun: `go build ./...` exit 0; `go vet ./...` exit 0; `go test -race ./...` exit 0 for `cmd/simurgh` plus all ten internal packages; `staticcheck ./...` exit 0; `git diff --check` exit 0.
- Review verdict: `approve`; one `minor/likely` finding at `.github/workflows/ci.yml:26` because Staticcheck uses `@latest`. Logged for conscious pinning; it did not block under the review policy.
- Worktree removed and feature branch deleted after merge.

### 2026-07-12 — M0.3 merged

- Branch commit: `a034519 feat(store): add atomic state journal`.
- Local merge commit on `master`: `8e4a185 merge: M0.3 atomic state journal`.
- Orchestrator rerun: `go build ./...` exit 0; `go vet ./...` exit 0; `go test -race -count=1 ./...` exit 0 with `internal/store` passing in 1.363s; `staticcheck ./...` exit 0; `git diff --check` exit 0.
- Review verdict: `approve`. Non-blocking findings: contextual wrapping for raw read errors (`minor/certain`); internal-vs-user error mapping (`minor/likely`, adjudicated as boundary-only and added to review standards); tests for rejected save/temp cleanup (`nit`).
- Worktree removed and feature branch deleted after merge.

### 2026-07-12 — session handoff: Codex → Claude Code, M0.2a merged

- The prior Codex orchestrator session (thread `019f5929-05b3-7b12-a8d3-d4e10f39034f`) ran the
  M0.2a fix loop (round 2: distinct `ErrTruncatedFrame` vs `ErrOversizedFrame`; `ErrInvalidVersion`
  vs `ErrUnsupportedVersion`; real `simurgh doctor` fix command) and left it uncommitted in
  `.worktrees/m0-ipc-protocol` — its last dispatch to `cursor-agent` was corrupted by shell-tick
  escaping in the prompt, redispatched with literal-safe wording, and the session ended
  (`task_complete`, `last_agent_message: null`) before that redispatch's result was read back.
- On resume, the orchestrator (Claude Code / Sonnet 5) inspected the uncommitted diff directly
  and independently re-ran the full gate suite in the worktree: `gofmt -l internal/ipc` clean;
  `go build ./...` exit 0; `go vet ./...` exit 0; `go test -race ./...` exit 0 (`internal/ipc`
  1.023s, `internal/store` cached ok); `staticcheck ./...` exit 0; `git diff --check` exit 0.
  All three requested fixes were present and correct.
- Dispatched a fresh independent Claude subagent (no prior context) with the work-package spec,
  `REVIEW_STANDARDS.md`, and the diff, per the verification pipeline's stage-2 cross-model review.
  It independently re-ran all 7 gates (same clean result, full test names pasted) and verified
  each of the three fixes at the exact boundary (8 bytes at `maxSize=8` → `ErrTruncatedFrame`;
  9 bytes over `maxSize=8` → `ErrOversizedFrame`; missing/`0`/`-3`/`-1` version → `ErrInvalidVersion`
  in both request and response paths; `FixUnsupportedVersion` now cites `simurgh doctor`, a real
  planned command per `ARCHITECTURE.md` §9 / `ROADMAP.md` M0.4). No dependents outside
  `internal/ipc` yet (`grep -rn "internal/ipc"` outside the package returns nothing). Verdict:
  **approve-with-minors** — 2 non-blocking notes logged (untested one-shot `DecodeRequest`/
  `DecodeResponse` free functions; `ErrInvalidVersion` will need its own error-triple mapping at
  the future CLI/M0.2b boundary, not folded into `UNSUPPORTED_VERSION`).
- Committed as `9f088a6 fix(ipc): reserve oversized-frame error, split invalid/unsupported version`
  on `feat/m0-ipc-protocol`; local-merged to `master` as `c0098bf merge: M0.2a versioned IPC
  protocol and framing`. Post-merge gate rerun on `master`: build/vet/test-race/staticcheck all
  exit 0. Worktree removed, branch deleted.

### 2026-07-12 — M0.2b merged (sensitive path)

- Owner approved dispatch (AskUserQuestion: "Proceed now") after the sensitive-path interrupt.
  Spec: `docs/plans/work-packages/m0.2b-daemon-socket.md`. Scope narrowed deliberately vs. the
  original brief wording: `daemon.ping` + socket + client auto-start-via-direct-spawn only;
  `launchctl kickstart`, `daemon install`/LaunchAgent plist, `daemon status|stop`, and the PID
  file were explicitly deferred to a later package to keep the sensitive write-set minimal and
  reviewable in one pass.
- Implemented by `grok-4.5-high` in `.worktrees/m0-daemon-socket` (branch `feat/m0-daemon-socket`):
  `internal/ipc/socket.go` (`Listen`, fail-closed stale-socket reclaim — a live socket is never
  unlinked, an unprovable probe failure is treated as live), `internal/ipc/server.go` (accept
  loop, `daemon.ping` handler, typed `METHOD_NOT_FOUND`), `internal/ipc/client.go` (`Dial` —
  exactly one detached-subprocess auto-start + one retry, typed `DAEMON_NOT_RUNNING`),
  `cmd/simurgh/daemon.go` (`simurgh daemon run`, SIGINT/SIGTERM graceful shutdown).
  938 lines (over the ~600-line soft target — logged, not blocking; a single cohesive
  socket+server+client+cmd slice was judged not worth fragmenting further).
- Orchestrator (Sonnet) independently reran all 7 gates before any review: `gofmt -l` clean,
  `go build ./...` exit 0, `go vet ./...` exit 0, `go test -race ./...` exit 0 (`internal/ipc`
  3.7s, `internal/store` cached), `staticcheck ./...` exit 0, `git diff --check` exit 0. Coder's
  own live manual check (pasted) also verified independently on rerun path: socket created at
  mode 0600, real `daemon.ping` round trip over the socket, clean SIGTERM exit.
- Full-diff sensitive-path review performed by the orchestrator directly (Sonnet, no exceptions
  per brief) rather than delegated: verified the 0600 chmod happens after `Listen` (TOCTOU
  window closed), the stale-socket probe fails closed (unexpected probe error ⇒ treated as live,
  never unlinked), the subprocess spawn uses `os.Executable()` (never a bare `"simurgh"` on
  `$PATH`) with exactly one spawn + one bounded retry (no loop), and that no `launchctl`/
  LaunchAgent/PID-file code crept in. One `minor/likely` finding: malformed frames close the
  connection instead of sending a best-effort error response, because a malformed frame often
  has no usable request ID to build a valid envelope — a reasoned, documented deviation from the
  spec's literal wording, not a defect; does not block. Verdict: **approve**.
- Committed as `beca66a feat(daemon): Unix socket listener, daemon.ping server, client auto-start`
  on `feat/m0-daemon-socket`; local-merged to `master` as `1983874 merge: M0.2b daemon socket,
  daemon.ping, client auto-start`. Post-merge gate rerun on `master`: build/vet/test-race/
  staticcheck all exit 0. Worktree removed, branch deleted.
- **M0 mechanical gate now fully green:** `go build ./... && go vet ./... && go test -race ./...`
  exits 0 across all packages with tests (`internal/ipc`, `internal/store`).

### 2026-07-12 — M0.4 merged; M0 milestone gate CLOSED

- Spec: `docs/plans/work-packages/m0.4-doctor.md`. Implemented by `grok-4.5-high` in
  `.worktrees/m0.4-doctor` (branch `feat/m0.4-doctor`): `internal/cli/doctor.go` (5 checks —
  Xcode, simctl, iOS runtime, socket perms, daemon liveness — collect-all not fail-fast, every
  failure with code/message/fix), `cmd/simurgh/doctor.go` (production seams: read-only
  `xcodebuild -version` / `xcrun simctl list runtimes -j`, and a **non-auto-starting**
  `daemon.ping` probe that dials the socket directly rather than calling `internal/ipc.Dial` —
  doctor must never spawn a daemon as a side effect of a diagnostic).
- Orchestrator independently reran all 7 gates before review: build/vet/test-race/staticcheck/
  gofmt/diff-check all exit 0.
- Fresh independent Claude subagent review (low risk, standard pipeline — not the sensitive
  Sonnet-mandatory path): independently re-ran all gates plus its own live manual check (built
  the binary itself, confirmed `ps aux` showed no spawned daemon after a doctor run against an
  empty `SIMURGH_HOME`, confirmed exit 4 → exit 0 transition with daemon started, confirmed
  `--json` output). Verified `dialPing` never touches `ipc.Dial`'s auto-start path and no
  mutating `simctl` subcommand appears anywhere. Verdict: **approve-with-minors** (2 non-blocking:
  `FixSimctlUnavailable` reusing the Xcode-CLT fix string may not resolve a CLT-only environment;
  runtime filter matches on the `name` string prefix rather than the structured `platform` field
  — logged, not blocking).
- Committed as `a30ab3b feat(cli): add simurgh doctor diagnostic command` on `feat/m0.4-doctor`;
  local-merged to `master` as `ca76ddc merge: M0.4 doctor diagnostic command`. Post-merge gate
  rerun: build/vet/test-race/staticcheck all exit 0. Worktree removed, branch deleted.
- **M0 milestone gate run live on this Mac** (the literal `docs/ROADMAP.md:29-32` command):
  built `/tmp/simurgh-m0-final`, `daemon run &` against a fresh `SIMURGH_HOME`, then `doctor` —
  all 5 checks green (`Xcode 27.0`, `simctl available`, `1 iOS runtime(s): iOS 27.0`, socket mode
  0600, `daemon.ping ok`), `doctor_exit=0`, daemon then killed cleanly (`daemon_exit=0`).
  **M0 — Skeleton is COMPLETE.**

## Next action

M0 is done (4/4 packages merged, mechanical + `[mac]` integration gates both green, live-verified
on this Mac). M1 — Lease core is next in phase order: M1.1 broker (acquire/renew/release/get/
list/wait, FIFO queue, capacity formula, error triple), M1.2 provisioner (clone/boot, interface-
faked in unit tests), M1.3 watcher+reaper (liveness/TTL/reclamation), M1.4 safety package
(**sensitive** — the centralized deletion guards; owner-visible approval required before
dispatch, same as M0.2b), M1.5 CLI. M1.4's property tests over adversarial fixtures and the M1
concurrency-proof gate (`docs/ARCHITECTURE.md` §11) are the product's actual differentiator —
budget real attention there, not just gate-passing. Owner digest delivered this turn; awaiting
go/no-go on M1 dispatch order (broker first, in parallel with the non-sensitive provisioner
interface, holding M1.4 safety for explicit approval) vs. any owner-directed reprioritization.
