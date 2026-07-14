package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

// warmDD implements same-path DerivedData seeding: an opt-in-by-default,
// best-effort speedup for the common case of repeatedly building the SAME
// project (same absolute -project/-workspace path + -scheme) across
// separate leases — e.g. one agent iterating acquire/build/release many
// times against one checkout. This is deliberately narrower than warm
// SIMULATOR templates (internal/provisioner/warm.go): it is keyed on the
// exact absolute project path, so it never applies across different git
// worktrees (each has a distinct path) and can't cross-contaminate one
// checkout's build cache into another's.
//
// Mechanism: after a successful xcodebuild, the lease's now-populated
// DerivedData is clonefile-copied (`cp -Rc`, APFS copy-on-write — no data
// duplication, near-instant even for large trees) into a shared warm-dd
// cache keyed by project+scheme. The next lease building that same
// project seeds its (empty) DerivedData from that cache before building,
// so xcodebuild's own incremental system only recompiles what actually
// changed since the last build, instead of starting from nothing.
//
// Safety: xcodebuild's own incremental build system is what actually
// decides what to recompile, by checking real source mtimes against its
// build graph — seeding never bypasses that, it only avoids paying for a
// cold start. Every step here is best-effort: any failure (cp missing,
// clonefile unsupported on this volume, permissions) silently falls back
// to the pre-existing cold-build behavior. This never blocks or fails the
// actual build.

const warmDDDirName = "warm-dd"

func warmDDKey(projectPath, scheme string) string {
	h := sha256.Sum256([]byte(projectPath + "|" + scheme))
	return hex.EncodeToString(h[:])[:16]
}

func warmDDCachePath(home, key string) string {
	return filepath.Join(home, warmDDDirName, key)
}

// extractProjectScheme pulls -project/-workspace and -scheme values out of
// an xcodebuild argv. Returns ("", "") if neither -project nor -workspace
// is present — nothing to key a same-path cache on.
func extractProjectScheme(argv []string) (projectPath, scheme string) {
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "-project", "-workspace":
			if i+1 < len(argv) {
				abs, err := filepath.Abs(argv[i+1])
				if err == nil {
					projectPath = abs
				} else {
					projectPath = argv[i+1]
				}
			}
			i++
		case "-scheme":
			if i+1 < len(argv) {
				scheme = argv[i+1]
			}
			i++
		}
	}
	return projectPath, scheme
}

// isXcodebuildQueryOnly reports whether argv is a pure introspection call
// (-showBuildSettings, -list, -version, -showsdks) that never populates
// DerivedData — seeding/refreshing around these would be wasted work, not
// incorrect, but pointless.
func isXcodebuildQueryOnly(argv []string) bool {
	for _, a := range argv[1:] {
		switch a {
		case "-showBuildSettings", "-list", "-version", "-showsdks":
			return true
		}
	}
	return false
}

// seedDerivedDataFromWarm is the legacy same-path helper kept for existing
// unit coverage. R-WP4 production seeding uses SeedWarmDerivedData with the
// formal compatibility key (see warm_seed.go).
func seedDerivedDataFromWarm(warmPath, leaseDD string) {
	if warmPath == "" || leaseDD == "" {
		return
	}
	if _, err := os.Stat(warmPath); err != nil {
		return
	}
	entries, err := os.ReadDir(leaseDD)
	if err != nil || len(entries) > 0 {
		return
	}
	if err := os.Remove(leaseDD); err != nil {
		return
	}
	_ = exec.Command("cp", "-Rc", warmPath, leaseDD).Run()
	// Best-effort: if cp failed partway (e.g. clonefile unsupported),
	// isolator already created leaseDD with 0700 before this ran, so a
	// missing dir here would break the build — restore an empty one.
	if _, err := os.Stat(leaseDD); err != nil {
		_ = os.MkdirAll(leaseDD, 0o700)
	}
}

// refreshWarmFromDerivedData is the legacy same-path publisher kept for
// existing unit coverage. Production refresh uses PublishWarmSeed.
func refreshWarmFromDerivedData(leaseDD, warmPath string) {
	if leaseDD == "" || warmPath == "" {
		return
	}
	if _, err := os.Stat(leaseDD); err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(warmPath), 0o700); err != nil {
		return
	}
	tmp := warmPath + ".tmp-" + strconv.Itoa(os.Getpid())
	_ = os.RemoveAll(tmp)
	if err := exec.Command("cp", "-Rc", leaseDD, tmp).Run(); err != nil {
		_ = os.RemoveAll(tmp)
		return
	}
	_ = os.RemoveAll(warmPath)
	_ = os.Rename(tmp, warmPath)
}
