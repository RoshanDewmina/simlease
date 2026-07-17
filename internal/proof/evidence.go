package proof

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AuditEntry mirrors pathshim.AuditEntry JSONL fields we care about.
type AuditEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Binary    string    `json:"binary"`
	Argv      []string  `json:"argv"`
	ExitCode  int       `json:"exitCode"`
	Mode      string    `json:"mode"`
}

// Evidence is the read-only scan of a lease root's proof sinks.
type Evidence struct {
	AuditEntries   []AuditEntry
	HasXCResult    bool
	HasAdapterMeta bool
}

// ScanEvidence reads existing lease-root artifacts without creating anything.
func ScanEvidence(leaseRoot string) (Evidence, error) {
	var ev Evidence
	auditPath := filepath.Join(leaseRoot, "pathshim", "audit.log")
	entries, err := readAuditLog(auditPath)
	if err != nil {
		return Evidence{}, err
	}
	ev.AuditEntries = entries

	resultsDir := filepath.Join(leaseRoot, "Results")
	has, err := hasXCResultDir(resultsDir)
	if err != nil {
		return Evidence{}, err
	}
	ev.HasXCResult = has

	adapterDir := filepath.Join(leaseRoot, "xcodebuildmcp")
	ev.HasAdapterMeta = fileExists(filepath.Join(adapterDir, "adapter.pid.json")) ||
		fileExists(filepath.Join(adapterDir, "config.yaml"))
	return ev, nil
}

func readAuditLog(path string) ([]AuditEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("proof: open audit log: %w", err)
	}
	defer f.Close()

	var out []AuditEntry
	sc := bufio.NewScanner(f)
	// xcodebuild argv lines can be long; raise the token limit.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e AuditEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return nil, fmt.Errorf("proof: audit.log line %d: %w", lineNo, err)
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("proof: read audit log: %w", err)
	}
	return out, nil
}

func hasXCResultDir(resultsDir string) (bool, error) {
	entries, err := os.ReadDir(resultsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("proof: read Results: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(e.Name(), ".xcresult") {
			return true, nil
		}
	}
	return false, nil
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func isXcodebuildInvocation(e AuditEntry) bool {
	if e.Binary == "xcodebuild" {
		return true
	}
	for _, a := range e.Argv {
		base := filepath.Base(a)
		if base == "xcodebuild" {
			return true
		}
	}
	return false
}

func argvActions(argv []string) (hasBuild, hasTest, hasClean bool) {
	for _, a := range argv {
		switch a {
		case "build":
			hasBuild = true
		case "test":
			hasTest = true
		case "clean":
			hasClean = true
		}
	}
	return
}

func argvHasUITestsToken(argv []string) bool {
	for _, a := range argv {
		if strings.Contains(a, "UITests") {
			return true
		}
	}
	return false
}
