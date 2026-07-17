package pathshim

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFakeRealBinary creates <devDir>/usr/bin/<name> as an executable file
// and returns its absolute path. Mirrors the fakeXBMCP-style helper in
// internal/integrations/xcodebuildmcp/adapter_test.go.
func writeFakeRealBinary(t *testing.T, devDir, name, script string) string {
	t.Helper()
	dir := filepath.Join(devDir, "usr", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	return abs
}

func TestResolverResolvesViaDeveloperDirEnv(t *testing.T) {
	devDir := t.TempDir()
	want := writeFakeRealBinary(t, devDir, "xcodebuild", "#!/bin/bash\necho fake\n")

	r := Resolver{
		Getenv: func(key string) string {
			if key == "DEVELOPER_DIR" {
				return devDir
			}
			return ""
		},
		XcodeSelectPath: func() (string, error) {
			t.Fatal("XcodeSelectPath should not be called when DEVELOPER_DIR is set")
			return "", nil
		},
	}
	got, err := r.Resolve("xcodebuild")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != want {
		t.Fatalf("Resolve = %q, want %q", got, want)
	}
}

func TestResolverFallsBackToXcodeSelectWhenDeveloperDirUnset(t *testing.T) {
	devDir := t.TempDir()
	want := writeFakeRealBinary(t, devDir, "xcrun", "#!/bin/bash\necho fake\n")

	calledXcodeSelect := false
	r := Resolver{
		Getenv: func(string) string { return "" },
		XcodeSelectPath: func() (string, error) {
			calledXcodeSelect = true
			return devDir, nil
		},
	}
	got, err := r.Resolve("xcrun")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !calledXcodeSelect {
		t.Fatal("expected XcodeSelectPath fallback to be used")
	}
	if got != want {
		t.Fatalf("Resolve = %q, want %q", got, want)
	}
}

func TestResolverErrorsWhenRealBinaryMissing(t *testing.T) {
	devDir := t.TempDir() // no usr/bin/xcodebuild written
	emptySys := t.TempDir()
	r := Resolver{
		Getenv: func(key string) string {
			if key == "DEVELOPER_DIR" {
				return devDir
			}
			return ""
		},
		SystemBinDir: emptySys, // no /usr/bin fallback either
	}
	if _, err := r.Resolve("xcodebuild"); err == nil {
		t.Fatal("expected error when real binary is missing, got nil")
	}
}

func TestResolverFallsBackToSystemBinWhenMissingUnderDeveloperDir(t *testing.T) {
	// Xcode-beta (and some Xcode layouts) ship xcodebuild under
	// Developer/usr/bin but not xcrun — the OS entry point is /usr/bin/xcrun.
	devDir := t.TempDir()
	sysDir := t.TempDir()
	want := filepath.Join(sysDir, "xcrun")
	if err := os.WriteFile(want, []byte("#!/bin/bash\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	wantAbs, _ := filepath.Abs(want)

	r := Resolver{
		Getenv: func(key string) string {
			if key == "DEVELOPER_DIR" {
				return devDir
			}
			return ""
		},
		SystemBinDir: sysDir,
	}
	got, err := r.Resolve("xcrun")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != wantAbs {
		t.Fatalf("Resolve = %q, want system fallback %q", got, wantAbs)
	}
}

func TestResolverErrorsOnUnsupportedName(t *testing.T) {
	r := Resolver{Getenv: func(string) string { return "/tmp" }}
	if _, err := r.Resolve("rm"); err == nil {
		t.Fatal("expected error for unsupported shim name, got nil")
	}
}

func TestResolverErrorsWhenXcodeSelectFails(t *testing.T) {
	r := Resolver{
		Getenv: func(string) string { return "" },
		XcodeSelectPath: func() (string, error) {
			return "", os.ErrNotExist
		},
	}
	if _, err := r.Resolve("xcodebuild"); err == nil {
		t.Fatal("expected error when xcode-select fails, got nil")
	}
}

func TestDefaultResolverUsesRealXcodeSelectWhenAvailable(t *testing.T) {
	r := DefaultResolver()
	if r.Getenv == nil || r.XcodeSelectPath == nil || r.Stat == nil {
		t.Fatal("DefaultResolver left a nil field")
	}
	// Only assert it actually runs without panicking; whether Xcode is
	// installed on this host is exercised properly by the darwin_sim live test.
	_, _ = r.Resolve("xcodebuild")
}
