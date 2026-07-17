package pathshim

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallSessionShimsCreatesSymlinks(t *testing.T) {
	root := t.TempDir()
	shim := filepath.Join(root, "simurgh-shim")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	paths := SessionDir(filepath.Join(root, "lease-1"))
	if err := InstallSessionShims(paths, shim); err != nil {
		t.Fatalf("InstallSessionShims: %v", err)
	}
	for _, name := range []string{"xcodebuild", "xcrun"} {
		link := filepath.Join(paths.BinDir, name)
		target, err := os.Readlink(link)
		if err != nil {
			t.Fatalf("readlink %s: %v", name, err)
		}
		want, _ := filepath.Abs(shim)
		if target != want {
			t.Fatalf("%s -> %q, want %q", name, target, want)
		}
	}
}

func TestInstallSessionShimsRequiresExistingBinary(t *testing.T) {
	root := t.TempDir()
	paths := SessionDir(filepath.Join(root, "lease-1"))
	missing := filepath.Join(root, "no-such-simurgh-shim")
	err := InstallSessionShims(paths, missing)
	if err == nil {
		t.Fatal("expected error for missing shim binary")
	}
	if !strings.Contains(err.Error(), "shim binary") {
		t.Fatalf("error = %v, want shim binary mention", err)
	}
}

func TestInstallSessionShimsRefusesNonSymlinkCollision(t *testing.T) {
	root := t.TempDir()
	shim := filepath.Join(root, "simurgh-shim")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	paths := SessionDir(filepath.Join(root, "lease-1"))
	if err := os.MkdirAll(paths.BinDir, 0o700); err != nil {
		t.Fatal(err)
	}
	collision := filepath.Join(paths.BinDir, "xcodebuild")
	if err := os.WriteFile(collision, []byte("not-a-symlink"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := InstallSessionShims(paths, shim)
	if err == nil {
		t.Fatal("expected error when replacing a non-symlink")
	}
	if !strings.Contains(err.Error(), "not a symlink") {
		t.Fatalf("error = %v, want not-a-symlink mention", err)
	}
	raw, readErr := os.ReadFile(collision)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(raw) != "not-a-symlink" {
		t.Fatalf("regular file was modified: %q", raw)
	}
}

func TestInstallSessionShimsReplacesExistingSymlink(t *testing.T) {
	root := t.TempDir()
	oldShim := filepath.Join(root, "old-shim")
	newShim := filepath.Join(root, "new-shim")
	for _, p := range []string{oldShim, newShim} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	paths := SessionDir(filepath.Join(root, "lease-1"))
	if err := InstallSessionShims(paths, oldShim); err != nil {
		t.Fatal(err)
	}
	if err := InstallSessionShims(paths, newShim); err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs(newShim)
	for _, name := range []string{"xcodebuild", "xcrun"} {
		got, err := os.Readlink(filepath.Join(paths.BinDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s -> %q, want %q", name, got, want)
		}
	}
}

func TestPrependPathToEnv(t *testing.T) {
	got := PrependPathToEnv([]string{"HOME=/tmp", "PATH=/usr/bin"}, "/shim/bin")
	wantPrefix := "PATH=/shim/bin" + string(os.PathListSeparator) + "/usr/bin"
	var pathKV string
	for _, kv := range got {
		if strings.HasPrefix(kv, "PATH=") {
			pathKV = kv
		}
	}
	if pathKV != wantPrefix {
		t.Fatalf("PATH entry = %q, want %q", pathKV, wantPrefix)
	}
}

func TestPrependPathToEnvAddsWhenMissing(t *testing.T) {
	got := PrependPathToEnv([]string{"HOME=/tmp"}, "/shim/bin")
	if len(got) != 2 || got[1] != "PATH=/shim/bin" {
		t.Fatalf("got %v, want PATH=/shim/bin appended", got)
	}
}

// TestInstallSessionShimsProcessAuditFlow builds the real simurgh-shim binary,
// installs session symlinks, invokes bare xcodebuild/xcrun through PATH, and
// asserts stage-1 audit JSONL entries. This is the process-level coverage for
// the clean-checkout install/discovery path without requiring a live lease.
func TestInstallSessionShimsProcessAuditFlow(t *testing.T) {
	root := t.TempDir()
	shimBin := filepath.Join(root, "simurgh-shim")
	modRoot := findModuleRoot(t)
	build := exec.Command("go", "build", "-o", shimBin, "./cmd/simurgh-shim")
	build.Dir = modRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build simurgh-shim: %v\n%s", err, out)
	}

	devDir := filepath.Join(root, "Developer")
	writeFakeTool(t, devDir, "xcodebuild", "#!/bin/bash\necho ok-xcodebuild\nexit 0\n")
	writeFakeTool(t, devDir, "xcrun", "#!/bin/bash\necho ok-xcrun\nexit 0\n")

	paths := SessionDir(filepath.Join(root, "lease-1"))
	if err := InstallSessionShims(paths, shimBin); err != nil {
		t.Fatalf("InstallSessionShims: %v", err)
	}

	env := PrependPathToEnv([]string{
		"PATH=/usr/bin:/bin",
		"DEVELOPER_DIR=" + devDir,
		ShimLogEnv + "=" + paths.AuditLog,
	}, paths.BinDir)

	for _, name := range []string{"xcodebuild", "xcrun"} {
		// Invoke the session symlink by absolute path: exec.Command(name)
		// resolves against the test process PATH, not cmd.Env.
		cmd := exec.Command(filepath.Join(paths.BinDir, name), "-version")
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
		if !strings.Contains(string(out), "ok-"+name) {
			t.Fatalf("%s output = %q", name, out)
		}
	}

	raw, err := os.ReadFile(paths.AuditLog)
	if err != nil {
		t.Fatalf("audit log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 audit lines, got %d:\n%s", len(lines), raw)
	}
	for _, line := range lines {
		var entry AuditEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("json: %v\n%s", err, line)
		}
		if entry.Mode != ModeAudit {
			t.Fatalf("mode=%q", entry.Mode)
		}
	}
}

func writeFakeTool(t *testing.T, developerDir, name, script string) {
	t.Helper()
	dir := filepath.Join(developerDir, "usr", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func findModuleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
