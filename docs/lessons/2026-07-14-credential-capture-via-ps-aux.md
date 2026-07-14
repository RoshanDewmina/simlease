Never embed raw `ps aux` / full command-line argv into committed evidence or logs.

**What happened:** `bench/preflight.sh` captured full `ps aux` output (including argv) for stray
`cursor-agent` worker processes into committed warm-DD evidence. One of those processes had been
launched with `--api-key crsr_...` on its command line, so the secret was captured verbatim and
committed to git history. Required credential revoke/rotate plus a local (unpushed) history
rewrite; unreachable-object cleanup remains an open caveat.

**Why:** any tool/script that shells out to `ps`, `ps aux`, `/proc/*/cmdline`, or similar to log
"what's running" for diagnostic/evidence purposes is a credential leak vector if any process on
the machine — including ones unrelated to the current session — was launched with a secret on its
command line. This is true even when the capturing script has nothing to do with secrets itself.

**How to apply:** when a script needs to record "what processes were running" for evidence/debug
purposes, capture PID + process name + start time only — never full argv. If argv is genuinely
needed for debugging, redact it through `internal/redact` (already exists in this repo,
`bench/redact_secrets_test.py` covers it) before it touches disk, not after. Treat this as a
standing review-checklist item for any new bench/evidence tooling: "does this touch `ps`/argv
capture? If so, is it redacted before write, not after?"
