package redact

import (
	"strings"
	"testing"
)

func TestSecretsRedactsCursorAPIKeyPrefix(t *testing.T) {
	// Use a clearly fake token shape — never a real credential.
	in := "agent --api-key crsr_TESTFAKE00000000000000000000000000000000 --endpoint https://example.test"
	out := Secrets(in)
	if strings.Contains(out, "crsr_") {
		t.Fatalf("crsr_ prefix still present after redaction")
	}
	if !strings.Contains(out, MarkerCursorAPIKey) {
		t.Fatalf("expected marker %q in %q", MarkerCursorAPIKey, out)
	}
	if strings.Contains(out, "TESTFAKE") {
		t.Fatalf("token body leaked after redaction")
	}
}

func TestSecretsRedactsAPIKeyFlagEvenWithoutPrefix(t *testing.T) {
	in := "tool --api-key not-a-crsr-but-still-secret --verbose"
	out := Secrets(in)
	if strings.Contains(out, "not-a-crsr-but-still-secret") {
		t.Fatalf("api-key argument leaked: %q", out)
	}
	if !strings.Contains(out, "--api-key") {
		t.Fatalf("flag name should remain for diagnostics: %q", out)
	}
}

func TestSecretsRedactsEnvAssignments(t *testing.T) {
	in := "CURSOR_API_KEY=supersecret TOKEN=abc PASSWORD=x API_KEY=y"
	out := Secrets(in)
	for _, leak := range []string{"supersecret", "abc", "x", "y"} {
		if strings.Contains(out, leak) {
			t.Fatalf("env value %q leaked in %q", leak, out)
		}
	}
	if !strings.Contains(out, "CURSOR_API_KEY=[REDACTED]") {
		t.Fatalf("expected CURSOR_API_KEY redaction, got %q", out)
	}
}

func TestSecretsPreservesSafeText(t *testing.T) {
	in := "found 2 stray processes; pids=[123,456]; command_lines_omitted"
	out := Secrets(in)
	if out != in {
		t.Fatalf("safe text mutated: got %q want %q", out, in)
	}
}

func TestStripCommandLinesKeepsOnlyProcessMetadata(t *testing.T) {
	// Simulate a ps aux line that would otherwise leak --api-key.
	psLine := "user  1234  0.1  0.2  100  200  ??  S  1:00AM  0:01.00 /path/cursor-agent --api-key crsr_TESTFAKE00000000000000000000000000000000 worker"
	out := StripCommandLines(psLine)
	if strings.Contains(out, "crsr_") || strings.Contains(out, "--api-key") || strings.Contains(out, "TESTFAKE") {
		t.Fatalf("command line content survived StripCommandLines: %q", out)
	}
	if strings.Contains(out, "/path/cursor-agent") {
		t.Fatalf("executable path survived StripCommandLines: %q", out)
	}
}
