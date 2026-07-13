# Simurgh orchestrator state

**Updated:** 2026-07-12 22:00 America/Toronto  
**Owner:** Roshan  
**Orchestrator:** Codex engineering-manager session  
**Repository:** `/Users/roshansilva/Documents/simurgh`  
**Current phase:** M0 — Skeleton

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
| M0.2a IPC protocol/framing | `feat/m0-ipc-protocol` | `.worktrees/m0-ipc-protocol` | `grok-4.5-high` | low | spec'd | pending |
| M0.2b daemon socket/startup | pending | pending | `grok-4.5-high` + Sonnet review | sensitive | blocked on M0.2a + owner-visible approval | pending |
| M0.3 store | merged locally as `8e4a185` | cleaned | `grok-4.5-high` | low | merged | green |
| M0.4 doctor | pending | pending | `grok-4.5-high` | low | blocked on M0.2 | pending |

## Milestone gates

| Milestone | Mechanical gates | macOS/integration gate | Status |
|---|---|---|---|
| M0 | build, vet, race tests, staticcheck | daemon run + doctor green | pending |
| M1 | broker/safety race + concurrency proofs | tagged real-sim lifecycle/reconciliation | not started |
| M2 | full Go gates | isolated concurrent sample builds + warm latency | not started |
| M3 | MCP tests | two-agent dogfood + screenshots/status evidence | not started |
| M4 | full suite + error audit | clean dogfood rerun + fresh-account quickstart | not started |

## Blockers

- No private Git remote has been supplied. This does not block local milestone implementation, but it blocks real PRs and the hosted third-review action.
- M0.2 touches socket creation and permissions, a security-sensitive path. Dispatch requires owner-visible approval after the M0.1 scaffold is integrated.
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

## Next action

Dispatch low-risk M0.2a protocol framing from `docs/plans/work-packages/m0.2a-ipc-protocol.md`. Hold M0.2b socket/startup for Roshan's sensitive-path approval.
