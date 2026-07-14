# Simurgh review standards

These rules supplement the product and architecture source of truth. Reviewers must report findings with severity `blocking|major|minor|nit` and confidence `certain|likely|speculative`; numeric confidence and numeric self-scores are invalid.

- Never weaken the deletion invariant in `ARCHITECTURE.md` section 2.
- Every user-facing error must include a stable code, a human message, and an exact fix command.
- The error triple is enforced at CLI, MCP, and IPC response boundaries. Internal packages return contextual errors that those boundaries map; do not require internal errors themselves to contain shell commands.
- No destructive simulator or recursive filesystem operation may bypass `internal/safety`.
- No public remote, telemetry, license file, private Apple framework, or post-v0 scope may be added.
- Tests and docs must not claim a real-simulator gate passed unless the literal macOS command was run successfully.
- Review beyond the diff: check all dependents of changed public symbols.
- Minor and nit findings are logged but do not block integration.
- Any filesystem path derived from a CLI-supplied or session-supplied ID must go
  through the same allowlist/validation pattern `internal/safety.ValidateLeasePath`
  establishes for lease IDs — never a raw `filepath.Join` against a root directory.
  (Found 2026-07-14, R-WP6: `Stop`/`Status` joined `sessionID` directly while the
  sibling `Start` path went through the trusted lease resolver.)
- For any feature whose real entrypoint is a CLI subcommand invoked as a separate
  OS process (not a long-lived daemon), tests must exercise it across real separate
  process invocations, not just as in-process Go function calls within one test
  binary. In-process calls share memory (maps, open file descriptors, goroutines)
  that a real multi-invocation CLI usage does not, and can make a broken
  process-lifetime contract look fully tested. (Found 2026-07-14, R-WP6: a stdin
  pipe meant to persist between separate `start`/`stop` CLI calls was held only in
  one process's memory and closed the instant that process exited — invisible to
  both the unit tests and the live test because both called `Start`/`Stop` in the
  same test process.)
