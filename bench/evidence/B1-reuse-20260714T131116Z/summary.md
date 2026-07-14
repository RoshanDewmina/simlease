# Bench summary — B1-reuse-20260714T131116Z

Raw run files: 20

| Scenario | Arm | n | Success | Median | Min | Max | Incidents |
|---|---|---|---|---|---|---|---|
| B1 | simurgh | 20 | 18/20 | 12.58s | 10.79s | 65.13s | launch_destination_failure=2, wrong_variant=2 |

## Time-to-N-green (retry-to-green, max attempts per agent-task shown)

Wall-clock per rep until ALL N agents hold a verified-correct result (retry latency included). `first-try greens` counts agents green on attempt 1.

| Scenario | Arm | Mode | Reps | All-green reps | Median to-green | Min | Max | First-try greens | Attempts used / budget |
|---|---|---|---|---|---|---|---|---|---|
| B1 | simurgh | reuse | 10 | 10/10 | 16.96s | 14.15s | 192.04s | 18/20 | 23/60 |

## Badge latency (launch → first AGENT_BADGE print, first attempts)

| Scenario | Arm | n with badge | Median | Min | Max |
|---|---|---|---|---|---|
| B1 | simurgh | 18 | 1.25s | 1.04s | 4.72s |

## Post-rep residue log

| Arm | Rep | simurgh-clone residue count |
|---|---|---|
| simurgh | 1 | 2 |
| simurgh | 2 | 2 |
| simurgh | 3 | 2 |
| simurgh | 4 | 2 |
| simurgh | 5 | 2 |
| simurgh | 6 | 2 |
| simurgh | 7 | 2 |
| simurgh | 8 | 2 |
| simurgh | 9 | 2 |
| simurgh | 10 | 2 |

## Evidence-integrity findings (wrong-variant badge)

| File | Scenario | Arm | Agent | Rep | Expected | Actual |
|---|---|---|---|---|---|---|
| run-B1-simurgh-rep2-A.json | B1 | simurgh | A | 2 | A | none |
| run-B1-simurgh-rep2-B.json | B1 | simurgh | B | 2 | B | none |

