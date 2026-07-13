# Bench summary — 20260713T185910Z

Raw run files: 36

| Scenario | Arm | n | Success | Median | Min | Max | Incidents |
|---|---|---|---|---|---|---|---|
| B5 | naive | 18 | 1/18 | 6.82s | 2.20s | 19.29s | install_clobber=1, launch_destination_failure=38, unclassified_failure=10, wrong_variant=17 |
| B5 | simurgh | 18 | 10/18 | 270.19s | 84.08s | 843.30s | launch_destination_failure=8, wrong_variant=8 |

## Post-rep residue log

| Arm | Rep | simurgh-clone residue count |
|---|---|---|
| naive | 1 | 0 |
| naive | 2 | 0 |
| naive | 3 | 0 |
| simurgh | 1 | 0 |
| simurgh | 2 | 0 |
| simurgh | 3 | 0 |

## Evidence-integrity findings (wrong-variant badge)

| File | Scenario | Arm | Agent | Rep | Expected | Actual |
|---|---|---|---|---|---|---|
| run-B5-naive-rep1-A.json | B5 | naive | A | 1 | A | none |
| run-B5-naive-rep1-B.json | B5 | naive | B | 1 | B | none |
| run-B5-naive-rep1-C.json | B5 | naive | C | 1 | C | B |
| run-B5-naive-rep1-D.json | B5 | naive | D | 1 | D | none |
| run-B5-naive-rep1-E.json | B5 | naive | E | 1 | E | B |
| run-B5-naive-rep1-F.json | B5 | naive | F | 1 | F | none |
| run-B5-naive-rep2-A.json | B5 | naive | A | 2 | A | none |
| run-B5-naive-rep2-B.json | B5 | naive | B | 2 | B | none |
| run-B5-naive-rep2-C.json | B5 | naive | C | 2 | C | none |
| run-B5-naive-rep2-D.json | B5 | naive | D | 2 | D | none |
| run-B5-naive-rep2-E.json | B5 | naive | E | 2 | E | B |
| run-B5-naive-rep2-F.json | B5 | naive | F | 2 | F | none |
| run-B5-naive-rep3-A.json | B5 | naive | A | 3 | A | none |
| run-B5-naive-rep3-B.json | B5 | naive | B | 3 | B | none |
| run-B5-naive-rep3-C.json | B5 | naive | C | 3 | C | F |
| run-B5-naive-rep3-D.json | B5 | naive | D | 3 | D | none |
| run-B5-naive-rep3-E.json | B5 | naive | E | 3 | E | none |
| run-B5-simurgh-rep1-A.json | B5 | simurgh | A | 1 | A | none |
| run-B5-simurgh-rep1-B.json | B5 | simurgh | B | 1 | B | none |
| run-B5-simurgh-rep1-C.json | B5 | simurgh | C | 1 | C | none |
| run-B5-simurgh-rep2-A.json | B5 | simurgh | A | 2 | A | none |
| run-B5-simurgh-rep2-B.json | B5 | simurgh | B | 2 | B | none |
| run-B5-simurgh-rep2-F.json | B5 | simurgh | F | 2 | F | none |
| run-B5-simurgh-rep3-B.json | B5 | simurgh | B | 3 | B | none |
| run-B5-simurgh-rep3-E.json | B5 | simurgh | E | 3 | E | none |

