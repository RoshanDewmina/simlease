// Package pathshim implements Simurgh's PATH shim for xcodebuild/xcrun
// (R-WP5). This is STAGE 1 of 3: audit only. It observes and logs every
// bare-name invocation of xcodebuild/xcrun that lands on it, injects
// nothing, rejects nothing, and always execs the real binary with
// unmodified argv and passes its exit code straight through. Warn mode
// (flag injection) and enforce mode (blocking destructive simctl
// operations) are explicitly future work, gated on real audit-log
// evidence from this stage — neither is implemented here.
//
// # Design: one shim binary, dispatched by argv[0]
//
// There is one shim binary source (cmd/simurgh-shim), not two. `simurgh
// shell <session-id>` creates two symlinks in a session-scoped bin
// directory — named exactly "xcodebuild" and "xcrun" — that both point at
// the same compiled simurgh-shim executable. At invocation, the shim reads
// filepath.Base(os.Args[0]) to decide which real tool it is standing in
// for. This works because POSIX exec semantics pass the caller's/PATH
// lookup's own name as argv[0], not the symlink's resolved target: a shell
// that finds "xcodebuild" first on PATH and execs it invokes the process
// with argv[0] == "xcodebuild" even though the inode is simurgh-shim's.
// This is the common Unix "busybox" multi-call pattern, chosen over two
// near-duplicate binaries because the two tools' shim logic (resolve real
// binary, guard recursion, exec unmodified, log) is identical modulo the
// tool name.
//
// simurgh-shim is intentionally its OWN binary, not a hidden mode of the
// main `simurgh` CLI (cmd/simurgh/main.go's os.Args[1] subcommand switch
// is untouched by this package) — PATH-shimming has zero surface area
// inside the existing command tree, and a session's PATH only ever
// contains "xcodebuild"/"xcrun" symlinks, never anything named "simurgh".
//
// # Recursion safety
//
// The real binary is resolved via an ABSOLUTE path only — DEVELOPER_DIR if
// set, else `xcode-select -p` plus the well-known "usr/bin/<name>" suffix —
// NEVER via a bare-name exec.LookPath. Looking up "xcodebuild" by name
// while the shim's own session bin directory could still be first on PATH
// is exactly how a shim recurses into itself. On top of that
// belt-and-braces absolute-path resolution, Run sets SIMURGH_SHIM_ACTIVE=1
// in the real binary's environment before exec'ing it, and refuses outright
// (no exec at all) if it ever finds that variable already set on entry.
// That should be unreachable given absolute-path resolution alone, which is
// exactly why it exists as defense-in-depth rather than the only guard.
//
// # Known coverage gap (not a bug)
//
// Audit mode only sees bare-name (PATH-resolved) invocations of
// xcodebuild/xcrun. Any caller that already holds, or hardcodes, an
// absolute path to the real binaries bypasses this shim entirely — that
// invocation is invisible to the audit log. This is a real, known,
// currently-unaddressed limitation of stage 1, not a safety promise that
// "everything is covered." See docs/ARCHITECTURE.md and `simurgh shell
// -h` for the same disclosure.
package pathshim
