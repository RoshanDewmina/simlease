package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
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
	Context context.Context
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

// WarmSeedKeyIncompleteError means no seed may be read or published because a
// correctness-sensitive key dimension is unavailable.
type WarmSeedKeyIncompleteError struct {
	Field string
}

func (e *WarmSeedKeyIncompleteError) Error() string {
	return fmt.Sprintf("warm seed key incomplete: %s is required; cold/no-seed", e.Field)
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

func warmSeedLockPath(home, digest string) string {
	return filepath.Join(home, warmSeedDirName, ".locks", digest+".lock")
}

func withWarmSeedLock(ctx context.Context, home, digest string, shared bool, fn func() error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	lockPath := warmSeedLockPath(home, digest)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return fmt.Errorf("warm seed lock mkdir: %w", err)
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("warm seed lock open: %w", err)
	}
	defer lock.Close()

	op := syscall.LOCK_EX | syscall.LOCK_NB
	if shared {
		op = syscall.LOCK_SH | syscall.LOCK_NB
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		err = syscall.Flock(int(lock.Fd()), op)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("warm seed lock acquire: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return fn()
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
	return PublishWarmSeedContext(context.Background(), home, key, sourceDD)
}

// PublishWarmSeedContext holds a per-digest exclusive filesystem lock from
// staging through atomic live-tree replacement.
func PublishWarmSeedContext(ctx context.Context, home string, key WarmSeedKey, sourceDD string) error {
	if home == "" || sourceDD == "" {
		return fmt.Errorf("warm seed: home and source DerivedData are required")
	}
	if key.PolicyVersion == "" {
		key.PolicyVersion = WarmSeedPolicyVersion
	}
	if err := validateWarmSeedKey(key); err != nil {
		return err
	}
	digest := WarmSeedDigest(key)
	return withWarmSeedLock(ctx, home, digest, false, func() error {
		parent := filepath.Join(home, warmSeedDirName)
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return err
		}
		tmpRoot, err := os.MkdirTemp(parent, "."+digest+".staging-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmpRoot)

		dataDst := filepath.Join(tmpRoot, "DerivedData")
		if err := defaultCloneDir(sourceDD, dataDst); err != nil {
			if err2 := exec.CommandContext(ctx, "cp", "-R", sourceDD, dataDst).Run(); err2 != nil {
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

		root := warmSeedRoot(home, digest)
		backup, err := os.MkdirTemp(parent, "."+digest+".backup-*")
		if err != nil {
			return err
		}
		if err := os.Remove(backup); err != nil {
			return err
		}
		hadRoot := false
		if _, err := os.Stat(root); err == nil {
			if err := os.Rename(root, backup); err != nil {
				return err
			}
			hadRoot = true
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.Rename(tmpRoot, root); err != nil {
			if hadRoot {
				_ = os.Rename(backup, root)
			}
			return err
		}
		if hadRoot {
			_ = os.RemoveAll(backup)
		}
		return nil
	})
}

// SeedWarmDerivedData CoW-clones a matching immutable seed into an empty lease DerivedData.
// Non-empty destinations are never overwritten. Clone failure → bounded cold fallback.
func SeedWarmDerivedData(opts SeedWarmOptions) (SeedResult, error) {
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
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
	if err := validateWarmSeedKey(key); err != nil {
		result.Outcome = SeedOutcomeMiss
		return result, err
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

	err = withWarmSeedLock(ctx, opts.Home, digest, true, func() error {
		seedPath := warmSeedDataPath(opts.Home, digest)
		metaPath := warmSeedMetaPath(opts.Home, digest)
		if _, err := os.Stat(seedPath); err != nil {
			result.Outcome = SeedOutcomeMiss
			return nil
		}
		raw, err := os.ReadFile(metaPath)
		if err != nil {
			result.Outcome = SeedOutcomeMiss
			return nil
		}
		var meta warmSeedMeta
		if json.Unmarshal(raw, &meta) != nil || meta.Digest != digest || !WarmSeedKeyEqual(meta.Key, key) {
			result.Outcome = SeedOutcomeMiss
			return nil
		}

		clone := opts.Clone
		if clone == nil {
			clone = defaultCloneDir
		}
		if err := os.Remove(opts.LeaseDD); err != nil {
			result.Outcome = SeedOutcomeFallbackCold
			_ = os.MkdirAll(opts.LeaseDD, 0o700)
			return nil
		}
		if err := clone(seedPath, opts.LeaseDD); err != nil {
			_ = os.RemoveAll(opts.LeaseDD)
			_ = os.MkdirAll(opts.LeaseDD, 0o700)
			result.Outcome = SeedOutcomeFallbackCold
			return nil
		}
		if _, err := os.Stat(opts.LeaseDD); err != nil {
			_ = os.MkdirAll(opts.LeaseDD, 0o700)
			result.Outcome = SeedOutcomeFallbackCold
			return nil
		}
		result.Outcome = SeedOutcomeHit
		return nil
	})
	if err != nil {
		return result, err
	}
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

// BuildWarmSeedKeyFromArgv constructs a complete key or returns a typed
// cold/no-seed error. It never invents a configuration.
func BuildWarmSeedKeyFromArgv(argv []string, xcodeVersion, xcodeBuild, sdk, runtime, packageResolved string) (WarmSeedKey, error) {
	project, scheme := extractProjectScheme(argv)
	if project == "" {
		return WarmSeedKey{}, &WarmSeedKeyIncompleteError{Field: "project/workspace identity"}
	}
	if scheme == "" {
		return WarmSeedKey{}, &WarmSeedKeyIncompleteError{Field: "scheme"}
	}
	configuration, ok := extractConfiguration(argv)
	if !ok {
		return WarmSeedKey{}, &WarmSeedKeyIncompleteError{Field: "configuration"}
	}
	key := WarmSeedKey{
		ProjectIdentity: project,
		Scheme:          scheme,
		Configuration:   configuration,
		XcodeVersion:    xcodeVersion,
		XcodeBuild:      xcodeBuild,
		SDK:             sdk,
		Runtime:         runtime,
		PackageResolved: packageResolved,
		PolicyVersion:   WarmSeedPolicyVersion,
	}
	if err := validateWarmSeedKey(key); err != nil {
		return WarmSeedKey{}, err
	}
	return key, nil
}

func extractConfiguration(argv []string) (string, bool) {
	for i := 0; i < len(argv); i++ {
		if argv[i] == "-configuration" && i+1 < len(argv) {
			value := strings.TrimSpace(argv[i+1])
			return value, value != ""
		}
	}
	return "", false
}

func validateWarmSeedKey(key WarmSeedKey) error {
	required := []struct {
		name, value string
	}{
		{"project/workspace identity", key.ProjectIdentity},
		{"scheme", key.Scheme},
		{"configuration", key.Configuration},
		{"Xcode version", key.XcodeVersion},
		{"Xcode build", key.XcodeBuild},
		{"SDK", key.SDK},
		{"runtime", key.Runtime},
		{"policy version", key.PolicyVersion},
	}
	for _, field := range required {
		if strings.TrimSpace(field.value) == "" {
			return &WarmSeedKeyIncompleteError{Field: field.name}
		}
	}
	return nil
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
