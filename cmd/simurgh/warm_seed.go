package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// WarmSeedPolicyVersion bumps whenever seed layout or matching semantics change.
// Mismatched policy versions are an exact miss → cold/no-seed.
const WarmSeedPolicyVersion = "1"

const warmSeedDirName = "warm-dd"

// WarmSeedKey is the formal APFS seed compatibility key (R-WP4 / ARCHITECTURE §9.2).
// Exact mismatch on any field means cold/no-seed — never a silent cross-seed.
type WarmSeedKey struct {
	ProjectIdentity string // canonical absolute -project / -workspace path
	Scheme          string
	Configuration   string
	XcodeVersion    string
	XcodeBuild      string
	SDK             string
	Runtime         string
	PackageResolved string // hash of Package.resolved contents (or empty if absent)
	PolicyVersion   string
}

// SeedOutcome classifies a SeedWarmDerivedData attempt.
type SeedOutcome string

const (
	SeedOutcomeHit             SeedOutcome = "hit"
	SeedOutcomeMiss            SeedOutcome = "miss"
	SeedOutcomeSkippedNonEmpty SeedOutcome = "skipped_nonempty"
	SeedOutcomeFallbackCold    SeedOutcome = "fallback_cold"
)

// SeedResult is the structured outcome of a seed attempt (never fabricated timings).
type SeedResult struct {
	Outcome SeedOutcome
	Digest  string
	Path    string
}

// SeedWarmOptions configures a private writable DerivedData seed from an immutable shared seed.
type SeedWarmOptions struct {
	Home    string
	Key     WarmSeedKey
	LeaseDD string
	// Clone copies src→dst (APFS CoW via cp -Rc by default). Injected for fallback tests.
	Clone func(src, dst string) error
}

type warmSeedMeta struct {
	Key    WarmSeedKey `json:"key"`
	Digest string      `json:"digest"`
}

// WarmSeedKeyEqual reports exact field equality (including empty vs non-empty).
func WarmSeedKeyEqual(a, b WarmSeedKey) bool {
	return a.ProjectIdentity == b.ProjectIdentity &&
		a.Scheme == b.Scheme &&
		a.Configuration == b.Configuration &&
		a.XcodeVersion == b.XcodeVersion &&
		a.XcodeBuild == b.XcodeBuild &&
		a.SDK == b.SDK &&
		a.Runtime == b.Runtime &&
		a.PackageResolved == b.PackageResolved &&
		a.PolicyVersion == b.PolicyVersion
}

// WarmSeedDigest returns a stable hex digest of the compatibility key.
func WarmSeedDigest(k WarmSeedKey) string {
	if k.PolicyVersion == "" {
		k.PolicyVersion = WarmSeedPolicyVersion
	}
	payload := strings.Join([]string{
		"project=" + k.ProjectIdentity,
		"scheme=" + k.Scheme,
		"configuration=" + k.Configuration,
		"xcodeVersion=" + k.XcodeVersion,
		"xcodeBuild=" + k.XcodeBuild,
		"sdk=" + k.SDK,
		"runtime=" + k.Runtime,
		"packageResolved=" + k.PackageResolved,
		"policyVersion=" + k.PolicyVersion,
	}, "\n")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func warmSeedRoot(home, digest string) string {
	return filepath.Join(home, warmSeedDirName, digest)
}

func warmSeedDataPath(home, digest string) string {
	return filepath.Join(warmSeedRoot(home, digest), "DerivedData")
}

func warmSeedMetaPath(home, digest string) string {
	return filepath.Join(warmSeedRoot(home, digest), "meta.json")
}

func defaultCloneDir(src, dst string) error {
	cmd := exec.Command("cp", "-Rc", src, dst)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cp -Rc: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// PublishWarmSeed stores an immutable shared seed under home for key.
// The seed tree is never mutated in place by later leases — each lease CoW-clones it.
func PublishWarmSeed(home string, key WarmSeedKey, sourceDD string) error {
	if home == "" || sourceDD == "" {
		return fmt.Errorf("warm seed: home and source DerivedData are required")
	}
	if key.PolicyVersion == "" {
		key.PolicyVersion = WarmSeedPolicyVersion
	}
	if key.ProjectIdentity == "" || key.Scheme == "" {
		return fmt.Errorf("warm seed: project identity and scheme are required")
	}
	digest := WarmSeedDigest(key)
	root := warmSeedRoot(home, digest)
	tmpRoot := root + ".tmp-" + strconv.Itoa(os.Getpid())
	_ = os.RemoveAll(tmpRoot)
	if err := os.MkdirAll(tmpRoot, 0o700); err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpRoot) }()

	dataDst := filepath.Join(tmpRoot, "DerivedData")
	if err := defaultCloneDir(sourceDD, dataDst); err != nil {
		// Fall back to plain recursive copy when CoW is unavailable at publish time.
		if err2 := exec.Command("cp", "-R", sourceDD, dataDst).Run(); err2 != nil {
			return fmt.Errorf("warm seed publish clone: %v / fallback: %w", err, err2)
		}
	}
	meta := warmSeedMeta{Key: key, Digest: digest}
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmpRoot, "meta.json"), raw, 0o600); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(root), 0o700); err != nil {
		return err
	}
	_ = os.RemoveAll(root)
	if err := os.Rename(tmpRoot, root); err != nil {
		return err
	}
	return nil
}

