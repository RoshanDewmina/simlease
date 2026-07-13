# Simurgh review standards

These rules supplement the product and architecture source of truth. Reviewers must report findings with severity `blocking|major|minor|nit` and confidence `certain|likely|speculative`; numeric confidence and numeric self-scores are invalid.

- Never weaken the deletion invariant in `ARCHITECTURE.md` section 2.
- Every user-facing error must include a stable code, a human message, and an exact fix command.
- No destructive simulator or recursive filesystem operation may bypass `internal/safety`.
- No public remote, telemetry, license file, private Apple framework, or post-v0 scope may be added.
- Tests and docs must not claim a real-simulator gate passed unless the literal macOS command was run successfully.
- Review beyond the diff: check all dependents of changed public symbols.
- Minor and nit findings are logged but do not block integration.
