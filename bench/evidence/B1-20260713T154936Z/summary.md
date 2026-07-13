# Bench summary — 20260713T154936Z

Raw run files: 60

| Scenario | Arm | n | Success | Median | Min | Max | Incidents |
|---|---|---|---|---|---|---|---|
| B1 | diy | 20 | 20/20 | 38.64s | 26.37s | 60.70s | none |
| B1 | naive | 20 | 10/20 | 8.06s | 7.35s | 9.32s | launch_destination_failure=10, wrong_variant=10 |
| B1 | simurgh | 20 | 17/20 | 81.43s | 66.79s | 100.12s | launch_destination_failure=3, wrong_variant=3 |

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
| run-B1-naive-rep1-B.json | B1 | naive | B | 1 | B | A |
| run-B1-naive-rep10-B.json | B1 | naive | B | 10 | B | A |
| run-B1-naive-rep2-A.json | B1 | naive | A | 2 | A | B |
| run-B1-naive-rep3-A.json | B1 | naive | A | 3 | A | B |
| run-B1-naive-rep4-B.json | B1 | naive | B | 4 | B | A |
| run-B1-naive-rep5-A.json | B1 | naive | A | 5 | A | B |
| run-B1-naive-rep6-A.json | B1 | naive | A | 6 | A | B |
| run-B1-naive-rep7-A.json | B1 | naive | A | 7 | A | B |
| run-B1-naive-rep8-B.json | B1 | naive | B | 8 | B | A |
| run-B1-naive-rep9-B.json | B1 | naive | B | 9 | B | A |
| run-B1-simurgh-rep6-A.json | B1 | simurgh | A | 6 | A | none |
| run-B1-simurgh-rep6-B.json | B1 | simurgh | B | 6 | B | none |
| run-B1-simurgh-rep8-B.json | B1 | simurgh | B | 8 | B | none |

