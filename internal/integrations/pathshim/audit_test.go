package pathshim

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppendAuditLogWritesOneCorrectEntry(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "nested", "audit.log")
	ts := time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC)

	entry := AuditEntry{
		Timestamp:  ts,
		Binary:     "xcodebuild",
		Argv:       []string{"xcodebuild", "-version"},
		RealBinary: "/Applications/Xcode.app/Contents/Developer/usr/bin/xcodebuild",
		ExitCode:   0,
		Mode:       ModeAudit,
	}
	if err := AppendAuditLog(logPath, entry); err != nil {
		t.Fatalf("AppendAuditLog: %v", err)
	}

	lines := readLines(t, logPath)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want exactly 1: %v", len(lines), lines)
	}
	var got AuditEntry
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("unmarshal: %v\nline: %s", err, lines[0])
	}
	if !got.Timestamp.Equal(ts) {
		t.Errorf("Timestamp = %v, want %v", got.Timestamp, ts)
	}
	if got.Binary != "xcodebuild" {
		t.Errorf("Binary = %q, want xcodebuild", got.Binary)
	}
	if len(got.Argv) != 2 || got.Argv[0] != "xcodebuild" || got.Argv[1] != "-version" {
		t.Errorf("Argv = %v, want [xcodebuild -version]", got.Argv)
	}
	if got.RealBinary != entry.RealBinary {
		t.Errorf("RealBinary = %q, want %q", got.RealBinary, entry.RealBinary)
	}
	if got.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", got.ExitCode)
	}
	if got.Mode != ModeAudit {
		t.Errorf("Mode = %q, want %q", got.Mode, ModeAudit)
	}
}

func TestAppendAuditLogAppendsAcrossMultipleInvocations(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")

	for i := 0; i < 3; i++ {
		entry := AuditEntry{
			Timestamp:  time.Now().UTC(),
			Binary:     "xcrun",
			Argv:       []string{"xcrun", "simctl", "list"},
			RealBinary: "/x/usr/bin/xcrun",
			ExitCode:   i,
			Mode:       ModeAudit,
		}
		if err := AppendAuditLog(logPath, entry); err != nil {
			t.Fatalf("AppendAuditLog[%d]: %v", i, err)
		}
	}
	lines := readLines(t, logPath)
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3: %v", len(lines), lines)
	}
	for i, line := range lines {
		var got AuditEntry
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("unmarshal line %d: %v", i, err)
		}
		if got.ExitCode != i {
			t.Fatalf("line %d ExitCode = %d, want %d", i, got.ExitCode, i)
		}
	}
}

func TestAppendAuditLogRequiresPath(t *testing.T) {
	if err := AppendAuditLog("", AuditEntry{}); err == nil {
		t.Fatal("expected error for empty path, got nil")
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if sc.Text() != "" {
			lines = append(lines, sc.Text())
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return lines
}
