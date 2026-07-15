package pathshim

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	// ShimActiveEnv is the recursion guard: set to "1" by Run in the real
	// binary's environment right before exec, and checked on entry. If it
	// is already "1" when Run starts, Run refuses to proceed at all — see
	// the package doc for why this must never trigger given absolute-path
	// resolution, and why it exists anyway (defense-in-depth).
	ShimActiveEnv = "SIMURGH_SHIM_ACTIVE"
	// ShimLogEnv names the audit-log file path a shim invocation appends
	// one AuditEntry to. `simurgh shell` sets it; if it's unset or empty,
	// Run still resolves and execs the real binary (audit mode must never
	// fail a build because logging is misconfigured) but silently skips
	// logging — a diagnostic goes to stderr instead so the gap is visible
	// without ever changing the child's exit code.
	ShimLogEnv = "SIMURGH_SHIM_LOG"
)

// Exit codes Run itself can return without ever having exec'd the real
// binary. Chosen well outside the 0-255 range real tools normally use
// meaningfully, so a caller scripting against exit codes can tell "the
// real xcodebuild failed" apart from "the shim refused to run it at all".
const (
	// ExitRecursionBlocked is returned when the recursion guard trips.
	ExitRecursionBlocked = 90
	// ExitResolutionFailed is returned when the real binary could not be
	// resolved (no DEVELOPER_DIR, xcode-select failed, binary missing) or
	// could not be exec'd once resolved.
	ExitResolutionFailed = 91
)

// ExecFunc runs the real binary and returns its exit code. Injectable so
// tests never depend on a real xcodebuild/xcrun; DefaultExecFunc wires a
// real os/exec.Cmd.
type ExecFunc func(path string, args []string, env []string, stdin io.Reader, stdout, stderr io.Writer) (exitCode int, err error)

// DefaultExecFunc runs the real binary as a real child process, forwarding
// stdio exactly and translating its exit code the same way
// cmd/simurgh/env.go's runExec already does for `simurgh exec`.
func DefaultExecFunc(ctx context.Context) ExecFunc {
	return func(path string, args []string, env []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.Env = env
		cmd.Stdin = stdin
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		err := cmd.Run()
		if err == nil {
			return 0, nil
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), nil
		}
		return -1, err
	}
}

// RunOptions carries everything Run needs, all injectable for tests.
type RunOptions struct {
	// ShimName is which real tool this invocation stands in for —
	// "xcodebuild" or "xcrun". Callers derive it from
	// filepath.Base(os.Args[0]); Run itself never reads os.Args.
	ShimName string
	// Args is argv AFTER argv[0] — the arguments to forward unmodified.
	Args []string
	// Environ is the full environment as seen by the shim process, in
	// os.Environ() "KEY=value" form.
	Environ []string
	// Resolver resolves the real binary's absolute path.
	Resolver Resolver
	// Exec runs the real binary. Defaults to DefaultExecFunc(context.Background())
	// if nil.
	Exec ExecFunc
	// Now returns the current time for the audit log entry. Defaults to time.Now.
	Now func() time.Time

	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

// Run is the shim's entire behavior: check the recursion guard, resolve
// the real binary via an absolute path, exec it with completely unmodified
// argv, append one audit-log entry, and return the real binary's own exit
// code unchanged. Run never injects a flag, never rejects an invocation,
// and never alters stdout/stderr beyond exact passthrough — audit mode is
// functionally transparent except for the audit-log side channel.
func Run(opts RunOptions) int {
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stdin := opts.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	// Recursion guard (defense-in-depth; see package doc). Checked FIRST,
	// before any resolution work, and — deliberately — no audit-log entry
	// is written on this path: nothing real ran, and keeping this specific
	// path as small and dependency-free as possible (no filesystem writes
	// beyond stderr) is itself part of what makes "refuse immediately"
	// reliable rather than a second place this could fail or hang.
	if lookupEnv(opts.Environ, ShimActiveEnv) == "1" {
		fmt.Fprintf(stderr,
			"simurgh pathshim: refusing to run %s — %s is already set in this "+
				"process's environment. That means this shim invocation would be "+
				"resolving/re-exec'ing itself instead of the real Xcode toolchain "+
				"(recursion). This should be unreachable because the real binary is "+
				"always resolved via an absolute DEVELOPER_DIR-based path, never a "+
				"bare-name PATH lookup — if you see this, something upstream (e.g. a "+
				"broken symlink, or the shim's own session bin dir appearing on PATH "+
				"ahead of the real Xcode toolchain in an unexpected way) needs "+
				"investigating. Not proceeding.\n",
			opts.ShimName, ShimActiveEnv)
		return ExitRecursionBlocked
	}

	real, err := opts.Resolver.Resolve(opts.ShimName)
	if err != nil {
		fmt.Fprintf(stderr, "simurgh pathshim: cannot resolve real %s: %v\n", opts.ShimName, err)
		return ExitResolutionFailed
	}

	execFn := opts.Exec
	if execFn == nil {
		execFn = DefaultExecFunc(context.Background())
	}

	childEnv := append(append([]string(nil), opts.Environ...), ShimActiveEnv+"=1")

	exitCode, execErr := execFn(real, opts.Args, childEnv, stdin, stdout, stderr)
	if execErr != nil {
		fmt.Fprintf(stderr, "simurgh pathshim: exec %s: %v\n", real, execErr)
		exitCode = ExitResolutionFailed
	}

	if logPath := lookupEnv(opts.Environ, ShimLogEnv); logPath != "" {
		entry := AuditEntry{
			Timestamp:  now().UTC(),
			Binary:     opts.ShimName,
			Argv:       append([]string{opts.ShimName}, opts.Args...),
			RealBinary: real,
			ExitCode:   exitCode,
			Mode:       ModeAudit,
		}
		if aerr := AppendAuditLog(logPath, entry); aerr != nil {
			// Audit logging is a side channel, not part of the contract with
			// the caller of xcodebuild/xcrun: a log write failure must never
			// change the real binary's exit code, only be visible on stderr.
			fmt.Fprintf(stderr, "simurgh pathshim: audit log write failed (non-fatal): %v\n", aerr)
		}
	}

	return exitCode
}

func lookupEnv(environ []string, key string) string {
	prefix := key + "="
	for _, kv := range environ {
		if strings.HasPrefix(kv, prefix) {
			return kv[len(prefix):]
		}
	}
	return ""
}
