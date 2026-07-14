package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func sampleWarmSeedKey() WarmSeedKey {
	return WarmSeedKey{
		ProjectIdentity: "/repo/App.xcodeproj",
		Scheme:          "App",
		Configuration:   "Debug",
		XcodeVersion:    "27.0",
		XcodeBuild:      "27A5194q",
		SDK:             "iphonesimulator27.0",
		Runtime:         "27.0",
		PackageResolved: "abc123resolved",
		PolicyVersion:   WarmSeedPolicyVersion,
	}
}

func TestWarmSeedCompatibilityHit(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	key := sampleWarmSeedKey()

	seedDD := filepath.Join(root, "prior-dd")
	if err := os.MkdirAll(filepath.Join(seedDD, "Build"), 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(seedDD, "Build", "marker.txt")
	if err := os.WriteFile(marker, []byte("warm-hit"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PublishWarmSeed(home, key, seedDD); err != nil {
		t.Fatalf("PublishWarmSeed: %v", err)
	}

	leaseDD := filepath.Join(root, "lease-dd")
	if err := os.MkdirAll(leaseDD, 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := SeedWarmDerivedData(SeedWarmOptions{
		Home:    home,
		Key:     key,
		LeaseDD: leaseDD,
	})
	if err != nil {
		t.Fatalf("SeedWarmDerivedData: %v", err)
	}
	if result.Outcome != SeedOutcomeHit {
		t.Fatalf("outcome=%q want %q", result.Outcome, SeedOutcomeHit)
	}
	got, err := os.ReadFile(filepath.Join(leaseDD, "Build", "marker.txt"))
	if err != nil {
		t.Fatalf("seeded content missing: %v", err)
	}
	if string(got) != "warm-hit" {
		t.Fatalf("content=%q want warm-hit", got)
	}
}

func TestWarmSeedCompatibilityMiss(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	key := sampleWarmSeedKey()

	seedDD := filepath.Join(root, "prior-dd")
	if err := os.MkdirAll(filepath.Join(seedDD, "Build"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seedDD, "Build", "marker.txt"), []byte("warm"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PublishWarmSeed(home, key, seedDD); err != nil {
		t.Fatalf("PublishWarmSeed: %v", err)
	}

	missKey := key
	missKey.XcodeBuild = "different-build"
	if WarmSeedKeyEqual(key, missKey) {
		t.Fatal("keys with different XcodeBuild must not be equal")
	}
	if WarmSeedDigest(key) == WarmSeedDigest(missKey) {
		t.Fatal("digest must change on exact field mismatch")
	}

	leaseDD := filepath.Join(root, "lease-dd")
	if err := os.MkdirAll(leaseDD, 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := SeedWarmDerivedData(SeedWarmOptions{
		Home:    home,
		Key:     missKey,
		LeaseDD: leaseDD,
	})
	if err != nil {
		t.Fatalf("SeedWarmDerivedData: %v", err)
	}
	if result.Outcome != SeedOutcomeMiss {
		t.Fatalf("outcome=%q want %q (exact mismatch → cold/no-seed)", result.Outcome, SeedOutcomeMiss)
	}
	entries, err := os.ReadDir(leaseDD)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("miss must leave empty lease DerivedData; got %d entries", len(entries))
	}
}

func TestWarmSeedNeverOverwritesNonEmptyDestination(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	key := sampleWarmSeedKey()

	seedDD := filepath.Join(root, "prior-dd")
	if err := os.MkdirAll(seedDD, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seedDD, "warm-marker.txt"), []byte("warm"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PublishWarmSeed(home, key, seedDD); err != nil {
		t.Fatal(err)
	}

	leaseDD := filepath.Join(root, "lease-dd")
	if err := os.MkdirAll(leaseDD, 0o700); err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(leaseDD, "own-marker.txt")
	if err := os.WriteFile(own, []byte("already building"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := SeedWarmDerivedData(SeedWarmOptions{
		Home:    home,
		Key:     key,
		LeaseDD: leaseDD,
	})
	if err != nil {
		t.Fatalf("SeedWarmDerivedData: %v", err)
	}
	if result.Outcome != SeedOutcomeSkippedNonEmpty {
		t.Fatalf("outcome=%q want %q", result.Outcome, SeedOutcomeSkippedNonEmpty)
	}
	if _, err := os.Stat(filepath.Join(leaseDD, "warm-marker.txt")); err == nil {
		t.Fatal("must never overwrite a non-empty destination with warm content")
	}
	got, err := os.ReadFile(own)
	if err != nil || string(got) != "already building" {
		t.Fatalf("own content disturbed: %q err=%v", got, err)
	}
}

func TestWarmSeedCloneFallback(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	key := sampleWarmSeedKey()

	seedDD := filepath.Join(root, "prior-dd")
	if err := os.MkdirAll(filepath.Join(seedDD, "Build"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seedDD, "Build", "marker.txt"), []byte("warm"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PublishWarmSeed(home, key, seedDD); err != nil {
		t.Fatal(err)
	}

	leaseDD := filepath.Join(root, "lease-dd")
	if err := os.MkdirAll(leaseDD, 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := SeedWarmDerivedData(SeedWarmOptions{
		Home:    home,
		Key:     key,
		LeaseDD: leaseDD,
		Clone: func(src, dst string) error {
			return errors.New("clonefile unsupported on this volume")
		},
	})
	if err != nil {
		t.Fatalf("SeedWarmDerivedData: %v", err)
	}
	if result.Outcome != SeedOutcomeFallbackCold {
		t.Fatalf("outcome=%q want %q", result.Outcome, SeedOutcomeFallbackCold)
	}
	info, err := os.Stat(leaseDD)
	if err != nil || !info.IsDir() {
		t.Fatalf("cold fallback must restore empty lease DerivedData dir: err=%v", err)
	}
	entries, err := os.ReadDir(leaseDD)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("fallback cold dest must be empty; got %d entries", len(entries))
	}
}

func TestWarmSeedConfigurationMustBeExplicit(t *testing.T) {
	base := []string{"xcodebuild", "-project", "App.xcodeproj", "-scheme", "App"}
	_, err := BuildWarmSeedKeyFromArgv(base, "27.0", "27A", "iphonesimulator27.0", "27.0", "")
	var incomplete *WarmSeedKeyIncompleteError
	if !errors.As(err, &incomplete) || incomplete.Field != "configuration" {
		t.Fatalf("omitted configuration err=%T %v; want typed configuration incomplete", err, err)
	}
	_, err = BuildWarmSeedKeyFromArgv(append(base, "-configuration", ""), "27.0", "27A", "iphonesimulator27.0", "27.0", "")
	if !errors.As(err, &incomplete) {
		t.Fatalf("empty configuration err=%T %v; want typed incomplete", err, err)
	}
	explicit, err := BuildWarmSeedKeyFromArgv(append(base, "-configuration", "Debug"), "27.0", "27A", "iphonesimulator27.0", "27.0", "")
	if err != nil {
		t.Fatalf("explicit Debug: %v", err)
	}
	if explicit.Configuration != "Debug" {
		t.Fatalf("configuration=%q want Debug", explicit.Configuration)
	}
}

func TestWarmSeedConcurrentPublishAndSeed(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	key := sampleWarmSeedKey()
	const iterations = 20

	makeSource := func(name string) string {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "generation.txt"), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	sourceA := makeSource("generation-a")
	sourceB := makeSource("generation-b")
	if err := PublishWarmSeed(home, key, sourceA); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, iterations*2)
	for i := 0; i < iterations; i++ {
		source := sourceA
		if i%2 == 1 {
			source = sourceB
		}
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := PublishWarmSeedContext(context.Background(), home, key, source); err != nil {
				errs <- err
			}
		}()
		go func(i int) {
			defer wg.Done()
			leaseDD := filepath.Join(root, "leases", fmt.Sprintf("lease-%d", i))
			if err := os.MkdirAll(leaseDD, 0o700); err != nil {
				errs <- err
				return
			}
			result, err := SeedWarmDerivedData(SeedWarmOptions{Context: context.Background(), Home: home, Key: key, LeaseDD: leaseDD})
			if err != nil {
				errs <- err
				return
			}
			if result.Outcome != SeedOutcomeHit {
				errs <- fmt.Errorf("seed %d outcome=%s", i, result.Outcome)
				return
			}
			raw, err := os.ReadFile(filepath.Join(leaseDD, "generation.txt"))
			if err != nil {
				errs <- err
				return
			}
			if got := string(raw); got != "generation-a" && got != "generation-b" {
				errs <- fmt.Errorf("seed %d observed partial generation %q", i, got)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestWarmSeedPublishFailureCleansStagingAndUnlocks(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	key := sampleWarmSeedKey()
	err := PublishWarmSeedContext(context.Background(), home, key, filepath.Join(root, "missing-source"))
	if err == nil {
		t.Fatal("expected publish failure")
	}
	parent := filepath.Join(home, warmSeedDirName)
	entries, readErr := os.ReadDir(parent)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".staging-") || strings.Contains(entry.Name(), ".backup-") {
			t.Fatalf("failed publish leaked %q", entry.Name())
		}
	}

	source := filepath.Join(root, "source")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "ok"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := PublishWarmSeedContext(ctx, home, key, source); err != nil {
		t.Fatalf("lock not released after failure: %v", err)
	}
}

func TestWarmSeedCrossProcessPublishSeedStress(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	key := sampleWarmSeedKey()
	sourceA := filepath.Join(root, "source-a")
	sourceB := filepath.Join(root, "source-b")
	for path, value := range map[string]string{sourceA: "a", sourceB: "b"} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "generation"), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := PublishWarmSeed(home, key, sourceA); err != nil {
		t.Fatal(err)
	}

	const iterations = 8
	for i := 0; i < iterations; i++ {
		source := sourceA
		if i%2 == 1 {
			source = sourceB
		}
		lease := filepath.Join(root, "process-leases", fmt.Sprintf("lease-%d", i))
		publish := warmSeedHelperCommand("publish", home, source, "")
		seed := warmSeedHelperCommand("seed", home, "", lease)
		if err := publish.Start(); err != nil {
			t.Fatal(err)
		}
		if err := seed.Start(); err != nil {
			t.Fatal(err)
		}
		if err := publish.Wait(); err != nil {
			t.Fatalf("publisher %d: %v", i, err)
		}
		if err := seed.Wait(); err != nil {
			t.Fatalf("seeder %d: %v", i, err)
		}
		raw, err := os.ReadFile(filepath.Join(lease, "generation"))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != "a" && string(raw) != "b" {
			t.Fatalf("iteration %d observed partial generation %q", i, raw)
		}
	}
}

func warmSeedHelperCommand(operation, home, source, lease string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestWarmSeedLockProcessHelper$")
	cmd.Env = append(os.Environ(),
		"SIMURGH_WARM_SEED_HELPER=1",
		"SIMURGH_WARM_SEED_OPERATION="+operation,
		"SIMURGH_WARM_SEED_HOME="+home,
		"SIMURGH_WARM_SEED_SOURCE="+source,
		"SIMURGH_WARM_SEED_LEASE="+lease,
	)
	return cmd
}

func TestWarmSeedLockProcessHelper(t *testing.T) {
	if os.Getenv("SIMURGH_WARM_SEED_HELPER") != "1" {
		t.Skip("subprocess helper")
	}
	key := sampleWarmSeedKey()
	switch os.Getenv("SIMURGH_WARM_SEED_OPERATION") {
	case "publish":
		if err := PublishWarmSeed(os.Getenv("SIMURGH_WARM_SEED_HOME"), key, os.Getenv("SIMURGH_WARM_SEED_SOURCE")); err != nil {
			t.Fatal(err)
		}
	case "seed":
		lease := os.Getenv("SIMURGH_WARM_SEED_LEASE")
		if err := os.MkdirAll(lease, 0o700); err != nil {
			t.Fatal(err)
		}
		result, err := SeedWarmDerivedData(SeedWarmOptions{
			Home:    os.Getenv("SIMURGH_WARM_SEED_HOME"),
			Key:     key,
			LeaseDD: lease,
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Outcome != SeedOutcomeHit {
			t.Fatalf("outcome=%s", result.Outcome)
		}
	default:
		t.Fatalf("unknown helper operation %q", os.Getenv("SIMURGH_WARM_SEED_OPERATION"))
	}
}
