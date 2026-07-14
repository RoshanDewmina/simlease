# Bench evidence — committed receipts for RESULTS.md

Every number published in `bench/RESULTS.md` (2026-07-13 run) traces to a JSON
file in this directory. These are verbatim copies of the gitignored raw
outputs under `bench/results/<ts>/`, with exactly one transformation applied:
absolute home paths are scrubbed (`/Users/<name>` → `/Users/OWNER`) inside the
text files. PNGs are untouched (they contain only the SampleApp fixture's
full-screen agent badge).

| Cell in RESULTS.md | Directory here |
|---|---|
| B1 (2 agents, N/D/S, 10 reps — clean re-run) | `B1-20260713T154936Z/` |
| B1 fresh rerun (N/D/S, retries=3, 2026-07-14) | `B1-fresh-20260714T111219Z/` |
| B1 reuse mode (ARM-S only, retries=3, 2026-07-14) | `B1-reuse-20260714T131116Z/` |
| B2 (4 agents, N/D/S, 5 reps) | `B2-20260713T171721Z/` |
| B3 (solo overhead, N/S, 10 reps) | `B3-20260713T175107Z/` |
| B4 (crash cleanup, N/D/S, 5 reps) | `B4-20260713T180241Z/` (see `b4-residue-log.jsonl`) |
| B5 (6 agents / capacity 3, N/S, 3 reps) | `B5-20260713T185910Z/` (see `b5-ram-log.jsonl`) |
| B6 naive (3 reps) | `B6-naive-20260713T200959Z/` |
| B6 diy (3 reps, one dir per foreground rep) | `B6-diy-20260713T211221Z/` `-212218Z/` `-213216Z/` |
| B6 simurgh (2 of 3 planned reps) | `B6-simurgh-20260713T214256Z/` `-215145Z/` |

Wrong-badge PNGs (the "agents literally screenshot each other's app" claim):

- `B1-20260713T154936Z/run-B1-naive-rep2-A.png` — agent A's screenshot showing
  agent **B**'s badge (`run-B1-naive-rep2-A.json`: expected `agent-badge-A`,
  observed `B`).
- `B1-20260713T154936Z/run-B1-naive-rep1-B.png` — agent B's screenshot showing
  agent **A**'s badge.

To verify a summary table: `python3 bench/report.py bench/evidence/<cell-dir>`
regenerates the `summary.md` committed alongside the raw JSONs (report.py is a
pure function of the JSON files).

Provenance for all cells: see [provenance.md](provenance.md).
