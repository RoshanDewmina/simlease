# Provenance — 2026-07-13 benchmark run

| | |
|---|---|
| Hardware | Mac16,13 (MacBook Air), 10 CPUs, 16 GB RAM |
| macOS | 27.0 (build 26A5378j) |
| Xcode | 27.0 (Build 27A5194q) |
| iOS runtime | iOS 27.0 |
| Simurgh commit (harness + product under test) | `4447e3c` |
| Fixture (B1–B5) | `test/fixtures/SampleApp` (compile-flag agent badge A–F) |
| B6 real app | Lancer (`~/Documents/command-center`), `master` @ `0e0b9eba`, detached worktrees |
| Run window | 2026-07-13 ~10:30–21:20 America/Toronto |

**RAM-floor decision (explicit, owner-made):** the plan's preflight gate
(free+inactive ≥ 8 GB) never passed on this 16 GB machine and was disabled via
`BENCH_RAM_FLOOR_GB=0` for the whole session. Real memory-pressure episodes
during the run are reported as findings in RESULTS.md, not hidden.

**Reproduction note:** re-running the harness regenerates comparable, not
byte-identical, data — absolute latencies depend on machine load. Summary
tables ARE byte-reproducible from the committed JSONs via
`python3 bench/report.py <cell-dir>`.

Cells re-run after 2026-07-13 (P0 fix session) carry their own provenance
blocks in RESULTS.md v2, including the preflight JSON recorded per run.
