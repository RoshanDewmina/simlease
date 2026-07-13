# Bench summary — 20260713T171721Z

Raw run files: 60

| Scenario | Arm | n | Success | Median | Min | Max | Incidents |
|---|---|---|---|---|---|---|---|
| B2 | diy | 20 | 20/20 | 94.34s | 65.77s | 114.37s | none |
| B2 | naive | 20 | 3/20 | 8.54s | 1.68s | 11.55s | install_clobber=5, launch_destination_failure=27, unclassified_failure=4, wrong_variant=17 |
| B2 | simurgh | 20 | 20/20 | 88.19s | 63.55s | 94.20s | none |

## Post-rep residue log

| Arm | Rep | simurgh-clone residue count |
|---|---|---|
| naive | 1 | 0 |
| naive | 2 | 0 |
| naive | 3 | 0 |
| naive | 4 | 0 |
| naive | 5 | 0 |
| diy | 1 | 0 |
| diy | 2 | 0 |
| diy | 3 | 0 |
| diy | 4 | 0 |
| diy | 5 | 0 |
| simurgh | 1 | 0 |
| simurgh | 2 | 0 |
| simurgh | 3 | 0 |
| simurgh | 4 | 0 |
| simurgh | 5 | 0 |

## Evidence-integrity findings (wrong-variant badge)

| File | Scenario | Arm | Agent | Rep | Expected | Actual |
|---|---|---|---|---|---|---|
| run-B2-naive-rep1-A.json | B2 | naive | A | 1 | A | B |
| run-B2-naive-rep1-B.json | B2 | naive | B | 1 | B | none |
| run-B2-naive-rep1-C.json | B2 | naive | C | 1 | C | B |
| run-B2-naive-rep1-D.json | B2 | naive | D | 1 | D | A |
| run-B2-naive-rep2-A.json | B2 | naive | A | 2 | A | B |
| run-B2-naive-rep2-B.json | B2 | naive | B | 2 | B | D |
| run-B2-naive-rep2-D.json | B2 | naive | D | 2 | D | C |
| run-B2-naive-rep3-A.json | B2 | naive | A | 3 | A | none |
| run-B2-naive-rep3-B.json | B2 | naive | B | 3 | B | none |
| run-B2-naive-rep3-C.json | B2 | naive | C | 3 | C | A |
| run-B2-naive-rep4-A.json | B2 | naive | A | 4 | A | none |
| run-B2-naive-rep4-B.json | B2 | naive | B | 4 | B | none |
| run-B2-naive-rep4-D.json | B2 | naive | D | 4 | D | none |
| run-B2-naive-rep5-A.json | B2 | naive | A | 5 | A | none |
| run-B2-naive-rep5-B.json | B2 | naive | B | 5 | B | none |
| run-B2-naive-rep5-C.json | B2 | naive | C | 5 | C | A |
| run-B2-naive-rep5-D.json | B2 | naive | D | 5 | D | A |

