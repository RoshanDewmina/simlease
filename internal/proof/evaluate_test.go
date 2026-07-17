package proof

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScanEvidence(t *testing.T) {
	root := t.TempDir()
	writeAudit(t, root,
		auditLine("xcodebuild", []string{"xcodebuild", "-scheme", "App", "build"}, 0),
		auditLine("xcodebuild", []string{"xcodebuild", "-scheme", "App", "clean"}, 0),
		auditLine("xcodebuild", []string{"xcodebuild", "-scheme", "App", "test", "-only-testing:AppUITests/Login"}, 0),
	)
	mustMkdir(t, filepath.Join(root, "Results", "run-1.xcresult"))
	mustWrite(t, filepath.Join(root, "xcodebuildmcp", "adapter.pid.json"), `{"pid":1,"leaseId":"x"}`)

	ev, err := ScanEvidence(root)
	if err != nil {
		t.Fatalf("ScanEvidence: %v", err)
	}
	if len(ev.AuditEntries) != 3 {
		t.Fatalf("AuditEntries = %d, want 3", len(ev.AuditEntries))
	}
	if !ev.HasXCResult {
		t.Fatal("HasXCResult = false, want true")
	}
	if !ev.HasAdapterMeta {
		t.Fatal("HasAdapterMeta = false, want true")
	}
}

func TestScanEvidenceEmptyLease(t *testing.T) {
	ev, err := ScanEvidence(t.TempDir())
	if err != nil {
		t.Fatalf("ScanEvidence: %v", err)
	}
	if len(ev.AuditEntries) != 0 || ev.HasXCResult || ev.HasAdapterMeta {
		t.Fatalf("unexpected evidence: %+v", ev)
	}
}

func TestEvaluateSteps(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, root string)
		step    string
		wantOK  bool
		wantSub string
	}{
		{
			name: "build via audit",
			setup: func(t *testing.T, root string) {
				writeAudit(t, root, auditLine("xcodebuild", []string{"xcodebuild", "build"}, 0))
			},
			step:    StepBuild,
			wantOK:  true,
			wantSub: "audit",
		},
		{
			name: "build rejects test action",
			setup: func(t *testing.T, root string) {
				writeAudit(t, root, auditLine("xcodebuild", []string{"xcodebuild", "test"}, 0))
			},
			step:    StepBuild,
			wantOK:  false,
			wantSub: "missing",
		},
		{
			name: "build via adapter meta",
			setup: func(t *testing.T, root string) {
				mustWrite(t, filepath.Join(root, "xcodebuildmcp", "config.yaml"), "sessionDefaults: {}\n")
			},
			step:    StepBuild,
			wantOK:  true,
			wantSub: "adapter",
		},
		{
			name: "clean_build needs clean and build",
			setup: func(t *testing.T, root string) {
				writeAudit(t, root,
					auditLine("xcodebuild", []string{"xcodebuild", "clean", "build"}, 0),
				)
			},
			step:    StepCleanBuild,
			wantOK:  true,
			wantSub: "clean",
		},
		{
			name: "clean_build fails without clean",
			setup: func(t *testing.T, root string) {
				writeAudit(t, root, auditLine("xcodebuild", []string{"xcodebuild", "build"}, 0))
			},
			step:    StepCleanBuild,
			wantOK:  false,
			wantSub: "clean",
		},
		{
			name: "unit_tests via audit",
			setup: func(t *testing.T, root string) {
				writeAudit(t, root, auditLine("xcodebuild", []string{"xcodebuild", "test"}, 0))
			},
			step:    StepUnitTests,
			wantOK:  true,
			wantSub: "audit",
		},
		{
			name: "unit_tests via xcresult",
			setup: func(t *testing.T, root string) {
				mustMkdir(t, filepath.Join(root, "Results", "run-1.xcresult"))
			},
			step:    StepUnitTests,
			wantOK:  true,
			wantSub: "xcresult",
		},
		{
			name: "ui_tests requires UITests token",
			setup: func(t *testing.T, root string) {
				writeAudit(t, root, auditLine("xcodebuild", []string{"xcodebuild", "test", "-only-testing:AppTests"}, 0))
			},
			step:    StepUITests,
			wantOK:  false,
			wantSub: "UITests",
		},
		{
			name: "ui_tests satisfied",
			setup: func(t *testing.T, root string) {
				writeAudit(t, root, auditLine("xcodebuild", []string{"xcodebuild", "test", "-only-testing:AppUITests"}, 0))
			},
			step:    StepUITests,
			wantOK:  true,
			wantSub: "UITests",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			tt.setup(t, root)
			ev, err := ScanEvidence(root)
			if err != nil {
				t.Fatal(err)
			}
			got := EvaluateStep(tt.step, ev)
			if got.Satisfied != tt.wantOK {
				t.Fatalf("Satisfied = %v, want %v; reason=%q", got.Satisfied, tt.wantOK, got.Reason)
			}
			if tt.wantSub != "" && !containsFold(got.Reason, tt.wantSub) {
				t.Fatalf("reason %q does not contain %q", got.Reason, tt.wantSub)
			}
		})
	}
}

func TestEvaluateTier(t *testing.T) {
	policy := Policy{
		PolicyVersion:   1,
		Quick:           []string{StepBuild, StepUnitTests},
		Full:            []string{StepCleanBuild, StepUnitTests, StepUITests},
		RequiredForDone: TierQuick,
	}

	t.Run("quick satisfied", func(t *testing.T) {
		root := t.TempDir()
		writeAudit(t, root,
			auditLine("xcodebuild", []string{"xcodebuild", "build"}, 0),
			auditLine("xcodebuild", []string{"xcodebuild", "test"}, 0),
		)
		rep, err := Evaluate(policy, TierQuick, mustScan(t, root))
		if err != nil {
			t.Fatal(err)
		}
		if !rep.Satisfied {
			t.Fatalf("want satisfied, got %+v", rep)
		}
	})

	t.Run("full missing ui_tests", func(t *testing.T) {
		root := t.TempDir()
		writeAudit(t, root,
			auditLine("xcodebuild", []string{"xcodebuild", "clean", "build"}, 0),
			auditLine("xcodebuild", []string{"xcodebuild", "test"}, 0),
		)
		rep, err := Evaluate(policy, TierFull, mustScan(t, root))
		if err != nil {
			t.Fatal(err)
		}
		if rep.Satisfied {
			t.Fatal("want unsatisfied")
		}
		found := false
		for _, s := range rep.Steps {
			if s.Name == StepUITests && !s.Satisfied {
				found = true
			}
		}
		if !found {
			t.Fatalf("ui_tests not reported missing: %+v", rep.Steps)
		}
	})
}

func mustScan(t *testing.T, root string) Evidence {
	t.Helper()
	ev, err := ScanEvidence(root)
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func writeAudit(t *testing.T, leaseRoot string, lines ...string) {
	t.Helper()
	dir := filepath.Join(leaseRoot, "pathshim")
	mustMkdir(t, dir)
	path := filepath.Join(dir, "audit.log")
	var body string
	for _, line := range lines {
		body += line + "\n"
	}
	mustWrite(t, path, body)
}

func auditLine(binary string, argv []string, exit int) string {
	raw, err := json.Marshal(map[string]any{
		"timestamp":  time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC),
		"binary":     binary,
		"argv":       argv,
		"realBinary": "/usr/bin/" + binary,
		"exitCode":   exit,
		"mode":       "audit",
	})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
