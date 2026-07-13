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
| M1.1 lease broker | merged locally as `38fe554` | cleaned | `grok-4.5-high` | low | merged | green |
| M1.2 provisioner | merged locally as `87de11c` | cleaned | `grok-4.5-high` | low (interface-faked; `[mac]` suite is real, live-verified twice) | merged | green |
| M1.4 safety | merged locally as `fd1773a` | cleaned | `grok-4.5-high` + Sonnet personal full-diff review | **sensitive** | merged | green |
| M1.3 watcher + reaper | merged locally as `60dfe1b` | cleaned | `grok-4.5-high` | low (no deletion itself — calls into M1.4 safety only) | merged | green |
| M1.5a daemon lease RPC + reclaim loop | merged locally as `6fc5529` | cleaned | `grok-4.5-high` | low, personal review (unattended deletion capability) | merged | green |
| M1.5b CLI client commands | merged locally as `512a427` | cleaned | `grok-4.5-high` | low | merged | green |

## Milestone gates

| Milestone | Mechanical gates | macOS/integration gate | Status |
|---|---|---|---|
| M0 | build, vet, race tests, staticcheck — **PASS** | `daemon run` + `doctor` exit 0, all 5 checks green — **PASS**, live-run 2026-07-12 | **COMPLETE** |
| M1 | `go test -race -count=1 ./...` (whole repo) — **PASS**, all 11 testable packages green | `[mac]` full-lifecycle + two-terminal concurrent-acquire smoke — **PASS**, live-run 2026-07-13. **Partial**: the GATE's "daemon SIGKILL mid-provision → restart reconciles with zero orphans" sub-scenario is NOT satisfied — it needs broker persistence, which doesn't exist yet (see M1.6 below, added to close this honestly rather than claim an unqualified pass). | **M1.1–M1.5 all merged and live-verified; M1.6 (new, persistence) added before M2 to close the one unmet GATE sub-scenario** |
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

### 2026-07-12 — M1.1 lease broker merged

- Owner approved continuing into M1 (AskUserQuestion at the M0→M1 boundary). Spec:
  `docs/plans/work-packages/m1.1-broker.md`. Scope: in-memory `Lease` state machine
  (`QUEUED→PROVISIONING→ACTIVE→RELEASED`/`CANCELLED`/`FAILED`), per-`DeviceSpec` FIFO queue,
  capacity gate (RAM/CPU formula + override), `Acquire/Renew/Release/Get/List/Wait`. Deliberately
  transport- and persistence-agnostic (no `internal/ipc` or `internal/store` import — a local
  duplicated error-triple helper, noted for future extraction) and provisioner-agnostic (depends
  only on a `Provisioner` interface it defines; a fake drives all unit tests — no `simctl` calls
  anywhere in this package). Deletion is explicitly out of scope: `Release` only transitions state
  and advances the queue; M1.3's reaper owns teardown via M1.4 safety.
- Implemented by `grok-4.5-high` in `.worktrees/m1.1-broker` (branch `feat/m1.1-broker`), 1528
  lines (well over the ~600-line soft target — logged, not blocking; this is the foundational
  concurrency-sensitive package the rest of M1 builds on, judged not worth fragmenting).
- Orchestrator independently reran gates before review: build/vet/staticcheck/gofmt/diff-check
  all exit 0; `go test -race ./internal/broker/...` green, all required GATE scenarios present
  (concurrent same-spec acquire → distinct UDIDs both ACTIVE; capacity-full → QUEUED with
  position; `Wait=true` blocks until `Release` frees a slot; queued `Release` → CANCELLED without
  provisioning; provisioner error → FAILED + typed error).
- Own direct read of the concurrency logic: capacity is reserved synchronously under `b.mu`
  before `Provisioner.Provision` runs with the lock released, so concurrent `Acquire`s can't
  overcommit a spec key's capacity; `tryBeginNextLocked` (queue advance) only ever runs under
  lock.
