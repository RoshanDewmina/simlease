package isolator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
)

func TestEnsureLeaseDirCreatesFiveSubdirs(t *testing.T) {
	root := t.TempDir()
	dirs, err := EnsureLeaseDir(root, "lease-86dd")
	if err != nil {
		t.Fatalf("EnsureLeaseDir: %v", err)
	}

	wantRoot := filepath.Join(root, "lease-86dd")
	if dirs.Root != wantRoot {
		t.Fatalf("Root = %q want %q", dirs.Root, wantRoot)
	}

	checks := map[string]string{
		"DerivedData": dirs.DerivedData,
		"ModuleCache": dirs.ModuleCache,
		"SwiftPM":     dirs.SwiftPM,
		"Results":     dirs.Results,
		"tmp":         dirs.Tmp,
	}
	for name, path := range checks {
		want := filepath.Join(wantRoot, name)
		if path != want {
			t.Fatalf("%s = %q want %q", name, path, want)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", name)
		}
		if info.Mode().Perm()&0o700 != 0o700 {
			t.Fatalf("%s mode = %o want owner rwx", name, info.Mode().Perm())
		}
	}

	// Idempotent second call.
	dirs2, err := EnsureLeaseDir(root, "lease-86dd")
	if err != nil {
		t.Fatalf("EnsureLeaseDir second: %v", err)
	}
	if dirs2 != dirs {
		t.Fatalf("second EnsureLeaseDir = %+v want %+v", dirs2, dirs)
	}
}

func TestBuildEnvGoldenFields(t *testing.T) {
	dirs := LeaseDirs{
		Root:        "/tmp/leases/lease-86dd",
		DerivedData: "/tmp/leases/lease-86dd/DerivedData",
		ModuleCache: "/tmp/leases/lease-86dd/ModuleCache",
		SwiftPM:     "/tmp/leases/lease-86dd/SwiftPM",
		Results:     "/tmp/leases/lease-86dd/Results",
		Tmp:         "/tmp/leases/lease-86dd/tmp",
	}
	udid := "4E2A0000-0000-0000-0000-00000000009F"
	env := BuildEnv(dirs, "lease-86dd", udid)

	wantEnv := map[string]string{
		"SIMURGH_LEASE_ID":          "lease-86dd",
		"SIMURGH_UDID":              udid,
		"SIMURGH_DERIVED_DATA":      dirs.DerivedData,
		"CLANG_MODULE_CACHE_PATH":   dirs.ModuleCache,
		"SIMURGH_RESULT_BUNDLE_DIR": dirs.Results,
		"SIMURGH_SWIFTPM_DIR":       dirs.SwiftPM,
	}
	if len(env.Env) != len(wantEnv) {
		t.Fatalf("env len = %d want %d: %#v", len(env.Env), len(wantEnv), env.Env)
	}
	for k, v := range wantEnv {
		if got := env.Env[k]; got != v {
			t.Errorf("env[%q] = %q want %q", k, got, v)
		}
	}

	wantFlags := []string{
		"-derivedDataPath", dirs.DerivedData,
		"-clonedSourcePackagesDirPath", dirs.SwiftPM,
		"-destination", "platform=iOS Simulator,id=" + udid,
	}
	if len(env.XcodebuildFlags) != len(wantFlags) {
		t.Fatalf("flags = %#v want %#v", env.XcodebuildFlags, wantFlags)
	}
	for i := range wantFlags {
		if env.XcodebuildFlags[i] != wantFlags[i] {
			t.Errorf("flag[%d] = %q want %q", i, env.XcodebuildFlags[i], wantFlags[i])
		}
	}

	wantSnippet := `eval "$(simurgh env lease-86dd)"`
	if env.ShellSnippet != wantSnippet {
		t.Fatalf("ShellSnippet = %q want %q", env.ShellSnippet, wantSnippet)
	}
}

func TestWritableCachesStayUnderLeaseRoot(t *testing.T) {
	root := t.TempDir()
	dirs, err := EnsureLeaseDir(root, "lease-cache")
	if err != nil {
		t.Fatalf("EnsureLeaseDir: %v", err)
	}
	if err := ValidateWritableCachesUnderLeaseRoot(dirs); err != nil {
		t.Fatalf("ValidateWritableCachesUnderLeaseRoot: %v", err)
	}

	env := BuildEnv(dirs, "lease-cache", "UDID-CACHE")
	paths := []string{
		env.Env["SIMURGH_DERIVED_DATA"],
		env.Env["CLANG_MODULE_CACHE_PATH"],
		env.Env["SIMURGH_SWIFTPM_DIR"],
		env.Env["SIMURGH_RESULT_BUNDLE_DIR"],
		dirs.Tmp,
	}
	for _, p := range paths {
		rel, err := filepath.Rel(dirs.Root, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			t.Fatalf("writable path %q escapes lease root %q", p, dirs.Root)
		}
		if home := os.Getenv("HOME"); home != "" {
			banned := filepath.Join(home, "Library", "Developer", "Xcode", "DerivedData")
			if p == banned || strings.HasPrefix(p, banned+string(os.PathSeparator)) {
				t.Fatalf("path %q uses default Xcode DerivedData", p)
			}
		}
	}
	// Flags must point at the same lease-scoped DerivedData/SwiftPM.
	if got := env.XcodebuildFlags; len(got) < 4 || got[1] != dirs.DerivedData || got[3] != dirs.SwiftPM {
		t.Fatalf("xcodebuild flags not lease-scoped: %#v", env.XcodebuildFlags)
	}

	// Escaping path must be rejected.
	bad := dirs
	bad.DerivedData = filepath.Join(root, "..", "escape-dd")
	if err := ValidateWritableCachesUnderLeaseRoot(bad); err == nil {
		t.Fatal("expected escape rejection")
	}
}

func TestWritableCachesRejectDefaultXcodePaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "lease")
	base := LeaseDirs{
		Root:        root,
		DerivedData: filepath.Join(root, "DerivedData"),
		ModuleCache: filepath.Join(root, "ModuleCache"),
		SwiftPM:     filepath.Join(root, "SwiftPM"),
		Results:     filepath.Join(root, "Results"),
		Tmp:         filepath.Join(root, "tmp"),
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	derived := base
	derived.DerivedData = filepath.Join(home, "Library", "Developer", "Xcode", "DerivedData")
	if err := ValidateWritableCachesUnderLeaseRoot(derived); err == nil {
		t.Fatal("default Xcode DerivedData must be rejected")
	}

	swiftPM := base
	swiftPM.SwiftPM = filepath.Join(home, "Library", "Caches", "org.swift.swiftpm")
	if err := ValidateWritableCachesUnderLeaseRoot(swiftPM); err == nil {
		t.Fatal("default SwiftPM cache must be rejected")
	}
}

func TestWritableCachesRejectSymlinkEscape(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "lease")
	outside := filepath.Join(parent, "outside")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	escapeLink := filepath.Join(root, "linked-cache")
	if err := os.Symlink(outside, escapeLink); err != nil {
		t.Fatal(err)
	}
	dirs := LeaseDirs{
		Root:        root,
		DerivedData: filepath.Join(escapeLink, "not-created-yet", "DerivedData"),
		ModuleCache: filepath.Join(root, "ModuleCache"),
		SwiftPM:     filepath.Join(root, "SwiftPM"),
		Results:     filepath.Join(root, "Results"),
		Tmp:         filepath.Join(root, "tmp"),
	}
	if err := ValidateWritableCachesUnderLeaseRoot(dirs); err == nil {
		t.Fatal("symlink escape through nearest existing parent must be rejected")
	}
	if _, err := os.Stat(filepath.Join(outside, "not-created-yet")); !os.IsNotExist(err) {
		t.Fatalf("validation must not create outside descendants: %v", err)
	}
}

func TestWritableCachesResolveSymlinkedRoot(t *testing.T) {
	parent := t.TempDir()
	realRoot := filepath.Join(parent, "real-lease")
	if err := os.MkdirAll(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	rootLink := filepath.Join(parent, "lease-link")
	if err := os.Symlink(realRoot, rootLink); err != nil {
		t.Fatal(err)
	}
	dirs := LeaseDirs{
		Root:        rootLink,
		DerivedData: filepath.Join(rootLink, "DerivedData"),
		ModuleCache: filepath.Join(rootLink, "ModuleCache"),
		SwiftPM:     filepath.Join(rootLink, "SwiftPM"),
		Results:     filepath.Join(rootLink, "Results"),
		Tmp:         filepath.Join(rootLink, "tmp"),
	}
	if err := ValidateWritableCachesUnderLeaseRoot(dirs); err != nil {
		t.Fatalf("symlinked root with confined descendants rejected: %v", err)
	}
}

func TestAllocateResultBundlePathAdvances(t *testing.T) {
	dirs := LeaseDirs{Results: "/tmp/leases/lease-x/Results"}
	t0 := time.Unix(1_700_000_000, 123)
	t1 := t0.Add(time.Nanosecond)

	p0 := AllocateResultBundlePath(dirs, func() time.Time { return t0 })
	p1 := AllocateResultBundlePath(dirs, func() time.Time { return t1 })
	if p0 == p1 {
		t.Fatalf("paths equal: %q", p0)
	}
	if filepath.Dir(p0) != dirs.Results || filepath.Ext(p0) != ".xcresult" {
		t.Fatalf("unexpected path shape: %q", p0)
	}
	if filepath.Base(p0)[:4] != "run-" {
		t.Fatalf("basename = %q want run- prefix", filepath.Base(p0))
	}
}

func TestWriteLeaseSnapshotRoundTrip(t *testing.T) {
	root := t.TempDir()
	dirs, err := EnsureLeaseDir(root, "lease-rt")
	if err != nil {
		t.Fatal(err)
	}
	lease := broker.Lease{
		ID:    "lease-rt",
		State: broker.LeaseActive,
		Spec:  broker.DeviceSpec{Platform: "iOS", Model: "iPhone 17 Pro"},
		Device: &broker.Device{
			UDID:   "UDID-1",
			Name:   "simurgh-clone[lease-rt:iPhone 17 Pro:27.0]",
			Booted: true,
		},
		Owner: broker.Owner{PID: 42, Agent: "test"},
	}
	env := BuildEnv(dirs, lease.ID, lease.Device.UDID)
	if err := WriteLeaseSnapshot(dirs, lease, env); err != nil {
		t.Fatalf("WriteLeaseSnapshot: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dirs.Root, "lease.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got broker.Lease
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}
	if got.ID != lease.ID || got.State != lease.State {
		t.Fatalf("lease identity = %+v", got)
	}
	if got.Device == nil || got.Device.UDID != "UDID-1" {
		t.Fatalf("device = %+v", got.Device)
	}
	if got.Env.Env["SIMURGH_LEASE_ID"] != "lease-rt" {
		t.Fatalf("env not embedded: %+v", got.Env)
	}
	if len(got.Env.XcodebuildFlags) == 0 || got.Env.ShellSnippet == "" {
		t.Fatalf("flags/snippet missing: %+v", got.Env)
	}
}