// SeedWarmDerivedData CoW-clones a matching immutable seed into an empty lease DerivedData.
// Non-empty destinations are never overwritten. Clone failure → bounded cold fallback.
func SeedWarmDerivedData(opts SeedWarmOptions) (SeedResult, error) {
	key := opts.Key
	if key.PolicyVersion == "" {
		key.PolicyVersion = WarmSeedPolicyVersion
	}
	digest := WarmSeedDigest(key)
	result := SeedResult{Digest: digest, Path: warmSeedDataPath(opts.Home, digest)}

	if opts.Home == "" || opts.LeaseDD == "" {
		result.Outcome = SeedOutcomeMiss
		return result, nil
	}
	if key.ProjectIdentity == "" || key.Scheme == "" {
		result.Outcome = SeedOutcomeMiss
		return result, nil
	}

	entries, err := os.ReadDir(opts.LeaseDD)
	if err != nil {
		result.Outcome = SeedOutcomeMiss
		return result, nil
	}
	if len(entries) > 0 {
		result.Outcome = SeedOutcomeSkippedNonEmpty
		return result, nil
	}

	seedPath := warmSeedDataPath(opts.Home, digest)
	metaPath := warmSeedMetaPath(opts.Home, digest)
	if _, err := os.Stat(seedPath); err != nil {
		result.Outcome = SeedOutcomeMiss
		return result, nil
	}
	// Exact key re-check via stored meta (defense in depth against digest collisions / stale trees).
	if raw, err := os.ReadFile(metaPath); err == nil {
		var meta warmSeedMeta
		if json.Unmarshal(raw, &meta) == nil && !WarmSeedKeyEqual(meta.Key, key) {
			result.Outcome = SeedOutcomeMiss
			return result, nil
		}
	}

	clone := opts.Clone
	if clone == nil {
		clone = defaultCloneDir
	}
	if err := os.Remove(opts.LeaseDD); err != nil {
		result.Outcome = SeedOutcomeFallbackCold
		_ = os.MkdirAll(opts.LeaseDD, 0o700)
		return result, nil
	}
	if err := clone(seedPath, opts.LeaseDD); err != nil {
		_ = os.RemoveAll(opts.LeaseDD)
		_ = os.MkdirAll(opts.LeaseDD, 0o700)
		result.Outcome = SeedOutcomeFallbackCold
		return result, nil
	}
	if _, err := os.Stat(opts.LeaseDD); err != nil {
		_ = os.MkdirAll(opts.LeaseDD, 0o700)
		result.Outcome = SeedOutcomeFallbackCold
		return result, nil
	}
	result.Outcome = SeedOutcomeHit
	return result, nil
}

// HashPackageResolved returns a hex sha256 of path contents, or "" if missing.
func HashPackageResolved(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// BuildWarmSeedKeyFromArgv constructs a key from xcodebuild argv plus toolchain identity.
func BuildWarmSeedKeyFromArgv(argv []string, xcodeVersion, xcodeBuild, sdk, runtime, packageResolved string) (WarmSeedKey, bool) {
	project, scheme := extractProjectScheme(argv)
	if project == "" || scheme == "" {
		return WarmSeedKey{}, false
	}
	return WarmSeedKey{
		ProjectIdentity: project,
		Scheme:          scheme,
		Configuration:   extractConfiguration(argv),
		XcodeVersion:    xcodeVersion,
		XcodeBuild:      xcodeBuild,
		SDK:             sdk,
		Runtime:         runtime,
		PackageResolved: packageResolved,
		PolicyVersion:   WarmSeedPolicyVersion,
	}, true
}

func extractConfiguration(argv []string) string {
	for i := 0; i < len(argv); i++ {
		if argv[i] == "-configuration" && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return "Debug"
}

// probeXcodeVersionBuild best-effort reads `xcodebuild -version`. Empty on failure.
func probeXcodeVersionBuild() (version, build string) {
	out, err := exec.Command("xcodebuild", "-version").Output()
	if err != nil {
		return "", ""
	}
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Xcode ") {
			version = strings.TrimSpace(strings.TrimPrefix(line, "Xcode "))
		}
		if strings.HasPrefix(line, "Build version ") {
			build = strings.TrimSpace(strings.TrimPrefix(line, "Build version "))
		}
	}
	return version, build
}

// probeSDKRuntime best-effort reads the default iphonesimulator SDK. Empty on failure.
func probeSDKRuntime() (sdk, runtime string) {
	out, err := exec.Command("xcrun", "--sdk", "iphonesimulator", "--show-sdk-version").Output()
	if err != nil {
		return "", ""
	}
	runtime = strings.TrimSpace(string(out))
	if runtime != "" {
		sdk = "iphonesimulator" + runtime
	}
	return sdk, runtime
}

// resolvePackageResolvedHash looks for Package.resolved near the project/workspace.
func resolvePackageResolvedHash(argv []string) string {
	project, _ := extractProjectScheme(argv)
	if project == "" {
		return ""
	}
	dir := filepath.Dir(project)
	candidates := []string{
		filepath.Join(dir, "Package.resolved"),
		filepath.Join(dir, project+".resolved"), // unlikely; kept for completeness
		filepath.Join(dir, "App.xcodeproj", "project.xcworkspace", "xcshareddata", "swiftpm", "Package.resolved"),
		filepath.Join(project, "project.xcworkspace", "xcshareddata", "swiftpm", "Package.resolved"),
		filepath.Join(dir, filepath.Base(project), "project.xcworkspace", "xcshareddata", "swiftpm", "Package.resolved"),
	}
	// Also check workspace xcshareddata when project is a .xcworkspace.
	if strings.HasSuffix(project, ".xcworkspace") {
		candidates = append(candidates, filepath.Join(project, "xcshareddata", "swiftpm", "Package.resolved"))
	}
	if strings.HasSuffix(project, ".xcodeproj") {
		candidates = append(candidates, filepath.Join(project, "project.xcworkspace", "xcshareddata", "swiftpm", "Package.resolved"))
	}
	for _, c := range candidates {
		if h := HashPackageResolved(c); h != "" {
			return h
		}
	}
	return ""
}
