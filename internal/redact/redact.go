// Package redact scrubs credential-like values from strings destined for
// bench evidence / preflight JSON so process listings and logs cannot
// serialize secrets (e.g. cursor-agent --api-key crsr_…).
package redact

import (
	"regexp"
	"strings"
)

// MarkerCursorAPIKey is the explicit replacement for crsr_* tokens.
const MarkerCursorAPIKey = "[REDACTED:CURSOR_API_KEY]"

// MarkerGeneric is used for non-crsr secret-shaped values.
const MarkerGeneric = "[REDACTED]"

var (
	crsrToken = regexp.MustCompile(`crsr_[A-Za-z0-9_-]+`)
	// Flags that take a secret as the next argv token.
	secretFlags = regexp.MustCompile(`(?i)(--(?:api-key|token|password|secret))\s+(\S+)`)
	// ENV=value forms commonly leaked via process listings or shell dumps.
	secretEnv = regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:API[_-]?KEY|TOKEN|PASSWORD|SECRET|CREDENTIAL)[A-Z0-9_]*)=([^\s"']+)`)
	// First path-like token starts the command; everything from there is argv.
	psCommandStart = regexp.MustCompile(`(?:^|\s)(/|\./|[A-Za-z0-9._+-]+/)`)
)

// Secrets redacts credential-like substrings while preserving surrounding
// diagnostic context (flag names, env var names, non-secret text).
func Secrets(s string) string {
	if s == "" {
		return s
	}
	// Cursor tokens first so --api-key crsr_… becomes --api-key <cursor-marker>
	// before the generic flag rule runs.
	out := crsrToken.ReplaceAllString(s, MarkerCursorAPIKey)
	out = secretFlags.ReplaceAllStringFunc(out, func(m string) string {
		parts := strings.Fields(m)
		if len(parts) < 2 {
			return m
		}
		flag, arg := parts[0], parts[1]
		if strings.HasPrefix(arg, "[REDACTED") {
			return m // already scrubbed
		}
		return flag + " " + MarkerGeneric
	})
	out = secretEnv.ReplaceAllString(out, "${1}=[REDACTED]")
	return out
}

// StripCommandLines removes everything from the first path-like token onward
// on each line (ps aux COMMAND column and similar). Use when a caller still
// has raw process text and must not persist argv.
func StripCommandLines(s string) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		loc := psCommandStart.FindStringIndex(line)
		if loc == nil {
			lines[i] = Secrets(line)
			continue
		}
		meta := strings.TrimRight(line[:loc[0]], " \t")
		lines[i] = Secrets(meta) + " [command_line_omitted]"
	}
	return strings.Join(lines, "\n")
}
