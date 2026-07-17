# darwin_sim provisioner cleanup isolation

**Goal:** Make `TestProvisionRealClone` verify cleanup of only the exact clone
it creates so the full parallel `darwin_sim` suite does not fail on another
test's live Simurgh clone.

**Root-cause evidence:** `cleanupClone` deletes its exact UDID, then scans every
CoreSimulator device and fails if *any* `simurgh-clone[...]` remains. This
contradicts the test's own exact-UDID cleanup contract and fails when other
tagged packages run live clone tests concurrently. Two final-gate runs passed
`internal/ipc` after its poll fix but failed here on a different clone.

**Write-set:** `internal/provisioner/integration_test.go` only.

**Required behavior:**
- Cleanup still shuts down and deletes the exact UDID created by this test.
- Post-cleanup assertion checks that exact UDID is absent.
- Other Simurgh clones are neither deleted nor treated as this test's failure.
- The create assertion must identify this test's exact UDID, not only a
  substring-matching name.

**Acceptance:**
```bash
go test -race -tags=darwin_sim -count=3 ./internal/provisioner -run TestProvisionRealClone
go test -race -tags=darwin_sim -count=1 ./...
go test -race -count=1 ./...
go vet ./...
```

**Negative:** Seed or concurrently create a foreign `simurgh-clone[...]`;
cleanup of the test-owned UDID must leave the foreign clone untouched and pass.

**Risk:** sensitive test cleanup — real CoreSimulator deletion, exact owned
UDID only.

**Evidence clause:**
> Before reporting progress, audit each claim against a tool result from this session. Only
> report work you can point to evidence for; if tests fail, say so with the output; if a step
> was skipped, say that. Paste real command output, never paraphrase it.
