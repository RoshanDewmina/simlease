package proof

import "fmt"

// StepResult is one policy step's advisory evaluation outcome.
type StepResult struct {
	Name      string `json:"name"`
	Satisfied bool   `json:"satisfied"`
	Reason    string `json:"reason"`
}

// Report is the machine-readable status for one tier evaluation.
type Report struct {
	PolicyVersion int          `json:"policy_version"`
	Tier          string       `json:"tier"`
	Steps         []StepResult `json:"steps"`
	Satisfied     bool         `json:"satisfied"`
}

// Evaluate checks policy steps for tier against scanned evidence.
func Evaluate(policy Policy, tier string, ev Evidence) (Report, error) {
	if tier == "" {
		tier = policy.RequiredForDone
	}
	steps, err := policy.StepsForTier(tier)
	if err != nil {
		return Report{}, err
	}
	rep := Report{
		PolicyVersion: policy.PolicyVersion,
		Tier:          tier,
		Steps:         make([]StepResult, 0, len(steps)),
		Satisfied:     true,
	}
	for _, name := range steps {
		sr := EvaluateStep(name, ev)
		if !sr.Satisfied {
			rep.Satisfied = false
		}
		rep.Steps = append(rep.Steps, sr)
	}
	return rep, nil
}

// EvaluateStep applies v1 evidence heuristics for a single step name.
func EvaluateStep(name string, ev Evidence) StepResult {
	switch name {
	case StepBuild:
		return evalBuild(ev)
	case StepCleanBuild:
		return evalCleanBuild(ev)
	case StepUnitTests:
		return evalUnitTests(ev)
	case StepUITests:
		return evalUITests(ev)
	default:
		return StepResult{
			Name:      name,
			Satisfied: false,
			Reason:    fmt.Sprintf("unknown step %q", name),
		}
	}
}

func evalBuild(ev Evidence) StepResult {
	if ok, reason := findSuccessfulBuild(ev); ok {
		return StepResult{Name: StepBuild, Satisfied: true, Reason: reason}
	}
	if ev.HasAdapterMeta {
		return StepResult{
			Name:      StepBuild,
			Satisfied: true,
			Reason:    "adapter session meta present under xcodebuildmcp/",
		}
	}
	return StepResult{
		Name:      StepBuild,
		Satisfied: false,
		Reason:    "missing: no successful xcodebuild build (non-test) in audit.log and no adapter session meta",
	}
}

func evalCleanBuild(ev Evidence) StepResult {
	buildOK, buildReason := findSuccessfulBuild(ev)
	if !buildOK && ev.HasAdapterMeta {
		buildOK = true
		buildReason = "adapter session meta present under xcodebuildmcp/"
	}
	cleanOK, cleanReason := findSuccessfulClean(ev)
	if buildOK && cleanOK {
		return StepResult{
			Name:      StepCleanBuild,
			Satisfied: true,
			Reason:    fmt.Sprintf("%s; %s", buildReason, cleanReason),
		}
	}
	if !buildOK && !cleanOK {
		return StepResult{
			Name:      StepCleanBuild,
			Satisfied: false,
			Reason:    "missing: no successful build evidence and no successful clean in audit.log",
		}
	}
	if !buildOK {
		return StepResult{
			Name:      StepCleanBuild,
			Satisfied: false,
			Reason:    "missing: clean observed, but no successful build evidence",
		}
	}
	return StepResult{
		Name:      StepCleanBuild,
		Satisfied: false,
		Reason:    "missing: build evidence present, but no successful clean action in audit.log",
	}
}

func evalUnitTests(ev Evidence) StepResult {
	if ok, reason := findSuccessfulTest(ev, false); ok {
		return StepResult{Name: StepUnitTests, Satisfied: true, Reason: reason}
	}
	if ev.HasXCResult {
		return StepResult{
			Name:      StepUnitTests,
			Satisfied: true,
			Reason:    "Results/*.xcresult directory present",
		}
	}
	return StepResult{
		Name:      StepUnitTests,
		Satisfied: false,
		Reason:    "missing: no successful xcodebuild test in audit.log and no Results/*.xcresult",
	}
}

func evalUITests(ev Evidence) StepResult {
	if ok, reason := findSuccessfulTest(ev, true); ok {
		return StepResult{Name: StepUITests, Satisfied: true, Reason: reason}
	}
	return StepResult{
		Name:      StepUITests,
		Satisfied: false,
		Reason:    "missing: no successful xcodebuild test whose argv references a UITests target/plan",
	}
}

func findSuccessfulBuild(ev Evidence) (bool, string) {
	for _, e := range ev.AuditEntries {
		if e.ExitCode != 0 || !isXcodebuildInvocation(e) {
			continue
		}
		hasBuild, hasTest, _ := argvActions(e.Argv)
		if hasBuild && !hasTest {
			return true, "audit.log: successful xcodebuild build (non-test)"
		}
	}
	return false, ""
}

func findSuccessfulClean(ev Evidence) (bool, string) {
	for _, e := range ev.AuditEntries {
		if e.ExitCode != 0 || !isXcodebuildInvocation(e) {
			continue
		}
		_, _, hasClean := argvActions(e.Argv)
		if hasClean {
			return true, "audit.log: successful xcodebuild clean"
		}
	}
	return false, ""
}

func findSuccessfulTest(ev Evidence, requireUITests bool) (bool, string) {
	for _, e := range ev.AuditEntries {
		if e.ExitCode != 0 || !isXcodebuildInvocation(e) {
			continue
		}
		_, hasTest, _ := argvActions(e.Argv)
		if !hasTest {
			continue
		}
		if requireUITests {
			if !argvHasUITestsToken(e.Argv) {
				continue
			}
			return true, "audit.log: successful xcodebuild test referencing UITests"
		}
		return true, "audit.log: successful xcodebuild test"
	}
	return false, ""
}
