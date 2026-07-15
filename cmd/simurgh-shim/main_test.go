package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/integrations/pathshim"
)

func TestRunRejectsUnexpectedInvokedName(t *testing.T) {
	got := run([]string{"/some/path/simurgh-shim", "-version"}, nil)
	if got != 2 {
		t.Fatalf("run() = %d, want 2 for an unexpected argv[0] name", got)
	}
}

func TestRunRejectsEmptyArgv(t *testing.T) {
	if got := run(nil, nil); got != 2 {
		t.Fatalf("run(nil, ...) = %d, want 2", got)
	}
}

func TestRunDispatchesOnArgv0BasenameXcodebuild(t *testing.T) {
	devDir := t.TempDir()
	writeFakeXcode(t, devDir, "xcodebuild", "#!/bin/bash\nexit 0\n")
	got := run(
		[]string{"/session/bin/xcodebuild", "-version"},
		[]string{"DEVELOPER_DIR=" + devDir},
	)
	if got != 0 {
		t.Fatalf("run() = %d, want 0", got)
	}
}

func TestRunDispatchesOnArgv0BasenameXcrun(t *testing.T) {
	devDir := t.TempDir()
	writeFakeXcode(t, devDir, "xcrun", "#!/bin/bash\nexit 3\n")
	got := run(
		[]string{"/session/bin/xcrun", "simctl", "list"},
		[]string{"DEVELOPER_DIR=" + devDir},
	)
	if got != 3 {
		t.Fatalf("run() = %d, want 3", got)
	}
}

// TestShimBinaryRecursionGuardRefusesInsteadOfLooping is the mandatory
// real-subprocess proof: build the actual simurgh-shim binary, invoke it
// through a real "xcodebuild"-named symlink with SIMURGH_SHIM_ACTIVE=1
// already present in its environment, and assert it refuses immediately
// (distinct exit code, clear stderr message) rather than resolving/
// re-exec'ing itself. A bounded context timeout means a broken guard that
// actually recursed would make this test fail loudly (timeout) instead of
// hanging the suite forever.
func TestShimBinaryRecursionGuardRefusesInsteadOfLooping(t *testing.T) {
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "simurgh-shim")

	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build simurgh-shim: %v\n%s", err, out)
	}

	symDir := t.TempDir()
	symPath := filepath.Join(symDir, "xcodebuild")
	if err := os.Symlink(binPath, symPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// A DEVELOPER_DIR pointing at a real fake xcodebuild is deliberately
	// provided too, so that IF the recursion guard were broken, resolution
	// would succeed and the process would actually try to loop — making
	// this test a real proof the guard trips first, not just an artifact of
	// resolution failing for an unrelated reason.
	devDir := t.TempDir()
	writeFakeXcode(t, devDir, "xcodebuild", "#!/bin/bash\necho SHOULD_NOT_RUN\nexit 0\n")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, symPath, "-version")
	cmd.Env = []string{
		"SIMURGH_SHIM_ACTIVE=1",
		"DEVELOPER_DIR=" + devDir,
		"PATH=" + os.Getenv("PATH"),
	}
	out, err := cmd.CombinedOutput()

	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("shim binary did not return within the bounded timeout — looks like it recursed instead of refusing\noutput so far:\n%s", out)
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected the shim to exit non-zero via *exec.ExitError, got err=%v (type %T)\noutput:\n%s", err, err, out)
	}
	if got := exitErr.ExitCode(); got != pathshim.ExitRecursionBlocked {
		t.Fatalf("exit code = %d, want %d (ExitRecursionBlocked)\noutput:\n%s", got, pathshim.ExitRecursionBlocked, out)
	}
	if strings.Contains(string(out), "SHOULD_NOT_RUN") {
		t.Fatalf("the real (fake) xcodebuild ran — the recursion guard did not stop it\noutput:\n%s", out)
	}
	if !strings.Contains(string(out), "SIMURGH_SHIM_ACTIVE") {
		t.Fatalf("expected stderr to explain the SIMURGH_SHIM_ACTIVE refusal, got:\n%s", out)
	}
}

func writeFakeXcode(t *testing.T, devDir, name, script string) {
	t.Helper()
	dir := filepath.Join(devDir, "usr", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
}
