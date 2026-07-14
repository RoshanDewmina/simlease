# Bench summary — B1-fresh-20260714T111219Z

Raw run files: 60

| Scenario | Arm | n | Success | Median | Min | Max | Incidents |
|---|---|---|---|---|---|---|---|
| B1 | diy | 20 | 20/20 | 54.15s | 25.01s | 98.02s | none |
| B1 | naive | 20 | 9/20 | 10.30s | 7.87s | 57.80s | dd_contention=1, launch_destination_failure=16, wrong_variant=11 |
| B1 | simurgh | 20 | 14/20 | 140.61s | 66.03s | 797.92s | launch_destination_failure=6, wrong_variant=6 |

## Time-to-N-green (retry-to-green, max attempts per agent-task shown)

Wall-clock per rep until ALL N agents hold a verified-correct result (retry latency included). `first-try greens` counts agents green on attempt 1.

| Scenario | Arm | Mode | Reps | All-green reps | Median to-green | Min | Max | First-try greens | Attempts used / budget |
|---|---|---|---|---|---|---|---|---|---|
| B1 | diy | n/a | 10 | 10/10 | 142.96s | 138.82s | 184.52s | 20/20 | 20/60 |
| B1 | naive | n/a | 10 | 4/10 | 36.73s | 23.97s | 92.44s | 9/20 | 37/60 |
| B1 | simurgh | fresh | 10 | 10/10 | 265.26s | 117.26s | 1540.74s | 14/20 | 27/60 |

## Badge latency (launch → first AGENT_BADGE print, first attempts)

| Scenario | Arm | n with badge | Median | Min | Max |
|---|---|---|---|---|---|
| B1 | diy | 20 | 3.13s | 1.50s | 4.78s |
| B1 | naive | 16 | 1.48s | 0.83s | 2.14s |
| B1 | simurgh | 14 | 2.47s | 1.56s | 4.62s |

## Post-rep residue log

| Arm | Rep | simurgh-clone residue count |
|---|---|---|
| naive | 1 | 0 |
| naive | 2 | 0 |
| naive | 3 | 0 |
| naive | 4 | 0 |
| naive | 5 | 0 |
| naive | 6 | 0 |
| naive | 7 | 0 |
| naive | 8 | 0 |
| naive | 9 | 0 |
| naive | 10 | 0 |
| diy | 1 | 0 |
| diy | 2 | 0 |
| diy | 3 | 0 |
| diy | 4 | 0 |
| diy | 5 | 0 |
| diy | 6 | 0 |
| diy | 7 | 0 |
| diy | 8 | 0 |
| diy | 9 | 0 |
| diy | 10 | 0 |
| simurgh | 1 | 0 |
| simurgh | 2 | 0 |
| simurgh | 3 | 0 |
| simurgh | 4 | 0 |
| simurgh | 5 | 0 |
| simurgh | 6 | 0 |
| simurgh | 7 | 0 |
| simurgh | 8 | 0 |
| simurgh | 9 | 0 |
| simurgh | 10 | 0 |

## Evidence-integrity findings (wrong-variant badge)

| File | Scenario | Arm | Agent | Rep | Expected | Actual |
|---|---|---|---|---|---|---|
| run-B1-naive-rep1-A.json | B1 | naive | A | 1 | A | none |
| run-B1-naive-rep1-B.json | B1 | naive | B | 1 | B | A |
| run-B1-naive-rep10-B.json | B1 | naive | B | 10 | B | none |
| run-B1-naive-rep2-A.json | B1 | naive | A | 2 | A | B |
| run-B1-naive-rep3-B.json | B1 | naive | B | 3 | B | none |
| run-B1-naive-rep4-B.json | B1 | naive | B | 4 | B | A |
| run-B1-naive-rep5-B.json | B1 | naive | B | 5 | B | A |
| run-B1-naive-rep6-B.json | B1 | naive | B | 6 | B | A |
| run-B1-naive-rep7-B.json | B1 | naive | B | 7 | B | A |
| run-B1-naive-rep8-B.json | B1 | naive | B | 8 | B | none |
| run-B1-naive-rep9-B.json | B1 | naive | B | 9 | B | A |
| run-B1-simurgh-rep2-A.json | B1 | simurgh | A | 2 | A | none |
| run-B1-simurgh-rep5-A.json | B1 | simurgh | A | 5 | A | none |
| run-B1-simurgh-rep7-A.json | B1 | simurgh | A | 7 | A | none |
| run-B1-simurgh-rep8-A.json | B1 | simurgh | A | 8 | A | none |
| run-B1-simurgh-rep8-B.json | B1 | simurgh | B | 8 | B | none |
| run-B1-simurgh-rep9-B.json | B1 | simurgh | B | 9 | B | none |

