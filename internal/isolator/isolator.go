package isolator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
)

// LeaseDirs holds the on-disk isolation layout for one lease
// (ARCHITECTURE §2: DerivedData/ModuleCache/SwiftPM/Results/tmp).
type LeaseDirs struct {
	Root        string
	DerivedData string
	ModuleCache string
	SwiftPM     string
	Results     string
	Tmp         string
}

// defaultXcodeWritableMarkers are host path suffixes Simurgh must never emit
// as writable caches (R-WP4 private writable-cache isolation).
var defaultXcodeWritableMarkers = []string{
	filepath.Join("Library", "Developer", "Xcode", "DerivedData"),
	filepath.Join("Library", "Caches", "org.swift.swiftpm"),
}

// EnsureLeaseDir creates the five isolation subdirectories under
// filepath.Join(leasesRoot, leaseID) with mode 0700. Idempotent via MkdirAll.
// leasesRoot is injected — this package never resolves ~/.simurgh itself.
func EnsureLeaseDir(leasesRoot, leaseID string) (LeaseDirs, error) {
	root := filepath.Join(leasesRoot, leaseID)
	dirs := LeaseDirs{
		Root:        root,
		DerivedData: filepath.Join(root, "DerivedData"),
		ModuleCache: filepath.Join(root, "ModuleCache"),
		SwiftPM:     filepath.Join(root, "SwiftPM"),
		Results:     filepath.Join(root, "Results"),
		Tmp:         filepath.Join(root, "tmp"),
	}
	if err := ValidateWritableCachesUnderLeaseRoot(dirs); err != nil {
		return LeaseDirs{}, err
	}
	for _, d := range []string{dirs.DerivedData, dirs.ModuleCache, dirs.SwiftPM, dirs.Results, dirs.Tmp} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return LeaseDirs{}, fmt.Errorf("isolator: mkdir %s: %w", d, err)
		}
	}
	if err := ValidateWritableCachesUnderLeaseRoot(dirs); err != nil {
		return LeaseDirs{}, err
	}
	return dirs, nil
}

// ValidateWritableCachesUnderLeaseRoot asserts DerivedData/ModuleCache/SwiftPM/
// Results/tmp are strictly under dirs.Root and never equal a default Xcode path.
func ValidateWritableCachesUnderLeaseRoot(dirs LeaseDirs) error {
	if dirs.Root == "" {
		return fmt.Errorf("isolator: lease root is required")
	}
	root, err := resolvePathAllowMissing(dirs.Root)
	if err != nil {
		return fmt.Errorf("isolator: resolve lease root %q: %w", dirs.Root, err)
	}
	writable := map[string]string{
		"DerivedData": dirs.DerivedData,
		"ModuleCache": dirs.ModuleCache,
		"SwiftPM":     dirs.SwiftPM,
		"Results":     dirs.Results,
		"tmp":         dirs.Tmp,
	}
	for name, path := range writable {
		if path == "" {
			return fmt.Errorf("isolator: %s path is empty", name)
		}
		clean, err := resolvePathAllowMissing(path)
		if err != nil {
			return fmt.Errorf("isolator: resolve %s path %q: %w", name, path, err)
		}
		if clean == root {
			return fmt.Errorf("isolator: %s path %q must be a subdirectory of lease root %q", name, clean, root)
		}
		rel, err := filepath.Rel(root, clean)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("isolator: %s path %q escapes lease root %q", name, clean, root)
		}
		if touchesDefaultXcodeWritablePath(clean) {
			return fmt.Errorf("isolator: %s path %q must not use default Xcode writable caches", name, clean)
		}
	}
	return nil
}

