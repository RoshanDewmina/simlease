package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunProofHelp(t *testing.T) {
	if got := runProof([]string{"--help"}); got != 0 {
		t.Fatalf("runProof(--help) = %d, want 0", got)
	}
}

func TestRunProofStatusNoPolicy(t *testing.T) {
	dir := t.TempDir()
	cwd := mustChdir(t, dir)
	defer mustChdir(t, cwd)

	out := captureStdout(t, func() {
		if got := runProof([]string{"status", "--lease-root", dir}); got != 0 {
			t.Fatalf("exit = %d, want 0", got)
		}
	})
	if !strings.Contains(out, "no policy configured") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestRunProofStatusSatisfied(t *testing.T) {
	lease := t.TempDir()
	writeProofAudit(t, lease,
		proofAuditLine([]string{"xcodebuild", "build"}, 0),
		proofAuditLine([]string{"xcodebuild", "test"}, 0),
	)
	policy := writeProofPolicy(t, `
verification:
  policy_version: 1
  quick: [build, unit_tests]
  full: [clean_build, unit_tests, ui_tests]
  required_for_done: quick
`)

	var exit int
	out := captureStdout(t, func() {
		exit = runProof([]string{
			"status",
			"--lease-root", lease,
			"--policy", policy,
		})
	})
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; out=%s", exit, out)
	}
	if !strings.Contains(out, "verdict: satisfied") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestRunProofStatusFullMissingUITests(t *testing.T) {
	lease := t.TempDir()
	writeProofAudit(t, lease,
		proofAuditLine([]string{"xcodebuild", "clean", "build"}, 0),
		proofAuditLine([]string{"xcodebuild", "test"}, 0),
	)
	policy := writeProofPolicy(t, `
verification:
  policy_version: 1
  quick: [build, unit_tests]
  full: [clean_build, unit_tests, ui_tests]
  required_for_done: quick
`)

	var exit int
	out := captureStdout(t, func() {
		exit = runProof([]string{
			"status",
			"--lease-root", lease,
			"--policy", policy,
			"--tier", "full",
		})
	})
	if exit != 3 {
		t.Fatalf("exit = %d, want 3; out=%s", exit, out)
	}
	if !strings.Contains(out, "ui_tests") || !strings.Contains(out, "missing") {
		t.Fatalf("expected ui_tests missing, got %q", out)
	}
	if !strings.Contains(out, "verdict: unsatisfied") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestRunProofStatusJSON(t *testing.T) {
	lease := t.TempDir()
	writeProofAudit(t, lease,
		proofAuditLine([]string{"xcodebuild", "build"}, 0),
		proofAuditLine([]string{"xcodebuild", "test"}, 0),
	)
	policy := writeProofPolicy(t, `
verification:
  policy_version: 1
  quick: [build, unit_tests]
  full: [clean_build, unit_tests, ui_tests]
  required_for_done: quick
`)

	out := captureStdout(t, func() {
		if got := runProof([]string{
			"status",
			"--lease-root", lease,
			"--policy", policy,
			"--json",
		}); got != 0 {
			t.Fatalf("exit = %d, want 0", got)
		}
	})
	var rep struct {
		PolicyVersion int    `json:"policy_version"`
		Tier          string `json:"tier"`
		Satisfied     bool   `json:"satisfied"`
		Steps         []struct {
			Name      string `json:"name"`
			Satisfied bool   `json:"satisfied"`
			Reason    string `json:"reason"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("json: %v\nout=%s", err, out)
	}
	if rep.PolicyVersion != 1 || rep.Tier != "quick" || !rep.Satisfied {
		t.Fatalf("report = %+v", rep)
	}
	if len(rep.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(rep.Steps))
	}
}

func TestRunProofStatusBadPolicy(t *testing.T) {
	policy := writeProofPolicy(t, `
verification:
  policy_version: 99
  quick: [build]
  full: [build]
  required_for_done: quick
`)
	var exit int
	out := captureStdout(t, func() {
		exit = runProof([]string{
			"status",
			"--lease-root", t.TempDir(),
			"--policy", policy,
		})
	})
	if exit != 1 {
		t.Fatalf("exit = %d, want 1; out=%s", exit, out)
	}
	if !strings.Contains(out, "policy_version") {
		t.Fatalf("stdout = %q", out)
	}
}

func writeProofPolicy(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "verification.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeProofAudit(t *testing.T, leaseRoot string, lines ...string) {
	t.Helper()
	dir := filepath.Join(leaseRoot, "pathshim")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var body string
	for _, line := range lines {
		body += line + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "audit.log"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func proofAuditLine(argv []string, exit int) string {
	raw, err := json.Marshal(map[string]any{
		"timestamp":  time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC),
		"binary":     "xcodebuild",
		"argv":       argv,
		"realBinary": "/usr/bin/xcodebuild",
		"exitCode":   exit,
		"mode":       "audit",
	})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func mustChdir(t *testing.T, dir string) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return cwd
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()

	done := make(chan string, 1)
	go func() {
		var buf strings.Builder
		tmp := make([]byte, 4096)
		for {
			n, readErr := r.Read(tmp)
			if n > 0 {
				buf.Write(tmp[:n])
			}
			if readErr != nil {
				done <- buf.String()
				return
			}
		}
	}()

	fn()
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}
