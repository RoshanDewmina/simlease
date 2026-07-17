# Dogfood ledger — Simurgh frictions found while building Lancer

Simurgh is dogfooded BY the Lancer swarm: every parallel Lancer simulator lane runs through
`lease_acquire → simurgh exec → lease_release`. Frictions found in that use are filed here
(and one-line mirrored in Lancer's `docs/CHANGELOG.md` / orchestrator-state ⚡).

Columns: date · Lancer lane · friction · repro · severity · fix SHA / workaround · status

| Date | Lancer lane | Friction | Repro | Severity | Fix / workaround | Status |
|---|---|---|---|---|---|---|
| 2026-07-17 | (session start) | — no frictions yet; ledger opened at Simurgh origin/master `deac3efb` (96-commit publish incl. exec flag-merge/auto-renew, hold/unhold, TTL 60m, xcodebuildmcp adapter) | — | — | — | open |

## Session 2026-07-17 notes

- Pre-push secret scan found a real (already-revoked) `crsr_` credential embedded in two
  unpushed security-doc commits (`a9443f9`, redacted later by `1f3d4f9`). Remediated by a
  scoped `git filter-repo --replace-text` over `origin/master..master` before publishing —
  tip tree verified bit-identical, backup at `master-backup-pre-redact`. Lesson: scan the
  *range*, not the tip, before any first publish of long local histories.
