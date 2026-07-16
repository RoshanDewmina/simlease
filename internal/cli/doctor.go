package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Stable check failure codes.
const (
	CodeXcodeMissing      = "XCODE_MISSING"
	CodeSimctlUnavailable = "SIMCTL_UNAVAILABLE"
	CodeIOSRuntimeMissing = "IOS_RUNTIME_MISSING"
	CodeSocketInvalid     = "SOCKET_INVALID"
	CodeDaemonNotRunning  = "DAEMON_NOT_RUNNING"
	CodeStaleLeases       = "STALE_LEASES"
	CodeHomeMismatch      = "HOME_MISMATCH"
)

// Exact fix command strings for failing checks.
const (
	FixXcodeMissing      = "xcode-select --install"
	FixSimctlUnavailable = "xcode-select --install"
	FixIOSRuntimeMissing = "xcodebuild -downloadPlatform iOS"
	FixSocketInvalid     = "simurgh daemon run"
	FixDaemonNotRunning  = "simurgh daemon run"
	// Stale ACTIVE leases are past ExpiresAt; release triggers immediate teardown
	// (M2.3). prune does not touch ACTIVE leases — do not suggest prune here.
	FixStaleLeases  = "simurgh release <lease-id>"
	FixHomeMismatch = "export SIMURGH_HOME=$HOME/.simurgh  # or unset HOME to match passwd home"
)

// StaleLeaseGrace is how far past ExpiresAt an ACTIVE lease may drift before
// doctor treats it as stale (watcher ticks every 5s; 5m is a real health signal).
const StaleLeaseGrace = 5 * time.Minute

// Check is one doctor diagnostic result. Failures always carry Code, Message, and Fix.
type Check struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Fix     string `json:"fix,omitempty"`
}

// LeaseExpiryInfo is the minimal lease shape doctor needs for the stale check.
type LeaseExpiryInfo struct {
	ID        string
	State     string
	ExpiresAt time.Time
}

// DoctorOptions injects seams so RunChecks is unit-testable without a real Mac.
type DoctorOptions struct {
	XcodeVersion   func(ctx context.Context) (string, error)
	SimctlRuntimes func(ctx context.Context) ([]string, error)
	SocketPath     string
	DialPing       func(ctx context.Context) error
	// ListLeases lists leases via the daemon (injected; unused when DialPing fails).
	ListLeases func(ctx context.Context) ([]LeaseExpiryInfo, error)
	// LeasesRoot is ~/.simurgh/leases (or $SIMURGH_HOME/leases) for disk usage.
	LeasesRoot string
	// Now overrides wall clock for the stale-lease check (tests).
	Now func() time.Time
	// Home resolution seams for the home-mismatch check (tests).
	SimurghHomeEnv func() string
	PasswdHome     func() (string, error)
	EnvHome        func() string
}

// RunChecks runs all doctor checks and returns every result (collect-all, not fail-fast).
func RunChecks(ctx context.Context, opts DoctorOptions) []Check {
	checks := make([]Check, 0, 8)
	checks = append(checks, checkXcode(ctx, opts))
	simctl, runtimes := checkSimctlAndRuntimes(ctx, opts)
	checks = append(checks, simctl, runtimes)
	checks = append(checks, checkHomeMismatch(opts))
	checks = append(checks, checkSocket(opts.SocketPath))
	checks = append(checks, checkDaemon(ctx, opts))
	checks = append(checks, checkStaleLeases(ctx, opts))
	checks = append(checks, checkDiskUsage(opts))
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

func checkHomeMismatch(opts DoctorOptions) Check {
	const name = "home-mismatch"
	simurghHome := ""
	if opts.SimurghHomeEnv != nil {
		simurghHome = opts.SimurghHomeEnv()
	}
	if simurghHome != "" {
		return ok(name, fmt.Sprintf("SIMURGH_HOME set (%s)", simurghHome))
	}
	if opts.PasswdHome == nil || opts.EnvHome == nil {
		return fail(name, CodeHomeMismatch, "home resolution probes not configured", FixHomeMismatch)
	}
	passwdHome, err := opts.PasswdHome()
	if err != nil {
		return fail(name, CodeHomeMismatch, fmt.Sprintf("passwd home lookup failed: %v", err), FixHomeMismatch)
	}
	envHome := opts.EnvHome()
	if envHome == "" || envHome == passwdHome {
		return ok(name, fmt.Sprintf("HOME matches passwd home (%s)", passwdHome))
	}
	return fail(name, CodeHomeMismatch,
		fmt.Sprintf("HOME=%q differs from passwd home %q while SIMURGH_HOME is unset", envHome, passwdHome),
		FixHomeMismatch)
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

func checkStaleLeases(ctx context.Context, opts DoctorOptions) Check {
	const name = "stale-leases"
	// Reuse the same daemon-liveness signal — do not invent a second message.
	if opts.DialPing == nil {
		return fail(name, CodeDaemonNotRunning, "daemon ping probe not configured", FixDaemonNotRunning)
	}
	if err := opts.DialPing(ctx); err != nil {
		return fail(name, CodeDaemonNotRunning, fmt.Sprintf("daemon is not responding: %v", err), FixDaemonNotRunning)
	}
	if opts.ListLeases == nil {
		return fail(name, CodeStaleLeases, "lease list probe not configured", FixStaleLeases)
	}
	leases, err := opts.ListLeases(ctx)
	if err != nil {
		return fail(name, CodeStaleLeases, fmt.Sprintf("lease.list failed: %v", err), FixStaleLeases)
	}
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	cutoff := now().Add(-StaleLeaseGrace)
	var stale []string
	for _, l := range leases {
		if l.State != "active" {
			continue
		}
		if l.ExpiresAt.IsZero() {
			continue
		}
		if l.ExpiresAt.Before(cutoff) {
			stale = append(stale, l.ID)
		}
	}
	if len(stale) == 0 {
		return ok(name, "0 stale active leases")
	}
	return fail(name, CodeStaleLeases,
		fmt.Sprintf("%d active lease(s) expired >%s ago: %s", len(stale), StaleLeaseGrace, strings.Join(stale, ", ")),
		FixStaleLeases)
}

func checkDiskUsage(opts DoctorOptions) Check {
	const name = "disk-usage"
	root := opts.LeasesRoot
	if root == "" {
		// Informational always-OK: missing root is reported as 0B.
		return ok(name, "leases/: 0B")
	}
	size, err := dirSize(root)
	if err != nil {
		if os.IsNotExist(err) {
			return ok(name, "leases/: 0B")
		}
		// Still informational — report the error in the message but stay OK.
		return ok(name, fmt.Sprintf("leases/: (could not measure: %v)", err))
	}
	return ok(name, fmt.Sprintf("leases/: %s", formatBytes(size)))
}

func dirSize(root string) (int64, error) {
	var total int64
	err := filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

func formatBytes(n int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case n >= gb:
		return fmt.Sprintf("%.1fGB", float64(n)/float64(gb))
	case n >= mb:
		return fmt.Sprintf("%dMB", n/mb)
	case n >= kb:
		return fmt.Sprintf("%dKB", n/kb)
	default:
		return fmt.Sprintf("%dB", n)
	}
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
