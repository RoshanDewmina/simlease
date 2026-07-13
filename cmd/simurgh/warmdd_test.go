package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWarmDDKeyDeterministicAndDistinct(t *testing.T) {
	a := warmDDKey("/repo/App.xcodeproj", "App")
	b := warmDDKey("/repo/App.xcodeproj", "App")
	if a != b {
		t.Fatalf("same inputs produced different keys: %q vs %q", a, b)
	}
	c := warmDDKey("/repo/App.xcodeproj", "OtherScheme")
	if a == c {
		t.Fatalf("different schemes produced the same key")
	}
	d := warmDDKey("/other/App.xcodeproj", "App")
	if a == d {
		t.Fatalf("different project paths produced the same key")
	}
}

func TestExtractProjectScheme(t *testing.T) {
	proj, scheme := extractProjectScheme([]string{
		"xcodebuild", "build", "-project", "App.xcodeproj", "-scheme", "App", "-configuration", "Debug",
	})
	abs, _ := filepath.Abs("App.xcodeproj")
	if proj != abs {
		t.Fatalf("project=%q want %q", proj, abs)
	}
	if scheme != "App" {
		t.Fatalf("scheme=%q want App", scheme)
	}

	proj, scheme = extractProjectScheme([]string{"xcodebuild", "-showBuildSettings"})
	if proj != "" || scheme != "" {
		t.Fatalf("no -project/-scheme present: got proj=%q scheme=%q, want both empty", proj, scheme)
	}
}

func TestIsXcodebuildQueryOnly(t *testing.T) {
	cases := []struct {
		argv []string
		want bool
	}{
		{[]string{"xcodebuild", "-showBuildSettings", "-project", "App.xcodeproj"}, true},
		{[]string{"xcodebuild", "-list"}, true},
		{[]string{"xcodebuild", "build", "-project", "App.xcodeproj"}, false},
		{[]string{"xcodebuild", "test", "-project", "App.xcodeproj"}, false},
	}
	for _, c := range cases {
		if got := isXcodebuildQueryOnly(c.argv); got != c.want {
			t.Fatalf("isXcodebuildQueryOnly(%v)=%v want %v", c.argv, got, c.want)
		}
	}
}

func TestSeedDerivedDataFromWarmRoundTrip(t *testing.T) {
	root := t.TempDir()
	warm := filepath.Join(root, "warm")
	lease := filepath.Join(root, "lease-dd")

	// No warm cache yet — seeding must be a safe no-op, lease dir untouched.
	if err := os.MkdirAll(lease, 0o700); err != nil {
		t.Fatal(err)
	}
	seedDerivedDataFromWarm(warm, lease)
	entries, err := os.ReadDir(lease)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("seeded from nonexistent warm cache — lease dir has %d entries, want 0", len(entries))
	}

	// Populate "warm" as if a prior build had happened, then refresh from
	// a simulated lease DerivedData into it.
	priorLeaseDD := filepath.Join(root, "prior-lease-dd")
	if err := os.MkdirAll(filepath.Join(priorLeaseDD, "Build", "Products"), 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(priorLeaseDD, "Build", "Products", "marker.txt")
	if err := os.WriteFile(marker, []byte("warm build product"), 0o600); err != nil {
		t.Fatal(err)
	}
	refreshWarmFromDerivedData(priorLeaseDD, warm)
	if _, err := os.Stat(filepath.Join(warm, "Build", "Products", "marker.txt")); err != nil {
		t.Fatalf("refresh did not populate warm cache: %v", err)
	}

	// Now seed a fresh, empty lease dir from that warm cache.
	seedDerivedDataFromWarm(warm, lease)
	got, err := os.ReadFile(filepath.Join(lease, "Build", "Products", "marker.txt"))
	if err != nil {
		t.Fatalf("seeded lease dir missing warm content: %v", err)
	}
	if string(got) != "warm build product" {
		t.Fatalf("seeded content=%q want %q", got, "warm build product")
	}
}

func TestSeedDerivedDataFromWarmNeverOverwritesNonEmptyLeaseDir(t *testing.T) {
	root := t.TempDir()
	warm := filepath.Join(root, "warm")
	lease := filepath.Join(root, "lease-dd")

	if err := os.MkdirAll(warm, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(warm, "warm-marker.txt"), []byte("warm"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(lease, 0o700); err != nil {
		t.Fatal(err)
	}
	ownMarker := filepath.Join(lease, "own-marker.txt")
	if err := os.WriteFile(ownMarker, []byte("already building"), 0o600); err != nil {
		t.Fatal(err)
	}

	seedDerivedDataFromWarm(warm, lease)

	if _, err := os.Stat(filepath.Join(lease, "warm-marker.txt")); err == nil {
		t.Fatalf("seeded into a non-empty lease dir — must never clobber in-progress state")
	}
	got, err := os.ReadFile(ownMarker)
	if err != nil || string(got) != "already building" {
		t.Fatalf("lease dir's own content was disturbed: content=%q err=%v", got, err)
	}
}
