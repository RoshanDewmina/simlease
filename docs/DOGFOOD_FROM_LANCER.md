# Dogfood ledger — Simurgh frictions found while building Lancer

Simurgh is dogfooded BY the Lancer swarm: every parallel Lancer simulator lane runs through
`lease_acquire → simurgh exec → lease_release`. Frictions found in that use are filed here
(and one-line mirrored in Lancer's `docs/CHANGELOG.md` / orchestrator-state ⚡).

Columns: date · Lancer lane · friction · repro · severity · fix SHA / workaround · status

| Date | Lancer lane | Friction | Repro | Severity | Fix / workaround | Status |
|---|---|---|---|---|---|---|
| 2026-07-17 | (session start) | — no frictions yet; ledger opened at Simurgh origin/master `deac3efb` (96-commit publish incl. exec flag-merge/auto-renew, hold/unhold, TTL 60m, xcodebuildmcp adapter) | — | — | — | open |
| 2026-07-17 | WP5 gap-reproof (lease-205) | XcodeBuildMCP session defaults silently clobbered mid-run by a concurrent lease (lease-204) — screenshots/builds hit the wrong sim | Two concurrent leases, both calling the raw global `mcp__XcodeBuildMCP__*` tools directly instead of registering the per-lease adapter's `configPath` as a separate MCP server | HIGH (wrong-device evidence, wasted builds) | **Root-cause corrected 2026-07-17 later same day** — the adapter's per-lease process isolation was NOT missing; it shipped `bec8670`..`b889cfb` (2026-07-14/15), before this entry was even written. The real gap is agents never routing tool calls to the isolated child (docs never said to `claude mcp add-json` the printed configPath). Fixed: `internal/cli/init.go` snippet + `docs/AGENT_GUIDE.md` now spell out the missing registration step. Structural fix (auto-register or Simurgh-side proxy tools) tracked in [issue #4](https://github.com/RoshanDewmina/simurgh/issues/4). | docs fixed; structural fix open |
| 2026-07-17 | WP5 gap-reproof | Claude Code PreToolUse hook resolves `$HOME/.lancer/bin/lancerd` via real `$HOME`, so an isolated (`LANCER_STATE_DIR`) daemon's runs still execute the PRODUCTION binary for hook gating (state stayed isolated via inherited env, binary did not) | Isolated daemon + real agent run; `ps` shows hook argv with production path | MED (version skew between daemon under test and hook binary) | Lancer-side fix filed (hook script should honor LANCER_BIN/state-dir-relative binary); noted here because Simurgh-driven isolated-daemon lanes hit it | open — Lancer issue |
| 2026-07-17 | WP5 gap-reproof | Relay pairing codes expire too fast for multi-step manual sim navigation (pair flow raced expiry twice) | Mint code → navigate sim UI by hand → code expired before Connect | LOW | Workaround: pre-stage pairing sheet before minting; consider longer TTL for dev pairing | open |

## Session 2026-07-17 notes

- Pre-push secret scan found a real (already-revoked) `crsr_` credential embedded in two
  unpushed security-doc commits (`a9443f9`, redacted later by `1f3d4f9`). Remediated by a
  scoped `git filter-repo --replace-text` over `origin/master..master` before publishing —
  tip tree verified bit-identical, backup at `master-backup-pre-redact`. Lesson: scan the
  *range*, not the tip, before any first publish of long local histories.
