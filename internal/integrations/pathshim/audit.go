package pathshim

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ModeAudit tags every AuditEntry this stage ever writes. A future
// warn/enforce stage would use its own Mode value, so old and new log
// lines stay distinguishable in one append-only file.
const ModeAudit = "audit"

// AuditEntry is one JSON-line record of a single shim invocation.
type AuditEntry struct {
	Timestamp  time.Time `json:"timestamp"`
	Binary     string    `json:"binary"`     // "xcodebuild" or "xcrun"
	Argv       []string  `json:"argv"`       // exact argv as invoked, argv[0] included
	RealBinary string    `json:"realBinary"` // resolved absolute path that was exec'd
	ExitCode   int       `json:"exitCode"`   // the real binary's exit code, passed through
	Mode       string    `json:"mode"`       // always ModeAudit in this stage
}

// AppendAuditLog appends one JSON-line entry to path, creating the parent
// directory and the file itself as needed. The whole line (JSON + trailing
// newline) is marshaled in memory first and written with a single Write
// call: on POSIX, an O_APPEND write is only guaranteed not to interleave
// with a concurrent writer's append when it happens as one write(2)
// syscall, so a single in-memory line, one Write, is what keeps concurrent
// xcodebuild/xcrun invocations from corrupting each other's log lines.
func AppendAuditLog(path string, entry AuditEntry) error {
	if path == "" {
		return fmt.Errorf("pathshim: audit log path is required")
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("pathshim: marshal audit entry: %w", err)
	}
	raw = append(raw, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("pathshim: mkdir audit log dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("pathshim: open audit log: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(raw); err != nil {
		return fmt.Errorf("pathshim: write audit log: %w", err)
	}
	return nil
}
