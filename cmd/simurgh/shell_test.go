package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RoshanDewmina/simurgh/internal/integrations/pathshim"
)

func TestLeaseRootFromEnv(t *testing.T) {
	root, err := leaseRootFromEnv(map[string]string{
		"SIMURGH_DERIVED_DATA": "/tmp/leases/lease-1/DerivedData",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/tmp/leases/lease-1")
	if root != want {
		t.Fatalf("got %q, want %q", root, want)
	}
}

func TestLeaseRootFromEnvMissing(t *testing.T) {
	if _, err := leaseRootFromEnv(map[string]string{}); err == nil {
		t.Fatal("expected error for missing SIMURGH_DERIVED_DATA")
	}
}

func TestRunShellHelp(t *testing.T) {
	if got := runShell([]string{"--help"}); got != 0 {
		t.Fatalf("runShell(--help) = %d, want 0", got)
	}
}

func TestRunShellRequiresLeaseID(t *testing.T) {
	if got := runShell(nil); got != 3 {
		t.Fatalf("runShell() = %d, want 3", got)
	}
}

func TestResolveShimBinaryBesideExecutable(t *testing.T) {
	dir := t.TempDir()
	shim := filepath.Join(dir, "simurgh-shim")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	restore := swapShimDiscovery(
		func() (string, error) { return filepath.Join(dir, "simurgh"), nil },
		func(string) (string, error) { return "", exec.ErrNotFound },
	)
	defer restore()

	got, err := resolveShimBinary()
	if err != nil {
		t.Fatalf("resolveShimBinary: %v", err)
	}
	if got != shim {
		t.Fatalf("got %q, want beside-executable %q", got, shim)
	}
}

func TestResolveShimBinaryFallsBackToPATH(t *testing.T) {
	dir := t.TempDir()
	shim := filepath.Join(dir, "simurgh-shim")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	restore := swapShimDiscovery(
		func() (string, error) { return "", errors.New("no exe") },
		func(file string) (string, error) {
			if file != "simurgh-shim" {
				t.Fatalf("LookPath(%q)", file)
			}
			return shim, nil
		},
	)
	defer restore()

	got, err := resolveShimBinary()
	if err != nil {
		t.Fatalf("resolveShimBinary: %v", err)
	}
	if got != shim {
		t.Fatalf("got %q, want PATH fallback %q", got, shim)
	}
}

func TestResolveShimBinaryMissing(t *testing.T) {
	restore := swapShimDiscovery(
		func() (string, error) { return filepath.Join(t.TempDir(), "simurgh"), nil },
		func(string) (string, error) { return "", exec.ErrNotFound },
	)
	defer restore()

	_, err := resolveShimBinary()
	if err == nil {
		t.Fatal("expected error when simurgh-shim is missing")
	}
	if !strings.Contains(err.Error(), "simurgh-shim not found") {
		t.Fatalf("error = %v, want discovery hint", err)
	}
}

func TestPrepareManagedShellAuditsBareXcodebuildViaProcess(t *testing.T) {
	root := t.TempDir()
	shimBin := buildTestShimBinary(t, root)
	devDir := filepath.Join(root, "FakeXcode.app/Contents/Developer")
	writeFakeDeveloperTool(t, devDir, "xcodebuild", "#!/bin/bash\necho fake-xcodebuild\nexit 0\n")
	writeFakeDeveloperTool(t, devDir, "xcrun", "#!/bin/bash\necho fake-xcrun\nexit 0\n")

	leaseRoot := filepath.Join(root, "lease-1")
	leaseEnv := map[string]string{
		"SIMURGH_DERIVED_DATA": filepath.Join(leaseRoot, "DerivedData"),
		"SIMURGH_UDID":         "TEST-UDID",
	}
	sess, err := prepareManagedShell(leaseEnv, []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + root,
		"DEVELOPER_DIR=" + devDir,
	}, shimBin)
	if err != nil {
		t.Fatalf("prepareManagedShell: %v", err)
	}

	for _, name := range []string{"xcodebuild", "xcrun"} {
		// Absolute symlink path — exec.Command(name) would resolve against
		// the test process PATH, not sess.Env.
		cmd := exec.Command(filepath.Join(sess.Paths.BinDir, name), "-version")
		cmd.Env = sess.Env
		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			t.Fatalf("%s -version: %v\n%s", name, runErr, out)
		}
		if !strings.Contains(string(out), "fake-"+name) {
			t.Fatalf("%s output = %q, want fake binary", name, out)
		}
	}

	raw, err := os.ReadFile(sess.Paths.AuditLog)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("audit lines = %d, want 2:\n%s", len(lines), raw)
	}
	seen := map[string]bool{}
	for _, line := range lines {
		var entry pathshim.AuditEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("audit json: %v\n%s", err, line)
		}
		if entry.Mode != pathshim.ModeAudit {
			t.Fatalf("mode = %q, want %q", entry.Mode, pathshim.ModeAudit)
		}
		if entry.ExitCode != 0 {
			t.Fatalf("exitCode = %d, want 0", entry.ExitCode)
		}
		seen[entry.Binary] = true
	}
	if !seen["xcodebuild"] || !seen["xcrun"] {
		t.Fatalf("audit binaries = %v, want xcodebuild and xcrun", seen)
	}
}

