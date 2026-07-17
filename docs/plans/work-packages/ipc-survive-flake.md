# darwin_sim parent-survival test flake

**Goal:** Make `TestDialDaemonSurvivesParentExit` deterministic under the full
parallel `darwin_sim` race suite without weakening its same-PID, parent-exit,
30-second survival, or SIGHUP assertions.

**Root-cause evidence:** On final HEAD `2c8332e`, the full tagged race suite
failed twice because the test's one-shot 2-second ping read timed out while
other live simulator tests were running. The exact test passed in 34.26s when
run alone. Product detach behavior also passed W11 and GitHub #3 live proof.
This isolates the failure to the test's response deadline under parallel host
load, not daemon death.

**Write-set:** `internal/ipc/client_survive_test.go` only.

**Required behavior:**
- Replace the one-shot startup/loop ping assumption with bounded,
  condition-based polling that tolerates transient scheduling/CoreSimulator
  contention.
- Continue to fail if the daemon dies, its PID changes, it cannot answer within
  the bounded window, or SIGHUP terminates it.
- Keep total test runtime bounded; do not add arbitrary unconditional sleeps.

**Acceptance:**
```bash
go test -race -count=10 ./internal/ipc -run TestDialDaemonSurvivesParentExit
go test -race -tags=darwin_sim -count=1 ./...
go test -race -count=1 ./...
go vet ./...
```

**Negative:** A wrong PID must still fail immediately after a successful ping;
an endpoint that never responds must fail when the bounded poll deadline
expires.

**Risk:** low (test-only).

**Evidence clause:**
> Before reporting progress, audit each claim against a tool result from this session. Only
> report work you can point to evidence for; if tests fail, say so with the output; if a step
> was skipped, say that. Paste real command output, never paraphrase it.