- Fresh independent Claude subagent review (concurrency-focused: mutex-discipline trace,
  overcommit/double-provision/lost-wakeup analysis, `-race -count=5`): confirmed no overcommit,
  no double-provision, no lost wakeup. Found one **major/likely**: cancelling a `Wait()` before
  the lease reaches a terminal state never removed that waiter's channel from `b.waiters`, so
  repeated short-deadline `Wait`/`Acquire(Wait=true)` polling against a permanently-queued lease
  (a realistic CLI pattern) grew that slice unboundedly. Plus two minor/nit: `Wait`-woken callers
  on a FAILED lease got a nil provisioning-error cause (only the direct `Acquire` caller saw the
  real error); recursive queue-advance on consecutive provisioner failures has stack depth
  proportional to queue length (untested at scale, low real risk given capacity ≤4).
- **One bounded fix loop** (per the pipeline's fix-loop rule — major findings don't auto-block
  forever, but do get one correction pass): dispatched the major + the cheap minor together.
  Fix verified directly by the orchestrator (not re-delegated — narrowly scoped, easy to check by
  reading the diff): `removeWaiterLocked` now swap-removes this call's channel by identity and
  deletes the map entry when empty, race-safe against a concurrent `notifyWaitersLocked` (no
  double-close — if the whole entry is already gone, remove is a no-op); a `failCauses
  map[string]error` now carries the real `Provisioner` error to `Wait`-woken callers. Two new
  tests (`TestWaitCancelRemovesWaiter`, `TestWaitOnProvisionFailurePreservesCause`). Full suite
  reran clean at `-count=3` under `-race`.
- Committed as `de511ff feat(broker): in-memory lease broker (state machine, FIFO queue, capacity
  gate)` on `feat/m1.1-broker`; local-merged to `master` as `38fe554 merge: M1.1 lease broker
  (state machine, FIFO queue, capacity gate)`. Post-merge gate rerun: build/vet/test-race/
  staticcheck all exit 0. Worktree removed, branch deleted.

### 2026-07-13 — M1.2 provisioner merged; owner switched to autonomous /loop mode

- **Owner directive (2026-07-13, supersedes the per-sensitive-package stop-and-ask default from
  the original brief):** "use /loop and keep looping and dont ask me any quesitons, use whatever
  you think is best, test thoroughly." The orchestrator will continue through the full roadmap
  (M1.3 → M1.4 → M1.5 → M1 gate → M2 → M3 → M4) without pausing for owner approval, INCLUDING at
  `sensitive` checkpoints like M1.4 safety. The mandatory Sonnet-or-orchestrator full-diff review
  on every sensitive-path diff still applies in full — "no stop-and-ask" is not "no review."
  Reordered M1.3/M1.4: M1.3 reaper calls into M1.4 safety for actual deletion, so safety is built
  first, not after.
- Spec: `docs/plans/work-packages/m1.2-provisioner.md`. Implemented by `grok-4.5-high` in
  `.worktrees/m1.2-provisioner`: real `simctl`-backed `Provisioner` — resolves `DeviceSpec.Model`
  to a device type and `.Runtime` to an installed iOS runtime (correct numeric version compare,
  confirmed not lexicographic — verified `27.0` ranks above `9.0`; correctly filters out the
  watchOS runtime also installed on this Mac), reuses/creates an idempotent
  `simurgh-base[<model>:<runtime>]` device (deliberately outside the
  `simurgh-(clone|warm)[...]` deletion-invariant pattern — confirmed by regex reasoning, not just
  eyeballing), clones to `simurgh-clone[<lease-id>:<model>:<runtime>]`, boots, waits `Booted`.
  `broker.Provisioner`'s signature grew `ProvisionRequest{Spec, LeaseID}` (mechanical, reviewed).
- Orchestrator independently reran all mechanical gates (green) AND **independently reproduced
  the live `[mac]` integration test myself** — real clone created (`simurgh-clone[test-m1.2:...]`),
  reached `Booted`, its own cleanup left zero leftover devices, confirmed via `xcrun simctl list
  devices` before/after both times.