func TestPrepareManagedShellReplacesInheritedDuplicateKeys(t *testing.T) {
	root := t.TempDir()
	shimBin := buildTestShimBinary(t, root)
	devDir := filepath.Join(root, "FakeXcode.app/Contents/Developer")
	writeFakeDeveloperTool(t, devDir, "xcodebuild", "#!/bin/bash\necho fake-xcodebuild\nexit 0\n")
	writeFakeDeveloperTool(t, devDir, "xcrun", "#!/bin/bash\necho fake-xcrun\nexit 0\n")

	leaseRoot := filepath.Join(root, "lease-1")
	wantDD := filepath.Join(leaseRoot, "DerivedData")
	leaseEnv := map[string]string{
		"SIMURGH_DERIVED_DATA": wantDD,
		"SIMURGH_UDID":         "LEASE-UDID",
	}
	parentWrongLog := filepath.Join(root, "parent-wrong-audit.log")
	sess, err := prepareManagedShell(leaseEnv, []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + root,
		"DEVELOPER_DIR=" + devDir,
		"SIMURGH_UDID=PARENT-UDID",
		"SIMURGH_DERIVED_DATA=/tmp/wrong/DerivedData",
		"SIMURGH_SHIM_LOG=" + parentWrongLog,
	}, shimBin)
	if err != nil {
		t.Fatalf("prepareManagedShell: %v", err)
	}

	if got := pathshim.LookupEnv(sess.Env, "SIMURGH_UDID"); got != "LEASE-UDID" {
		t.Fatalf("SIMURGH_UDID = %q, want LEASE-UDID", got)
	}
	if got := pathshim.LookupEnv(sess.Env, "SIMURGH_DERIVED_DATA"); got != wantDD {
		t.Fatalf("SIMURGH_DERIVED_DATA = %q, want %q", got, wantDD)
	}
	if got := pathshim.LookupEnv(sess.Env, pathshim.ShimLogEnv); got != sess.Paths.AuditLog {
		t.Fatalf("%s = %q, want lease audit path %q", pathshim.ShimLogEnv, got, sess.Paths.AuditLog)
	}
	if countEnvKey(sess.Env, pathshim.ShimLogEnv) != 1 {
		t.Fatalf("expected exactly one %s entry, got %d", pathshim.ShimLogEnv, countEnvKey(sess.Env, pathshim.ShimLogEnv))
	}
	if countEnvKey(sess.Env, "SIMURGH_UDID") != 1 {
		t.Fatalf("expected exactly one SIMURGH_UDID entry, got %d", countEnvKey(sess.Env, "SIMURGH_UDID"))
	}

	cmd := exec.Command(filepath.Join(sess.Paths.BinDir, "xcodebuild"), "-version")
	cmd.Env = sess.Env
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("xcodebuild: %v\n%s", runErr, out)
	}
	if _, err := os.Stat(parentWrongLog); !os.IsNotExist(err) {
		t.Fatalf("parent SIMURGH_SHIM_LOG was written (%v); lease audit path must win", err)
	}
	raw, err := os.ReadFile(sess.Paths.AuditLog)
	if err != nil {
		t.Fatalf("read lease audit log: %v", err)
	}
	if !strings.Contains(string(raw), `"binary":"xcodebuild"`) {
		t.Fatalf("lease audit log missing xcodebuild entry:\n%s", raw)
	}
}

