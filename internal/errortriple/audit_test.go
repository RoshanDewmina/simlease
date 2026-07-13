package errortriple

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestAuditDetectsEmptyLiteralInFixture(t *testing.T) {
	dir := t.TempDir()
	src := `package fixture

func NewError(code, message, fix string) error { return nil }

func good() {
	_ = NewError("CODE", "message", "fix")
}

func bad() {
	_ = NewError("", "message", "fix")
}
`
	if err := os.WriteFile(filepath.Join(dir, "fixture.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	findings, examined, err := audit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if examined != 2 {
		t.Fatalf("examined = %d, want 2", examined)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %#v, want exactly 1", findings)
	}
	f := findings[0]
	if f.File != "fixture.go" {
		t.Errorf("File = %q, want fixture.go", f.File)
	}
	if f.Line != 10 {
		t.Errorf("Line = %d, want 10", f.Line)
	}
	if !strings.Contains(f.Snippet, `NewError("", "message", "fix")`) {
		t.Errorf("Snippet = %q, want empty-literal call", f.Snippet)
	}
}

// TestAuditSkipsBenchResultsAndOtherBulkDirs is a regression test: a real
// bench run left 26,000+ files under bench/results/, which filepath.WalkDir
// happily traverses regardless of .gitignore — that alone drove this
// package's test from ~1.8s to ~7.8s. audit() must skip these dirs by
// structure, not rely on the fixture staying small.
func TestAuditSkipsBenchResultsAndOtherBulkDirs(t *testing.T) {
	dir := t.TempDir()
	src := `package fixture

func NewError(code, message, fix string) error { return nil }

func good() {
	_ = NewError("CODE", "message", "fix")
}
`
	if err := os.WriteFile(filepath.Join(dir, "fixture.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	bulkDirs := []string{
		filepath.Join(dir, "bench", "results", "20260713T000000Z"),
		filepath.Join(dir, "bench", ".bin"),
		filepath.Join(dir, ".worktrees", "some-branch"),
		filepath.Join(dir, "internal", "__pycache__"),
	}
	for _, d := range bulkDirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		// A .go file containing an empty-literal call planted inside each
		// bulk dir — if audit() ever stops skipping it, this test starts
		// failing on a false-positive finding instead of silently passing.
		bait := `package bait

func NewError(code, message, fix string) error { return nil }

func bad() {
	_ = NewError("", "message", "fix")
}
`
		if err := os.WriteFile(filepath.Join(d, "bait.go"), []byte(bait), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	findings, examined, err := audit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if examined != 1 {
		t.Fatalf("examined = %d, want 1 (only fixture.go's call site — bulk dirs must be skipped)", examined)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %#v, want none — a finding here means a bulk dir was walked", findings)
	}
}

func TestNoEmptyErrorTripleLiteralsInRepo(t *testing.T) {
	root := moduleRoot(t)
	findings, examined, err := audit(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("examined %d NewError call sites under %s", examined, root)
	if examined == 0 {
		t.Fatal("examined 0 NewError call sites; audit did not walk the repo")
	}
	if len(findings) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString("empty error-triple string literal(s) found:\n")
	for _, f := range findings {
		b.WriteString(f.File)
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(f.Line))
		b.WriteString(": ")
		b.WriteString(f.Snippet)
		b.WriteByte('\n')
	}
	t.Fatal(b.String())
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from test file")
		}
		dir = parent
	}
}
