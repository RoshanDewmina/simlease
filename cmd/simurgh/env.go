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
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
	"github.com/RoshanDewmina/simurgh/internal/isolator"
)

func runEnv(args []string) int {
	var (
		leaseID string
		asJSON  bool
		posSeen bool
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			asJSON = true
		case a == "-h" || a == "--help" || a == "help":
			fmt.Println(`usage: simurgh env <lease-id> [--json]
Print eval-able export KEY=value lines for the lease isolation env.
Default (no --json) is meant for: eval "$(simurgh env <lease-id>)"`)
			return 0
		case strings.HasPrefix(a, "-"):
			printErrorTriple("INVALID_INPUT", fmt.Sprintf("unknown flag %q", a), "simurgh env <lease-id>", asJSON)
			return 3
		default:
			if posSeen {
				printErrorTriple("INVALID_INPUT", "only one lease-id allowed", "simurgh env <lease-id>", asJSON)
				return 3
			}
			leaseID = a
			posSeen = true
		}
	}
	if leaseID == "" {
		printErrorTriple("INVALID_INPUT", "lease-id is required", "simurgh env <lease-id>", asJSON)
		return 3
	}

	raw, err := dialAndCall(context.Background(), "lease.get", map[string]any{"id": leaseID})
	if err != nil {
		printIPCError(err, asJSON)
		return exitCodeForIPCError(err)
	}
	var wrap struct {
		Lease broker.Lease `json:"lease"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		fmt.Fprintf(os.Stderr, "simurgh env: decode: %v\n", err)
		return 5
	}
	if wrap.Lease.Device == nil || wrap.Lease.Device.UDID == "" {
		printErrorTriple("INVALID_INPUT", "lease has no device (not ACTIVE)", "wait for acquire to finish", asJSON)
		return 3
	}

	envMap := wrap.Lease.Env.Env
	if len(envMap) == 0 {
		fmt.Fprintf(os.Stderr, "simurgh env: lease env is empty\n")
		return 5
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(wrap.Lease.Env)
		return 0
	}

	// Stable-ish order matching ARCHITECTURE §7 field list.
	keys := []string{
		"SIMURGH_LEASE_ID",
		"SIMURGH_UDID",
		"SIMURGH_DERIVED_DATA",
		"CLANG_MODULE_CACHE_PATH",
		"SIMURGH_RESULT_BUNDLE_DIR",
		"SIMURGH_SWIFTPM_DIR",
	}
	seen := map[string]bool{}
	for _, k := range keys {
		v, ok := envMap[k]
		if !ok {
			continue
		}
		seen[k] = true
		fmt.Printf("export %s=%s\n", k, shellQuote(v))
	}
	for k, v := range envMap {
		if seen[k] {
			continue
		}
		fmt.Printf("export %s=%s\n", k, shellQuote(v))
	}
	return 0
}

func runExec(args []string) int {
	var (
		leaseID string
		cmdArgs []string
		sawDash bool
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		if sawDash {
			cmdArgs = append(cmdArgs, a)
			continue
		}
		switch {
		case a == "--":
			sawDash = true
		case a == "-h" || a == "--help" || a == "help":
			fmt.Println(`usage: simurgh exec <lease-id> -- <cmd...>
Run <cmd> with the lease isolation env injected. For xcodebuild, also injects
isolation flags (-derivedDataPath, -clonedSourcePackagesDirPath, -destination).
For xcodebuild test / test-without-building, additionally allocates a fresh
-resultBundlePath under the lease Results/ dir.
Exit code is the child's exit code (not the IPC 0/2/3/4/5 contract).`)
			return 0
		case strings.HasPrefix(a, "-"):
			printErrorTriple("INVALID_INPUT", fmt.Sprintf("unknown flag %q", a), "simurgh exec <lease-id> -- <cmd...>", false)
			return 3
		default:
			if leaseID != "" {
				printErrorTriple("INVALID_INPUT", "unexpected argument before --", "simurgh exec <lease-id> -- <cmd...>", false)
				return 3
			}
			leaseID = a
		}
	}
	if leaseID == "" {
		printErrorTriple("INVALID_INPUT", "lease-id is required", "simurgh exec <lease-id> -- <cmd...>", false)
		return 3
	}
	if !sawDash || len(cmdArgs) == 0 {
		printErrorTriple("INVALID_INPUT", "command required after --", "simurgh exec <lease-id> -- <cmd...>", false)
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
		fmt.Fprintf(os.Stderr, "simurgh exec: decode: %v\n", err)
		return 5
	}
	if wrap.Lease.Device == nil || wrap.Lease.Device.UDID == "" {
		printErrorTriple("INVALID_INPUT", "lease has no device (not ACTIVE)", "wait for acquire to finish", false)
		return 3
	}

	iso := wrap.Lease.Env
	if len(iso.Env) == 0 {
		fmt.Fprintf(os.Stderr, "simurgh exec: lease env is empty\n")
		return 5
	}

	argv := append([]string(nil), cmdArgs...)
	if isXcodebuild(argv) {
		// Inject base isolation flags after the binary (PATH shim is v0.1 — not built).
		flags := append([]string(nil), iso.XcodebuildFlags...)
		if isXcodebuildTest(argv) {
			results := iso.Env["SIMURGH_RESULT_BUNDLE_DIR"]
			if results == "" {
				results = filepath.Join(iso.Env["SIMURGH_DERIVED_DATA"], "..", "Results")
			}
			bundle := isolator.AllocateResultBundlePath(isolator.LeaseDirs{Results: results}, time.Now)
			flags = append(flags, "-resultBundlePath", bundle)
		}
		argv = append([]string{argv[0]}, append(flags, argv[1:]...)...)
	}

	env := append([]string(nil), os.Environ()...)
	for k, v := range iso.Env {
		env = append(env, k+"="+v)
	}

	cmd := exec.CommandContext(context.Background(), argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	err = cmd.Run()
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	fmt.Fprintf(os.Stderr, "simurgh exec: %v\n", err)
	return 5
}

func isXcodebuild(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	base := filepath.Base(argv[0])
	return base == "xcodebuild"
}

func isXcodebuildTest(argv []string) bool {
	if !isXcodebuild(argv) {
		return false
	}
	for _, a := range argv[1:] {
		if a == "test" || a == "test-without-building" {
			return true
		}
	}
	return false
}

// shellQuote returns a single-quoted shell string safe for eval.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
