A pressure/capacity gate correctly refusing under real load is stronger proof than a forced rerun.

**What happened:** during M4 gate closure, a dogfood rerun was attempted under genuinely low free
memory (111MB unused physical, below the 4GB free+inactive floor). `simurgh acquire --json`
(no `--wait`) correctly returned `CAPACITY_QUEUED`/`reason: ram` instead of overcommitting. The
orchestrator did not kill unrelated processes or force a `--wait` grant to manufacture a "clean"
rerun.

**Why:** fabricating favorable conditions to get a rerun to pass defeats the purpose of the gate
being tested — the correct refusal under real, uncontrived load is itself the evidence that the
pressure gate works. A forced/staged pass would have proven nothing new (the successful-grant path
was already proven by an earlier concurrent-lease dogfood run) and would have set a bad precedent
of treating "green" as more valuable than "correct."

**How to apply:** when a live/dogfood verification step can't run cleanly because of genuine host
state (memory pressure, an unrelated process holding a resource, a simulator already in use),
don't manufacture conditions to force a pass. Investigate root cause first (is the load actually
unrelated to this session? — check start times / process ownership), then either (a) treat a
correct refusal as valid evidence for the behavior it's exercising, or (b) explicitly document the
step as blocked-by-host-state and defer it, never silently skip or fake it.