// resolvePathAllowMissing resolves symlinks through the nearest existing
// ancestor, then appends not-yet-created descendants lexically.
func resolvePathAllowMissing(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current := filepath.Clean(abs)
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func touchesDefaultXcodeWritablePath(path string) bool {
	clean := filepath.Clean(path)
	for _, marker := range defaultXcodeWritableMarkers {
		if clean == marker || strings.HasSuffix(clean, string(os.PathSeparator)+marker) {
			return true
		}
		if idx := strings.Index(clean, string(os.PathSeparator)+marker+string(os.PathSeparator)); idx >= 0 {
			return true
		}
		if strings.HasSuffix(clean, string(os.PathSeparator)+marker) {
			return true
		}
	}
	if home := os.Getenv("HOME"); home != "" {
		banned := []string{
			filepath.Join(home, "Library", "Developer", "Xcode", "DerivedData"),
			filepath.Join(home, "Library", "Caches", "org.swift.swiftpm"),
		}
		for _, b := range banned {
			b = filepath.Clean(b)
			if clean == b || strings.HasPrefix(clean, b+string(os.PathSeparator)) {
				return true
			}
		}
	}
	return false
}

// BuildEnv is pure: computes IsolationEnv from dirs + lease identity.
//
// Flag/env matrix adapted from VibeChard's seven-resource isolation pattern
// (repo: VibeChard, license: Apache-2.0). Field names match ARCHITECTURE §7
// byte-for-byte. Does not emit -resultBundlePath (that is per-exec, via
// AllocateResultBundlePath). PATH shim is v0.1 — not built here.
//
// Panics are avoided: callers that need a hard guarantee should call
// ValidateWritableCachesUnderLeaseRoot first (EnsureLeaseDir already does).
func BuildEnv(dirs LeaseDirs, leaseID, udid string) broker.IsolationEnv {
	return broker.IsolationEnv{
		Env: map[string]string{
			"SIMURGH_LEASE_ID":          leaseID,
			"SIMURGH_UDID":              udid,
			"SIMURGH_DERIVED_DATA":      dirs.DerivedData,
			"CLANG_MODULE_CACHE_PATH":   dirs.ModuleCache,
			"SIMURGH_RESULT_BUNDLE_DIR": dirs.Results,
			"SIMURGH_SWIFTPM_DIR":       dirs.SwiftPM,
		},
		XcodebuildFlags: []string{
			"-derivedDataPath", dirs.DerivedData,
			"-clonedSourcePackagesDirPath", dirs.SwiftPM,
			"-destination", "platform=iOS Simulator,id=" + udid,
		},
		ShellSnippet: fmt.Sprintf(`eval "$(simurgh env %s)"`, leaseID),
	}
}

// AllocateResultBundlePath returns Results/run-<timestamp>.xcresult — a fresh
// path every call (ARCHITECTURE §7: -resultBundlePath requires a new path per
// run). now is injected for testability; nil uses time.Now.
func AllocateResultBundlePath(dirs LeaseDirs, now func() time.Time) string {
	if now == nil {
		now = time.Now
	}
	ts := now().UTC().Format("20060102T150405.000000000")
	return filepath.Join(dirs.Results, "run-"+ts+".xcresult")
}

// WriteLeaseSnapshot writes <dirs.Root>/lease.json — the lease record with
// computed IsolationEnv embedded — via temp file + rename (mirrors
// internal/store's atomic-write pattern; duplicated here rather than forcing
// a shared-package dependency).
func WriteLeaseSnapshot(dirs LeaseDirs, lease broker.Lease, env broker.IsolationEnv) error {
	snap := lease
	snap.Env = env
	raw, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("isolator: marshal lease.json: %w", err)
	}
	if err := os.MkdirAll(dirs.Root, 0o700); err != nil {
		return fmt.Errorf("isolator: mkdir lease root: %w", err)
	}
	path := filepath.Join(dirs.Root, "lease.json")
	tmp, err := os.CreateTemp(dirs.Root, "lease.json.*.tmp")
	if err != nil {
		return fmt.Errorf("isolator: create temp: %w", err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	ok = true
	return nil
}
