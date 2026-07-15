package pathshim

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRunRecursionGuardRefusesInProcess is the in-process complement to
// cmd/simurgh-shim's real-subprocess recursion test: it proves Run's guard
// logic trips on SIMURGH_SHIM_ACTIVE=1 without ever calling Resolve or Exec
// — i.e. the refusal happens before any work that could recurse, not after.
func TestRunRecursionGuardRefusesInProcess(t *testing.T) {
	resolveCalled := false
	execCalled := false

	opts := RunOptions{
		ShimName: "xcodebuild",
		Args:     []string{"-version"},
		Environ:  []string{"SIMURGH_SHIM_ACTIVE=1", "PATH=/usr/bin"},
		Resolver: Resolver{
			Getenv: func(string) string {
				resolveCalled = true
				return ""
			},
		},
		Exec: func(string, []string, []string, io.Reader, io.Writer, io.Writer) (int, error) {
			execCalled = true
			return 0, nil
		},
	}
	var stderr bytes.Buffer
	opts.Stderr = &stderr

	got := Run(opts)
	if got != ExitRecursionBlocked {
		t.Fatalf("Run() = %d, want ExitRecursionBlocked (%d)", got, ExitRecursionBlocked)
	}
	if resolveCalled {
		t.Error("Resolver was consulted despite the recursion guard tripping — the whole point is to refuse before any resolution work")
	}
	if execCalled {
		t.Error("Exec was called despite the recursion guard tripping — this would be the recursion itself")
	}
	if !strings.Contains(stderr.String(), ShimActiveEnv) {
		t.Errorf("stderr should mention %s, got: %s", ShimActiveEnv, stderr.String())
	}
}

// TestRunExecsRealBinaryUnmodifiedArgvAndExitCode proves passthrough:
// argv reaches the exec seam byte-for-byte, and Run returns exactly the
// exit code the (mocked) real binary reported — no injection, no
// modification.
func TestRunExecsRealBinaryUnmodifiedArgvAndExitCode(t *testing.T) {
	wantArgs := []string{"build", "-scheme", "Foo", "-derivedDataPath", "/should/not/be/touched"}
	const wantExit = 42

	var gotPath string
	var gotArgs []string
	var gotEnvHasGuard bool

	opts := RunOptions{
		ShimName: "xcodebuild",
		Args:     wantArgs,
		Environ:  []string{"PATH=/usr/bin", "HOME=/Users/tester"},
		Resolver: Resolver{
			Getenv: func(key string) string {
				if key == "DEVELOPER_DIR" {
					return "/fake/developer/dir"
				}
				return ""
			},
			Stat: func(string) (os.FileInfo, error) { return fakeFileInfo{}, nil },
		},
		Exec: func(path string, args []string, env []string, _ io.Reader, _, _ io.Writer) (int, error) {
			gotPath = path
			gotArgs = append([]string(nil), args...)
			for _, kv := range env {
				if kv == ShimActiveEnv+"=1" {
					gotEnvHasGuard = true
				}
			}
			return wantExit, nil
		},
	}

	got := Run(opts)
	if got != wantExit {
		t.Fatalf("Run() = %d, want %d", got, wantExit)
	}
	wantPath := filepath.Join("/fake/developer/dir", "usr", "bin", "xcodebuild")
	if gotPath != wantPath {
		t.Fatalf("resolved path passed to Exec = %q, want %q", gotPath, wantPath)
	}
	if len(gotArgs) != len(wantArgs) {
		t.Fatalf("Exec args = %v, want %v", gotArgs, wantArgs)
	}
	for i := range wantArgs {
		if gotArgs[i] != wantArgs[i] {
			t.Fatalf("Exec args[%d] = %q, want %q (argv must be forwarded unmodified)", i, gotArgs[i], wantArgs[i])
		}
	}
	if !gotEnvHasGuard {
		t.Fatal("real binary's env should carry SIMURGH_SHIM_ACTIVE=1 (recursion guard for anything it re-execs)")
	}
}

// TestRunWritesAuditLogEntryWithCorrectFields exercises the audit-log side
// channel end-to-end through Run (not AppendAuditLog directly): one
// invocation must produce exactly one log line with the right fields.
func TestRunWritesAuditLogEntryWithCorrectFields(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	fixedNow := time.Date(2026, 7, 15, 3, 4, 5, 0, time.UTC)

	opts := RunOptions{
		ShimName: "xcrun",
		Args:     []string{"simctl", "list", "devices"},
		Environ:  []string{"SIMURGH_SHIM_LOG=" + logPath},
		Resolver: Resolver{
			Getenv: func(key string) string {
				if key == "DEVELOPER_DIR" {
					return "/fake/dev"
				}
				return ""
			},
			Stat: func(string) (os.FileInfo, error) { return fakeFileInfo{}, nil },
		},
		Exec: func(string, []string, []string, io.Reader, io.Writer, io.Writer) (int, error) {
			return 7, nil
		},
		Now: func() time.Time { return fixedNow },
	}

	if got := Run(opts); got != 7 {
		t.Fatalf("Run() = %d, want 7", got)
	}

	lines := readLines(t, logPath)
	if len(lines) != 1 {
		t.Fatalf("got %d audit log lines, want exactly 1: %v", len(lines), lines)
	}
	var entry AuditEntry
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !entry.Timestamp.Equal(fixedNow) {
		t.Errorf("Timestamp = %v, want %v", entry.Timestamp, fixedNow)
	}
	if entry.Binary != "xcrun" {
		t.Errorf("Binary = %q, want xcrun", entry.Binary)
	}
	wantArgv := []string{"xcrun", "simctl", "list", "devices"}
	if len(entry.Argv) != len(wantArgv) {
		t.Fatalf("Argv = %v, want %v", entry.Argv, wantArgv)
	}
	for i := range wantArgv {
		if entry.Argv[i] != wantArgv[i] {
			t.Fatalf("Argv[%d] = %q, want %q", i, entry.Argv[i], wantArgv[i])
		}
	}
	wantReal := filepath.Join("/fake/dev", "usr", "bin", "xcrun")
	if entry.RealBinary != wantReal {
		t.Errorf("RealBinary = %q, want %q", entry.RealBinary, wantReal)
	}
	if entry.ExitCode != 7 {
		t.Errorf("ExitCode = %d, want 7", entry.ExitCode)
	}
	if entry.Mode != ModeAudit {
		t.Errorf("Mode = %q, want %q", entry.Mode, ModeAudit)
	}
}

