`docs/plans/orchestrator-state.md` on the main checkout (master) goes stale the moment work moves
into a stacked worktree — always read the state file from the *active* package's worktree, not master.

**What happened:** getting oriented on this project meant reading `orchestrator-state.md`; the
copy on the `master` checkout was ~700 lines shorter than the copy in `.worktrees/r-wp6-xcodebuildmcp`
(the active work was two roadmap packages ahead of what master's copy described). Diffing the two
was needed to find the real current tail.

**Why:** this project's workflow is stacked local worktrees (`.worktrees/r-wpN-*`), each carrying
its own copy of the state file forward from its base commit. `master` isn't fast-forwarded until a
package is reviewed and merged, so its copy of any doc under `docs/plans/` reflects whatever the
last merge left behind — not the current frontier of work.

**How to apply:** when resuming a session or getting oriented, identify the *active* (most recent,
unmerged) worktree first — `git worktree list`, then find the tip with the newest commit — and
read the state file from there, not from the main checkout. Once R-WP0..R-WP4 were merged to
master this session, master's copy became current for that slice of history again; the same drift
will recur for R-WP6 until it's merged too.
