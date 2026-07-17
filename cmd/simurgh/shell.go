package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/RoshanDewmina/simurgh/internal/broker"
	"github.com/RoshanDewmina/simurgh/internal/integrations/pathshim"
)

// Discovery seams for resolveShimBinary — swapped in tests so clean-checkout
// sibling discovery and PATH fallback are covered without relocating the
// real test binary.
var (
	shimExecutable = os.Executable
	shimLookPath   = exec.LookPath
)

// managedShellSession is the installed shim layout + environment that
// `simurgh shell` applies before starting the interactive shell. Extracted
// so process-level tests can exercise the real binary/symlink/audit path
// without driving an interactive TTY.
type managedShellSession struct {
	Paths pathshim.SessionPaths
	Env   []string
	Shell string
}

func runShell(args []string) int {
	var (
		leaseID string
		posSeen bool
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help" || a == "help":
			printShellHelp()
			return 0
		case strings.HasPrefix(a, "-"):
			printErrorTriple("INVALID_INPUT", fmt.Sprintf("unknown flag %q", a), "simurgh shell <lease-id>", false)
			return 3
		default:
			if posSeen {
				printErrorTriple("INVALID_INPUT", "only one lease-id allowed", "simurgh shell <lease-id>", false)
				return 3
			}
			leaseID = a
			posSeen = true
		}
	}
	if leaseID == "" {
		printErrorTriple("INVALID_INPUT", "lease-id is required", "simurgh shell <lease-id>", false)
		return 3
	}

	raw, err := dialAndCall(context.Background(), "lease.get", map[string]any{"id": leaseID})
	if err != nil {
		printIPCError(err, false)
		return exitCodeForIPCError(err)
	}
	var wrap struct {
		Lease broker.Lease `json:"lease"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		fmt.Fprintf(os.Stderr, "simurgh shell: decode: %v\n", err)
		return 5
	}
	if wrap.Lease.Device == nil || wrap.Lease.Device.UDID == "" {
		printErrorTriple("INVALID_INPUT", "lease has no device (not ACTIVE)", "wait for acquire to finish", false)
		return 3
	}
	iso := wrap.Lease.Env
	if len(iso.Env) == 0 {
		fmt.Fprintf(os.Stderr, "simurgh shell: lease env is empty\n")
		return 5
	}

	shimBin, err := resolveShimBinary()
	if err != nil {
		printErrorTriple("SHIM_NOT_FOUND", err.Error(), "go build -o simurgh-shim ./cmd/simurgh-shim", false)
		return 5
	}
	sess, err := prepareManagedShell(iso.Env, os.Environ(), shimBin)
	if err != nil {
		printErrorTriple("INTERNAL", err.Error(), shellPrepFix(err), false)
		return 5
	}

	renewCtx, stopRenew := context.WithCancel(context.Background())
	defer stopRenew()
	renewLoop := newExecRenewLoop(leaseID, wrap.Lease, func(ctx context.Context) error {
		_, renewErr := dialAndCall(ctx, "lease.renew", map[string]any{"id": leaseID})
		return renewErr
	})
	go renewLoop.run(renewCtx)

	fmt.Fprintf(os.Stderr,
		"simurgh shell: lease %s (udid %s)\n"+
			"  PATH shims: %s (audit mode — logs bare xcodebuild/xcrun to %s)\n"+
			"  Long builds/tests: prefer `simurgh exec %s -- xcodebuild …` for flag merge + auto-renew\n",
		leaseID, wrap.Lease.Device.UDID, sess.Paths.BinDir, sess.Paths.AuditLog, leaseID)

	cmd := exec.Command(sess.Shell)
	cmd.Env = sess.Env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "simurgh shell: %v\n", err)
		return 5
	}
	return 0
}

func printShellHelp() {
	fmt.Println(`usage: simurgh shell <lease-id>

Start an interactive shell with the lease isolation env applied and audit-mode
PATH shims for xcodebuild/xcrun prepended. Bare-name invocations are logged to
<lease-root>/pathshim/audit.log; they do not yet auto-inject isolation flags —
route long builds through simurgh exec <lease-id> -- xcodebuild … instead.

Requires simurgh-shim beside the simurgh binary (or on PATH). Build both:
  go build -o simurgh ./cmd/simurgh
  go build -o simurgh-shim ./cmd/simurgh-shim

The lease is renewed on a TTL/3 cadence while the shell runs.`)
}

func prepareManagedShell(leaseEnv map[string]string, baseEnviron []string, shimBin string) (managedShellSession, error) {
	leaseRoot, err := leaseRootFromEnv(leaseEnv)
	if err != nil {
		return managedShellSession{}, err
	}
	paths := pathshim.SessionDir(leaseRoot)
	if err := pathshim.InstallSessionShims(paths, shimBin); err != nil {
		return managedShellSession{}, fmt.Errorf("install PATH shims: %w", err)
	}

	// Lease keys and SIMURGH_SHIM_LOG must replace any inherited duplicates:
	// pathshim.LookupEnv (and the child shim) take the first KEY= match.
	env := append([]string(nil), baseEnviron...)
	for k, v := range leaseEnv {
		env = upsertEnv(env, k, v)
	}
	env = pathshim.PrependPathToEnv(env, paths.BinDir)
	env = upsertEnv(env, pathshim.ShimLogEnv, paths.AuditLog)

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	return managedShellSession{Paths: paths, Env: env, Shell: shell}, nil
}

// upsertEnv sets key=value in an environ slice, removing any prior KEY=
// entries so LookupEnv and child processes see the new value deterministically.
func upsertEnv(environ []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(environ)+1)
	replaced := false
	for _, kv := range environ {
		if strings.HasPrefix(kv, prefix) {
			if !replaced {
				out = append(out, key+"="+value)
				replaced = true
			}
			continue
		}
		out = append(out, kv)
	}
	if !replaced {
		out = append(out, key+"="+value)
	}
	return out
}

func shellPrepFix(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not a symlink"):
		return "remove non-symlink files under the lease pathshim/bin directory and retry: simurgh shell <lease-id>"
	case strings.Contains(msg, "SIMURGH_DERIVED_DATA"):
		return "wait for acquire to finish; lease must be ACTIVE"
	default:
		return "go build -o simurgh-shim ./cmd/simurgh-shim"
	}
}

func leaseRootFromEnv(env map[string]string) (string, error) {
	dd := env["SIMURGH_DERIVED_DATA"]
	if dd == "" {
		return "", fmt.Errorf("lease env missing SIMURGH_DERIVED_DATA")
	}
	return filepath.Dir(dd), nil
}

func resolveShimBinary() (string, error) {
	if exe, err := shimExecutable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "simurgh-shim")
		if st, statErr := os.Stat(candidate); statErr == nil && !st.IsDir() {
			return candidate, nil
		}
	}
	path, err := shimLookPath("simurgh-shim")
	if err != nil {
		return "", fmt.Errorf("simurgh-shim not found beside simurgh or on PATH — build with: go build -o simurgh-shim ./cmd/simurgh-shim")
	}
	return path, nil
}
