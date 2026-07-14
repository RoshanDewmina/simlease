package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
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
