package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Stable check failure codes.
const (
	CodeXcodeMissing      = "XCODE_MISSING"
	CodeSimctlUnavailable = "SIMCTL_UNAVAILABLE"
	CodeIOSRuntimeMissing = "IOS_RUNTIME_MISSING"
	CodeSocketInvalid     = "SOCKET_INVALID"
	CodeDaemonNotRunning  = "DAEMON_NOT_RUNNING"
)

// Exact fix command strings for failing checks.
const (
	FixXcodeMissing      = "xcode-select --install"
	FixSimctlUnavailable = "xcode-select --install"
	FixIOSRuntimeMissing = "xcodebuild -downloadPlatform iOS"
	FixSocketInvalid     = "simurgh daemon run"
	FixDaemonNotRunning  = "simurgh daemon run"
)

// Check is one doctor diagnostic result. Failures always carry Code, Message, and Fix.
type Check struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Fix     string `json:"fix,omitempty"`
}

// DoctorOptions injects seams so RunChecks is unit-testable without a real Mac.
type DoctorOptions struct {
	XcodeVersion   func(ctx context.Context) (string, error)
	SimctlRuntimes func(ctx context.Context) ([]string, error)
	SocketPath     string
	DialPing       func(ctx context.Context) error
}

// RunChecks runs all doctor checks and returns every result (collect-all, not fail-fast).
func RunChecks(ctx context.Context, opts DoctorOptions) []Check {
	checks := make([]Check, 0, 5)
	checks = append(checks, checkXcode(ctx, opts))
	simctl, runtimes := checkSimctlAndRuntimes(ctx, opts)
	checks = append(checks, simctl, runtimes)
	checks = append(checks, checkSocket(opts.SocketPath))
	checks = append(checks, checkDaemon(ctx, opts))
	return checks
}

// ExitCode maps check results to the CLI exit convention: 0 all green, 4 environment problem.
func ExitCode(checks []Check) int {
	for _, c := range checks {
		if !c.OK {
			return 4
		}
	}
	return 0
}

func checkXcode(ctx context.Context, opts DoctorOptions) Check {
	const name = "xcode"
	if opts.XcodeVersion == nil {
		return fail(name, CodeXcodeMissing, "xcode version probe not configured", FixXcodeMissing)
	}
	out, err := opts.XcodeVersion(ctx)
	if err != nil {
		return fail(name, CodeXcodeMissing, fmt.Sprintf("xcodebuild not available: %v", err), FixXcodeMissing)
	}
	msg := firstLine(out)
	if msg == "" {
		msg = "Xcode present"
	}
	return ok(name, msg)
}

func checkSimctlAndRuntimes(ctx context.Context, opts DoctorOptions) (Check, Check) {
	const simctlName = "simctl"
	const runtimesName = "runtimes"

	if opts.SimctlRuntimes == nil {
		return fail(simctlName, CodeSimctlUnavailable, "simctl probe not configured", FixSimctlUnavailable),
			fail(runtimesName, CodeIOSRuntimeMissing, "cannot list iOS runtimes: simctl probe not configured", FixIOSRuntimeMissing)
	}

	names, err := opts.SimctlRuntimes(ctx)
	if err != nil {
		return fail(simctlName, CodeSimctlUnavailable, fmt.Sprintf("simctl not available: %v", err), FixSimctlUnavailable),
			fail(runtimesName, CodeIOSRuntimeMissing, fmt.Sprintf("cannot list iOS runtimes: %v", err), FixIOSRuntimeMissing)
	}

	simctlOK := ok(simctlName, "simctl available")
	ios := filterIOSRuntimes(names)
	if len(ios) == 0 {
		return simctlOK, fail(runtimesName, CodeIOSRuntimeMissing, "no installed iOS simulator runtime", FixIOSRuntimeMissing)
	}
	return simctlOK, ok(runtimesName, fmt.Sprintf("%d iOS runtime(s): %s", len(ios), strings.Join(ios, ", ")))
}

func checkSocket(path string) Check {
	const name = "socket"
	if path == "" {
		return fail(name, CodeSocketInvalid, "socket path is empty", FixSocketInvalid)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return fail(name, CodeSocketInvalid, fmt.Sprintf("socket %s not found: %v", path, err), FixSocketInvalid)
	}
	if fi.Mode()&os.ModeSocket == 0 {
		return fail(name, CodeSocketInvalid, fmt.Sprintf("%s exists but is not a unix socket", path), FixSocketInvalid)
	}
	perm := fi.Mode().Perm()
	if perm != 0o600 {
		return fail(name, CodeSocketInvalid,
			fmt.Sprintf("socket %s mode is %#o, want 0600", path, perm),
			FixSocketInvalid)
	}
	return ok(name, fmt.Sprintf("%s mode 0600", path))
}

func checkDaemon(ctx context.Context, opts DoctorOptions) Check {
	const name = "daemon"
	if opts.DialPing == nil {
		return fail(name, CodeDaemonNotRunning, "daemon ping probe not configured", FixDaemonNotRunning)
	}
	if err := opts.DialPing(ctx); err != nil {
		return fail(name, CodeDaemonNotRunning, fmt.Sprintf("daemon is not responding: %v", err), FixDaemonNotRunning)
	}
	return ok(name, "daemon.ping ok")
}

func filterIOSRuntimes(names []string) []string {
	var out []string
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if strings.HasPrefix(n, "iOS") {
			out = append(out, n)
		}
	}
	return out
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func ok(name, message string) Check {
	return Check{Name: name, OK: true, Message: message}
}

func fail(name, code, message, fix string) Check {
	return Check{Name: name, OK: false, Code: code, Message: message, Fix: fix}
}
