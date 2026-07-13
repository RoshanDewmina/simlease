# Bench summary — 20260713T180241Z

Raw run files: 15

| Scenario | Arm | n | Success | Median | Min | Max | Incidents |
|---|---|---|---|---|---|---|---|
| B4 | diy | 5 | 4/5 | 60.41s | 36.03s | 112.45s | launch_destination_failure=1, wrong_variant=1 |
| B4 | naive | 5 | 2/5 | 7.91s | 7.21s | 8.08s | launch_destination_failure=1, wrong_variant=3 |
| B4 | simurgh | 5 | 5/5 | 39.85s | 37.20s | 40.34s | none |

## Evidence-integrity findings (wrong-variant badge)

| File | Scenario | Arm | Agent | Rep | Expected | Actual |
|---|---|---|---|---|---|---|
| run-B4-diy-rep5-A.json | B4 | diy | A | 5 | A | none |
| run-B4-naive-rep1-A.json | B4 | naive | A | 1 | A | B |
| run-B4-naive-rep3-A.json | B4 | naive | A | 3 | A | none |
| run-B4-naive-rep5-A.json | B4 | naive | A | 5 | A | B |

