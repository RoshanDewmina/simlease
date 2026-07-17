package proof

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPolicyHappyPath(t *testing.T) {
	path := writePolicy(t, `
verification:
  policy_version: 1
  quick: [build, unit_tests]
  full: [clean_build, unit_tests, ui_tests]
  required_for_done: quick
`)
	p, err := LoadPolicy(path)
	if err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}
	if p.PolicyVersion != 1 {
		t.Fatalf("PolicyVersion = %d, want 1", p.PolicyVersion)
	}
	if got := strings.Join(p.Quick, ","); got != "build,unit_tests" {
		t.Fatalf("Quick = %v", p.Quick)
	}
	if got := strings.Join(p.Full, ","); got != "clean_build,unit_tests,ui_tests" {
		t.Fatalf("Full = %v", p.Full)
	}
	if p.RequiredForDone != "quick" {
		t.Fatalf("RequiredForDone = %q, want quick", p.RequiredForDone)
	}
}

func TestLoadPolicyErrors(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantSub string
		code    string
	}{
		{
			name:    "malformed yaml",
			body:    "verification: [\n",
			wantSub: "yaml",
			code:    "INVALID_POLICY",
		},
		{
			name: "wrong policy_version",
			body: `
verification:
  policy_version: 2
  quick: [build]
  full: [build]
  required_for_done: quick
`,
			wantSub: "policy_version",
			code:    "UNSUPPORTED_POLICY_VERSION",
		},
		{
			name: "unknown step",
			body: `
verification:
  policy_version: 1
  quick: [build, lint]
  full: [build]
  required_for_done: quick
`,
			wantSub: "lint",
			code:    "UNKNOWN_STEP",
		},
		{
			name: "empty tier",
			body: `
verification:
  policy_version: 1
  quick: []
  full: [build]
  required_for_done: quick
`,
			wantSub: "quick",
			code:    "EMPTY_TIER",
		},
		{
			name: "missing required tier",
			body: `
verification:
  policy_version: 1
  quick: [build]
  full: [build]
  required_for_done: staging
`,
			wantSub: "staging",
			code:    "UNKNOWN_TIER",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writePolicy(t, tt.body)
			_, err := LoadPolicy(path)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantSub)
			}
			var pe *PolicyError
			if !errors.As(err, &pe) {
				t.Fatalf("want PolicyError, got %T: %v", err, err)
			}
			if pe.Code != tt.code {
				t.Fatalf("Code = %q, want %q", pe.Code, tt.code)
			}
		})
	}
}

func TestLoadPolicyMissingFile(t *testing.T) {
	_, err := LoadPolicy(filepath.Join(t.TempDir(), "missing.yaml"))
	if !errors.Is(err, ErrNoPolicy) {
		t.Fatalf("got %v, want ErrNoPolicy", err)
	}
}

func writePolicy(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "verification.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
