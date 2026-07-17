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
- When a fix changes a type's ownership from one-instance-per-call to one
  long-lived-shared-instance (e.g. moving it into the daemon), audit every mutable
  field on that type for concurrent access, not just the fields the bug fix itself
  touched — a field with zero synchronization was safe by accident (single-threaded
  by construction) and becomes a real data race the moment it's shared across
  goroutines. Prove it with a `-race` test exercising concurrent calls, don't just
  reason about it. (Found 2026-07-14, R-WP6: fixing B2 by sharing one `*Adapter`
  across daemon connections exposed `stdinHolders` — never mutex-guarded because it
  never needed to be before — to real concurrent map access.)
- Env vars read directly in a shipped production code path (not gated behind a
  build tag or an equivalent compile-time split) are an unconditional backdoor,
  even when added only to make a test practical. Test-only overrides must be
  eliminated from the real binary at compile time (e.g. the same build-tag pattern
  already used for `darwin_sim`-gated live test files), never left as a runtime
  env-var check with no secondary guard. (Found 2026-07-14, R-WP6: two env vars
  added to support a new regression test were readable by the real `simurgh daemon
  run` path with no gate — one could substitute an arbitrary binary for
  XcodeBuildMCP, the other silently disabled the pressure gate.)
- When replacing an IPC/RPC round-trip with an in-process shortcut for performance
  or simplicity, the shortcut must reproduce the original path's full error
  classification, not just its happy path — diff the old and new error handling
  for every documented error code, not only the cases a manual test happens to hit.
  (Found 2026-07-14, R-WP6: replacing a `dialAndCall("lease.get", ...)` round-trip
  with a bare in-process closure silently changed lease-not-found from a structured
  `LEASE_NOT_FOUND`/exit-3 error to a generic `INTERNAL`/exit-5 one.)
- A transport error classification shared by multiple public boundaries must be
  wired and tested at every boundary that performs the response read (currently
  CLI, MCP, and doctor), not only at the boundary where the bug was first
  observed. Include a negative protocol-frame test so connection-loss mapping
  cannot swallow malformed/truncated responses. (Found 2026-07-17, issue #2:
  the first fix mapped mid-RPC EOF for CLI while MCP still returned bare EOF.)
