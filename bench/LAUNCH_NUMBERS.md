# Launch numbers — the only claims currently safe to publish

Each line traces to a committed evidence cell (`bench/evidence/`). Nothing
else from the benchmark is publishable yet; the blocked list is at the bottom
of RESULTS.md v2.

1. **4 parallel agents on one 16 GB MacBook Air: 20/20 tasks green, zero
   collisions, zero residue — naive setup managed 3/20.**
   Source: `B2-20260713T171721Z` (simurgh 20/20, incidents none; naive 3/20
   with install_clobber=5, wrong_variant=17).

2. **Without isolation, agents literally screenshot each other's app.**
   Source: `B1-20260713T154936Z/run-B1-naive-rep2-A.png` (+ rep1-B.png) —
   agent A's screenshot shows agent B's badge; 10/20 naive tasks at just 2
   agents hit real cross-contamination.

3. **Naive parallelism gets worse as you scale: 50% task failure at 2 agents
   → 85% at 4 agents, including real binary clobbering mid-run.**
   Source: B1 naive 10/20 vs B2 naive 3/20, install_clobber=5.

4. **Kill an agent mid-build: the DIY script orphans its booted simulator
   5/5 times; Simurgh's daemon reclaims the lease and deletes the device,
   4/4 cleanly-measured reps.**
   Source: `B4-20260713T180241Z/b4-residue-log.jsonl`. (Session-bound leases
   are now the shipped default — MCP `releaseOnPIDExit=true`, CLI `--rm`
   parent-binding — one confirming rep on pure defaults still queued.)

5. **Honest overhead, published before anyone asks: solo, Simurgh costs
   ~41 s per task over raw xcodebuild (48.7 s vs 7.3 s median).**
   Decomposition: 1.3 s acquire + ~14 s cold-DerivedData build tax + ~10.5 s
   cold install + ~9 s of the harness's own defensive boot wait. A careful
   hand-rolled isolation script pays nearly the same (38.6 s median) — this
   is the price of isolation, not of Simurgh.
   Source: `B3-20260713T175107Z`, decomposition table in RESULTS.md v2.

6. **Dogfooding found a real daemon bug before launch: a queued lease could
   stall forever after a transient RAM-pressure dip. Found under real
   4-agent load, root-caused, fixed, regression-tested, re-verified live
   (B2 20/20 after the fix).**
   Source: RESULTS.md "Bug 1", `internal/broker` `RetryQueues` +
   `TestRetryQueuesPromotesAfterTransientPressureDip`.

7. **Warm-DD on a real Lancer checkout does not speed up builds (−0.2%).**
   Cold 318.2s vs warm-seeded 319.0s; clonefile seeding works, but the toy
   ~30% win does not transfer. Do not publish the toy figure as the real-app
   payoff.
   Source: `bench/evidence/warmdd-lancer-20260714T033925Z.json`.

**Do not publish yet:** any B5 throughput claim, any "we build our own app
with it" B6 claim (n=4 + incomplete XcodeBuildMCP), any B2 time-to-green
until the 2026-07-14 B2 cell with `--retries` lands. B1 zero-collision /
time-to-green claims are publishable from the fresh+reuse evidence above
(all-green 10/10; cite reuse for the recommended pattern).