func TestShellPrepFix(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{errors.New("install PATH shims: pathshim: refuse to replace /tmp/x — not a symlink"), "remove non-symlink files under the lease pathshim/bin directory and retry: simurgh shell <lease-id>"},
		{errors.New("lease env missing SIMURGH_DERIVED_DATA"), "wait for acquire to finish; lease must be ACTIVE"},
		{errors.New("install PATH shims: pathshim: shim binary \"/missing\": no such file"), "go build -o simurgh-shim ./cmd/simurgh-shim"},
	}
	for _, tc := range cases {
		if got := shellPrepFix(tc.err); got != tc.want {
			t.Fatalf("shellPrepFix(%q) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestPrintShellShimNotFoundErrorTriple(t *testing.T) {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	printErrorTriple("SHIM_NOT_FOUND", "simurgh-shim not found beside simurgh or on PATH — build with: go build -o simurgh-shim ./cmd/simurgh-shim", "go build -o simurgh-shim ./cmd/simurgh-shim", false)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = old
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "SHIM_NOT_FOUND:") {
		t.Fatalf("missing code:\n%s", got)
	}
	if !strings.Contains(got, "fix: go build -o simurgh-shim ./cmd/simurgh-shim") {
		t.Fatalf("missing fix:\n%s", got)
	}
}

func TestPrintShellPrepInternalErrorTriple(t *testing.T) {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	prepErr := errors.New("install PATH shims: pathshim: refuse to replace /tmp/x — not a symlink")
	printErrorTriple("INTERNAL", prepErr.Error(), shellPrepFix(prepErr), false)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = old
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.HasPrefix(got, "INTERNAL:") {
		t.Fatalf("missing INTERNAL code:\n%s", got)
	}
	if !strings.Contains(got, "fix: remove non-symlink files under the lease pathshim/bin directory") {
		t.Fatalf("missing actionable fix:\n%s", got)
	}
}

func countEnvKey(environ []string, key string) int {
	prefix := key + "="
	n := 0
	for _, kv := range environ {
		if strings.HasPrefix(kv, prefix) {
			n++
		}
	}
	return n
}

func swapShimDiscovery(exe func() (string, error), look func(string) (string, error)) func() {
	oldExe, oldLook := shimExecutable, shimLookPath
	shimExecutable, shimLookPath = exe, look
	return func() {
		shimExecutable, shimLookPath = oldExe, oldLook
	}
}

func buildTestShimBinary(t *testing.T, root string) string {
	t.Helper()
	out := filepath.Join(root, "simurgh-shim")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/simurgh-shim")
	cmd.Dir = filepath.Join("..", "..")
	// When tests run as `go test ./cmd/simurgh`, module root is two levels up
	// from the package dir — but go test sets cwd to the package directory.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// Prefer walking up to go.mod so this works from cmd/simurgh or module root.
	modRoot := wd
	for {
		if _, statErr := os.Stat(filepath.Join(modRoot, "go.mod")); statErr == nil {
			break
		}
		parent := filepath.Dir(modRoot)
		if parent == modRoot {
			t.Fatal("go.mod not found above " + wd)
		}
		modRoot = parent
	}
	cmd.Dir = modRoot
	if outBytes, buildErr := cmd.CombinedOutput(); buildErr != nil {
		t.Fatalf("build simurgh-shim: %v\n%s", buildErr, outBytes)
	}
	return out
}

func writeFakeDeveloperTool(t *testing.T, developerDir, name, script string) {
	t.Helper()
	dir := filepath.Join(developerDir, "usr", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