// TestRunSkipsLoggingSilentlyWhenLogEnvUnset proves audit mode never fails
// the real invocation just because logging isn't wired up.
func TestRunSkipsLoggingSilentlyWhenLogEnvUnset(t *testing.T) {
	opts := RunOptions{
		ShimName: "xcodebuild",
		Environ:  nil, // no SIMURGH_SHIM_LOG
		Resolver: Resolver{
			Getenv: func(key string) string {
				if key == "DEVELOPER_DIR" {
					return "/fake/dev"
				}
				return ""
			},
			Stat: func(string) (os.FileInfo, error) { return fakeFileInfo{}, nil },
		},
		Exec: func(string, []string, []string, io.Reader, io.Writer, io.Writer) (int, error) {
			return 0, nil
		},
	}
	if got := Run(opts); got != 0 {
		t.Fatalf("Run() = %d, want 0", got)
	}
}

// TestRunResolutionFailureReturnsDistinctExitCode proves a broken/missing
// Xcode install produces a clearly-distinguishable exit code rather than
// silently pretending success or colliding with a real tool's own codes.
func TestRunResolutionFailureReturnsDistinctExitCode(t *testing.T) {
	opts := RunOptions{
		ShimName: "xcodebuild",
		Resolver: Resolver{
			Getenv: func(string) string { return "" },
			XcodeSelectPath: func() (string, error) {
				return "", os.ErrNotExist
			},
		},
		Exec: func(string, []string, []string, io.Reader, io.Writer, io.Writer) (int, error) {
			t.Fatal("Exec must not be called when resolution fails")
			return 0, nil
		},
	}
	var stderr bytes.Buffer
	opts.Stderr = &stderr
	got := Run(opts)
	if got != ExitResolutionFailed {
		t.Fatalf("Run() = %d, want ExitResolutionFailed (%d)", got, ExitResolutionFailed)
	}
	if stderr.Len() == 0 {
		t.Error("expected a stderr message explaining the resolution failure")
	}
}

// TestRunResolvesAndExecsRealBinaryUnmodifiedPassthrough is the real,
// non-mocked end-to-end proof: a real fake xcodebuild script (not a mocked
// ExecFunc) receives the exact argv Run was given, its stdout/exit code
// pass through untouched, and the audit log records the real resolved path.
func TestRunResolvesAndExecsRealBinaryUnmodifiedPassthrough(t *testing.T) {
	devDir := t.TempDir()
	script := `#!/bin/bash
set -euo pipefail
printf 'argv:%s\n' "$*"
exit 17
`
	realBin := writeFakeRealBinary(t, devDir, "xcodebuild", script)
	logPath := filepath.Join(t.TempDir(), "audit.log")

	var stdout, stderr bytes.Buffer
	opts := RunOptions{
		ShimName: "xcodebuild",
		Args:     []string{"-scheme", "Demo", "-configuration", "Debug"},
		Environ:  []string{"SIMURGH_SHIM_LOG=" + logPath},
		Resolver: Resolver{
			Getenv: func(key string) string {
				if key == "DEVELOPER_DIR" {
					return devDir
				}
				return ""
			},
		},
		Stdout: &stdout,
		Stderr: &stderr,
	}

	got := Run(opts)
	if got != 17 {
		t.Fatalf("Run() = %d, want 17 (the real script's exit code, passed through)\nstderr: %s", got, stderr.String())
	}
	wantOut := "argv:-scheme Demo -configuration Debug\n"
	if stdout.String() != wantOut {
		t.Fatalf("stdout = %q, want %q", stdout.String(), wantOut)
	}

	lines := readLines(t, logPath)
	if len(lines) != 1 {
		t.Fatalf("got %d audit lines, want 1: %v", len(lines), lines)
	}
	var entry AuditEntry
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if entry.RealBinary != realBin {
		t.Fatalf("RealBinary = %q, want %q", entry.RealBinary, realBin)
	}
	if entry.ExitCode != 17 {
		t.Fatalf("logged ExitCode = %d, want 17", entry.ExitCode)
	}
}

type fakeFileInfo struct{ os.FileInfo }

func (fakeFileInfo) Name() string { return "fake" }
