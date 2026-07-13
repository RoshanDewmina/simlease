package isolator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	for _, d := range []string{dirs.DerivedData, dirs.ModuleCache, dirs.SwiftPM, dirs.Results, dirs.Tmp} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return LeaseDirs{}, fmt.Errorf("isolator: mkdir %s: %w", d, err)
		}
	}
	return dirs, nil
}

// BuildEnv is pure: computes IsolationEnv from dirs + lease identity.
//
// Flag/env matrix adapted from VibeChard's seven-resource isolation pattern
// (repo: VibeChard, license: Apache-2.0). Field names match ARCHITECTURE §7
// byte-for-byte. Does not emit -resultBundlePath (that is per-exec, via
// AllocateResultBundlePath). PATH shim is v0.1 — not built here.
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