- Fresh independent Claude subagent review: grepped the whole package for
  delete/erase/shutdown (zero product-code hits), verified the base/clone naming against the
  deletion-invariant regex, **independently ran its own live `[mac]` test** (third live
  reproduction total) with clean before/after `simctl list devices`. Found one **major/plausible**
  real bug: `ensureBaseDevice`'s list-then-create was unsynchronized, so two concurrent
  `Acquire`s for an identical `DeviceSpec` (capacity ≥2 — the roadmap's own flagship concurrency
  scenario) could race into creating two same-named base devices, breaking the idempotent-reuse
  guarantee. Not a deletion-safety violation (the spec's hard blocking bar), but a real
  correctness bug against the core clone-per-lease model.
- **One bounded fix loop:** per-`(model, runtime)` mutex around the list-check-create in
  `ensureBaseDevice` only — never held during `Clone`/`Boot`/`WaitBooted`, and different specs
  stay unserialized against each other. New test
  `TestConcurrentSameSpecProvisionCreatesBaseOnce` (delay-forced race window, mirroring
  `internal/broker`'s own concurrent-acquire test pattern) proved red-without-the-lock,
  green-with-it. Orchestrator independently reread the lock scope to confirm it doesn't
  over-serialize distinct specs, and reran the full suite at `-count=5` under `-race`.
- Committed as `5662ca5 feat(provisioner): real simctl-backed clone/boot provisioner (M1.2)` on
  `feat/m1.2-provisioner`; local-merged to `master` as `87de11c merge: M1.2 real simctl
  provisioner (clone/boot)`. Post-merge gate rerun: build/vet/test-race/staticcheck all exit 0.
  Worktree removed, branch deleted.

### 2026-07-13 — M1.4 safety merged (sensitive, personal full-diff review)

- Spec: `docs/plans/work-packages/m1.4-safety.md`, written with extreme precision given this is
  the single most safety-critical package in the product — a bug here deletes a user's real files
  or real simulators. Implemented by `grok-4.5-high` in `.worktrees/m1.4-safety`:
  `ValidateLeasePath`/`DeleteLeaseDir` (the ONLY way to obtain/act on a deletable filesystem path —
  strict `^lease-[0-9]+$` allowlist before any join, symlink-resolved path-separator-bounded
  prefix check against an injected `leasesRoot`, resolved-target-equal-to-root rejected) and
  `ValidateDeviceDeletion`/`DeleteSimulatorClone` (always re-queries the LIVE device list and
  checks the live name against `IsSimurghManagedName` — `^simurgh-(clone|warm)\[.*\]$` — a
  caller-supplied name is never trusted, and `knownLeaseUDIDs` membership never overrides a
  failing name check).
- Orchestrator independently reran all gates, then **independently reproduced the live `[mac]`
  test myself** (not delegated): real throwaway clone created+deleted; the real pre-existing
  `simurgh-base[iPhone 17 Pro:27.0]` device (same UDID from M1.2's runs) confirmed refused and
  left untouched, verified via `xcrun simctl list devices` before/after.
- **Personal full-diff trace by the orchestrator (mandatory per the brief's "no exceptions" rule
  for this package — not delegated to a subagent, unlike every other package this session):**
  traced every path from a public function to `os.RemoveAll`/`deleter.Delete` and confirmed
  validation is unconditionally on that path with no raw-path overload and no cacheable/skippable
  prior-validation shortcut. Read `paths_test.go`'s symlink-escape and root-equality tests closely
  — they plant real symlinks on disk (not just adversarial string fixtures) and assert the
  outside file is provably untouched afterward, plus a 500-iteration `testing/quick` property
  test (no new dependency — stdlib) asserting no escape and no panic across randomized lease IDs.
  Verdict: **approve**. Two non-blocking notes: a TOCTOU window between validation and the actual
  delete syscall (accepted — same-user local threat model per this product's stated design, no
  privilege boundary crossed); `DeleteSimulatorClone` swallows all `Shutdown` errors
  unconditionally rather than differentiating "already shutdown" from other failures (an
  observability gap, not a safety issue — `Delete` still fails and propagates if the device
  genuinely can't be removed).
- Committed as `57fc48c feat(safety): centralized deletion guards (M1.4)` on `feat/m1.4-safety`;
  local-merged to `master` as `fd1773a merge: M1.4 centralized deletion safety guards`.
  Post-merge gate rerun: build/vet/test-race/staticcheck all exit 0. Worktree removed, branch
  deleted.

### 2026-07-13 — M1.3 watcher+reaper merged; known gap: cross-process crash recovery deferred

- Spec: `docs/plans/work-packages/m1.3-watcher-reaper.md`, written with an explicit, deliberate
  scope boundary: the roadmap's `[mac]` GATE scenario "daemon SIGKILL mid-provision → restart
  reconciles with zero orphans" needs broker-lease persistence via `internal/store`, which does
  not exist yet (M1.1 scoped that out as a later package). This package delivers everything
  achievable without it — in-process reclaim (PID death/TTL/heartbeat/hard-cap) and same-process
  orphan scan — and stubs `Reaper.Reconcile` as an honest no-op with a `TODO(m1.x-store)` comment
  rather than faking cross-restart recovery. **This gap is still open** — whoever wires
  `internal/store` into the broker next should also revisit `Reconcile`.
- Implemented by `grok-4.5-high` in `.worktrees/m1.3-watcher-reaper`: `broker.Reclaim`
  (ACTIVE→RECLAIMED, queue advance factored into a shared `endActiveOrQueued` helper with
  `Release`, `isWaitTerminal` updated so `Wait()` callers see reclaimed leases) — the one
  permitted `internal/broker` edit; `watcher.Tick` (pure decision function, zero side effects,
  priority order PID-exit(`--rm`) → PID-death → hard-cap → inactivity → TTL, real
  `syscall.Kill(pid,0)` seam) + `watcher.Run` (thin ticker wrapper); `reaper.Reclaim` (acts on an
  already-RECLAIMED lease, calls `safety.DeleteSimulatorClone`/`safety.DeleteLeaseDir` only,
  idempotent on both sides) + `reaper.ScanOrphans` (refuses to run inside the 10s post-start
  reconciliation window per §10, returns candidates without deleting them — mirrors
  `safety.ValidateDeviceDeletion`'s validate-vs-act split, so a scan false positive still can't
  cause an unsafe delete since any real reclaim of a returned UDID re-validates live through
  safety).
- Orchestrator independently reran all gates, then **independently reproduced the live `[mac]`
  test myself**: real throwaway clone reclaimed (device + lease dir both actually deleted); a
  separately-created real orphan clone correctly found by `ScanOrphans` after the window elapsed;
  the pre-existing `simurgh-base[...]` device confirmed untouched throughout, same UDID
  before/after.
- **Personal trace of the decision logic** (this package is risk-adjacent even though it's
  classified `low` — a bug here means "my simulator got deleted while I was using it," so it got
  the same scrutiny as a sensitive package): confirmed `reaper.go` calls only the two
  already-reviewed `internal/safety` destructive functions, no raw `simctl`/`os.RemoveAll`
  anywhere; confirmed the reconciliation window correctly gates only `ScanOrphans` (an already-
  decided `Reclaim` on an ACTIVE lease is NOT gated by it, which is the correct reading of §10 —
  the window protects against premature orphan reclamation on a fresh/restarting daemon, not
  against reclaiming a lease within the same running process); confirmed passing a device's own
  UDID as "known" into `DeleteSimulatorClone` is legitimate audit context, not a bypass (`safety`
  still independently re-verifies the live name regardless). No blocking findings; approved
  without a separate subagent pass given the depth of this direct trace.
- Committed as `d93e1b7 feat(watcher,reaper): reclamation loop — decide then act (M1.3)` on
  `feat/m1.3-watcher-reaper`; local-merged to `master` as `60dfe1b merge: M1.3 watcher + reaper
  (reclamation loop)`. Post-merge gate rerun: build/vet/test-race/staticcheck all exit 0.
  Worktree removed, branch deleted.

### 2026-07-13 — M1.5a merged: the daemon has a real, live-proven reclaim loop

- Spec: `docs/plans/work-packages/m1.5a-daemon-lease-rpc.md`. This is the first package where a
  real client can trigger real device creation over the actual socket protocol, and the first
  place unattended reclaim/deletion runs while the daemon is up — so it got the same "personally
  spot-check before merge" treatment as a sensitive package, without formally being classified
  `sensitive` (no new destructive primitive was added; every delete still routes through the
  already-reviewed `internal/safety`).
- Implemented by `grok-4.5-high` in `.worktrees/m1.5a-daemon-rpc`: registered
  `lease.acquire|renew|release|get|list|wait` on the socket server (additive
  `ipc.Server.RegisterHandler`), explicit wire DTOs decoupling the JSON contract from broker's Go
  shape, a complete `brokerErrorToIPC` mapping (all 4 typed broker errors + INTERNAL fallback,
  including `CAPACITY_QUEUED`'s `queuePosition`), additive JSON tags + duration-string marshaling
  on `broker/types.go` (existing broker tests unaffected), and `daemon.ReclaimLoop` (5s watch
  tick → `Broker.Reclaim` → `Reaper.Reclaim`; separate 5m orphan scan, **log-only, no
  auto-delete** — the safer judgment call, deferred to a future `prune --apply` per roadmap
  M2.3). `cmd/simurgh/daemon.go` gained a `simctlSafetyClient` adapter (real `xcrun simctl`
  shutdown/delete) wired only into `reaper.Reaper`'s `Deleter` — reachable only through the
  already-validated `safety.DeleteSimulatorClone` path, no bypass.
- Orchestrator independently reran all gates (green), then **personally traced `ReclaimLoop`**:
  confirmed `applyDecisions` always re-validates via `Broker.Reclaim` (which itself re-checks
  ACTIVE state) before calling `Reaper.Reclaim` with the exact lease object `Broker.Reclaim`
  returned — no stale-lease or cross-lease risk — and confirmed the orphan scanner never deletes,
  only logs.
- **Personally exercised the full reclaim→deletion path live end-to-end** (the subagent's own
  live check explicitly did NOT cover this — it only proved acquire/release, not reclaim): built
  a throwaway Go client inside the worktree (deleted before commit, never shipped) that dialed
  the real socket, sent a real `lease.acquire` with `ttl:"6s"`, waited past one 5s watch tick, and
  confirmed via `lease.get` over the wire that the lease auto-transitioned to `"state":"reclaimed"`
  — then independently confirmed via `xcrun simctl list devices` that the real clone was actually
  gone, with zero manual intervention. Only the pre-existing `simurgh-base[...]` remained
  throughout, same UDID as always.
- Committed as `8e6e1a1 feat(daemon): wire lease RPCs + reclaim loop into the running daemon
  (M1.5a)` on `feat/m1.5a-daemon-rpc`; local-merged to `master` as `6fc5529 merge: M1.5a daemon
  lease RPC wiring + reclaim loop`. Post-merge gate rerun: build/vet/test-race/staticcheck all
  exit 0. Worktree removed, branch deleted.

### 2026-07-13 — M1.5b merged; M1 milestone gate closed (with one honest caveat); M1.6 added

- Spec: `docs/plans/work-packages/m1.5b-cli-commands.md`. Implemented by `grok-4.5-high` in
  `.worktrees/m1.5b-cli`: `simurgh acquire|release|renew|status|list`, matching `cmd/simurgh`'s
  existing hand-rolled flag style (no new CLI-framework dependency added). Added an additive
  `pool.status` RPC (capacity + lease counts by state) since `simurgh status` needed it and it
  wasn't in M1.5a's six `lease.*` methods. `exitCodeForIPCError` implements the full ARCHITECTURE
  §9 exit-code contract. Honestly documented gap: `--reuse-if-exists` is wired end-to-end but
  inert server-side (the M1.1 gap it inherits — `broker.Acquire` never reads the field).
- Orchestrator independently reran all gates (green after discarding one stray trailing-whitespace
  edit the coder made to this state file mid-session — `git diff --check` caught it, `git checkout`
  reverted it, since this file gets its authoritative update from the orchestrator anyway).
- **Personally re-ran the live check in a single clean shell** rather than trusting the coder's
  report at face value: real acquire, `status`/`list` JSON, a real `LEASE_NOT_FOUND` exit-3 error
  path, `renew`, and `release` via the no-arg last-lease-pointer fallback all round-tripped
  correctly over the real socket against a real daemon. Confirmed `release` leaves the device
  intact (matches M1.5a). No blocking findings — merged without a separate subagent pass.
- Committed as `1f470ef feat(cli): acquire/release/renew/status/list commands (M1.5b)` on
  `feat/m1.5b-cli`; local-merged to `master` as `512a427 merge: M1.5b CLI acquire/release/renew/
  status/list commands`. Post-merge gate rerun: build/vet/test-race/staticcheck all exit 0.
  Worktree removed, branch deleted.

### 2026-07-13 — M1 milestone gate: mechanical PASS, `[mac]`/manual-smoke PASS, one honest gap

- **Mechanical:** `go build ./... && go vet ./... && go test -race -count=1 ./...` — all 11
  testable packages green. `staticcheck ./...` clean.
- **`[mac]` two-terminal manual smoke (the roadmap's own flagship scenario), run for real by the
  orchestrator:** built the binary, exported a fresh `SIMURGH_HOME`, backgrounded TWO concurrent
  real `simurgh acquire --model "iPhone 17 Pro"` processes (this Mac's computed capacity is 2:
  `min(floor(16GB/6), 10cores/2, 4) = min(2,5,4) = 2`, so both could grant concurrently rather than
  one queuing) — both returned `exit 0` with `state:active`, distinct lease IDs (`lease-1`,
  `lease-2`) and **distinct UDIDs**, confirmed via `xcrun simctl list devices` showing two
  separate `Booted` `simurgh-clone[...]` devices. Zero collisions. This is the exact "two terminal
  `simurgh acquire`... two booted clones... distinct UDIDs" smoke test `docs/ROADMAP.md` calls for
  under M1's `[mac]` GATE, and it's the first time it was actually runnable (the CLI didn't exist
  before M1.5b).
- **Testing-hygiene incident (own mistake, not a product bug, logged so it doesn't repeat):**
  mid-verification, a `SIMURGH_HOME` shell-variable lookup across separate Bash tool calls (which
  don't share shell state) evaluated to an empty string; an empty string is falsy in
  `simurghHomeDir()`'s `if h := os.Getenv("SIMURGH_HOME"); h != ""` check, so a `release` call fell
  through to the REAL default `~/.simurgh` and `ipc.Dial` auto-started a real daemon bound there.
  Caught immediately by checking `~/.simurgh` right after — it contained only an empty
  `leases/`+`logs/` scaffold and an empty log file (no state.json since persistence doesn't exist
  yet, no real lease directories, no simctl side effects, since `release` alone never provisions or
  deletes anything). The stray daemon process was already dead (an unrelated `pkill` in the same
  script coincidentally matched its command line). Removed the accidental `~/.simurgh` scaffold
  (my own artifact from seconds earlier, not pre-existing user data) and reconfirmed real device
  state was untouched throughout. **Lesson for future live-`[mac]` sessions: always `export
  SIMURGH_HOME` and verify it's non-empty within the SAME shell invocation as the command that uses
  it — Bash tool calls do not share environment state.**
- **The one gap, stated plainly:** the `[mac]` GATE's "daemon SIGKILL mid-provision → restart
  reconciles with zero orphans" sub-scenario is NOT satisfied. It requires broker persistence via
  `internal/store`, which was deliberately deferred starting at M1.1 and never revisited since
  M1.2–M1.5 didn't strictly need it. Rather than claim an unqualified milestone pass, this is
  logged honestly and **M1.6 (broker persistence + reconciliation) has been added to
  `docs/ROADMAP.md`'s M1 package list** (a real SSOT update, not just a note here) as the next
  package, before M2, so isolation/warm-template state doesn't compound on top of an unpersisted
  broker.

## Next action

**M1.6 — broker persistence + reconciliation** is next (newly added, see `docs/ROADMAP.md`'s M1
section and the gate-closure entry above): wire `internal/store`'s journal into `internal/broker`
(persist on every state-mutating operation — additive `store.State` schema extension, following
the pattern `internal/store/store.go`'s own doc comment anticipated back in M0.3), and implement
real `internal/reaper.Reconcile` (load the journal on daemon start, cross-reference against a live
`simctl` device scan via the reconciliation-window-respecting pattern `ScanOrphans` already
established, rehydrate known ACTIVE leases into a fresh in-memory `Broker`, treat anything
`simurgh-clone[...]`-named with no journal entry as an orphan — log-only, per the M1.5a policy,
still not auto-reclaimed). This closes the one M1 GATE sub-scenario left open. After M1.6: rerun
the SIGKILL-mid-provision → restart → reconcile scenario live for real (build, acquire, `kill -9`
the daemon, restart it against the same `SIMURGH_HOME`, confirm the journal-recovered state and
zero orphan/non-simurgh deletions) as the actual acceptance evidence for this specific gate item.
Then M2 (isolation + warm templates) in phase order, followed by M3 (MCP + the two-parallel-
Claude-Code-sessions dogfood — the v0 exit demo) and M4 (hardening), tagging `v0.0.1` per
`~/Documents/simurgh/HANDOFF_PROMPT.md`'s done-bar.
