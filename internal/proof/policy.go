// Package proof implements the advisory proof-gate v0.1 policy loader and
// evidence evaluator (Option A). It never enforces or mutates lease state.
package proof

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

const (
	PolicyVersionSupported = 1

	TierQuick = "quick"
	TierFull  = "full"

	StepBuild      = "build"
	StepCleanBuild = "clean_build"
	StepUnitTests  = "unit_tests"
	StepUITests    = "ui_tests"

	DefaultPolicyPath = ".simurgh/verification.yaml"
)

// ErrNoPolicy means the policy file was absent (advisory: treat as unconfigured).
var ErrNoPolicy = errors.New("proof: no policy configured")

var knownSteps = map[string]struct{}{
	StepBuild:      {},
	StepCleanBuild: {},
	StepUnitTests:  {},
	StepUITests:    {},
}

// Policy is the validated contents of verification.yaml.
type Policy struct {
	PolicyVersion   int
	Quick           []string
	Full            []string
	RequiredForDone string
}

// PolicyError is a structured policy validation/load failure.
type PolicyError struct {
	Code    string
	Message string
}

func (e *PolicyError) Error() string {
	if e == nil {
		return "proof: policy error"
	}
	return fmt.Sprintf("proof: %s: %s", e.Code, e.Message)
}

type policyFile struct {
	Verification struct {
		PolicyVersion   int      `yaml:"policy_version"`
		Quick           []string `yaml:"quick"`
		Full            []string `yaml:"full"`
		RequiredForDone string   `yaml:"required_for_done"`
	} `yaml:"verification"`
}

// LoadPolicy reads and validates path. Missing file returns ErrNoPolicy.
func LoadPolicy(path string) (Policy, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Policy{}, ErrNoPolicy
		}
		return Policy{}, &PolicyError{Code: "INVALID_POLICY", Message: err.Error()}
	}

	var doc policyFile
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return Policy{}, &PolicyError{
			Code:    "INVALID_POLICY",
			Message: fmt.Sprintf("malformed yaml: %v", err),
		}
	}
	v := doc.Verification
	if v.PolicyVersion != PolicyVersionSupported {
		return Policy{}, &PolicyError{
			Code: "UNSUPPORTED_POLICY_VERSION",
			Message: fmt.Sprintf(
				"unsupported policy_version %d (supported: %d)",
				v.PolicyVersion, PolicyVersionSupported,
			),
		}
	}
	if err := validateTier(TierQuick, v.Quick); err != nil {
		return Policy{}, err
	}
	if err := validateTier(TierFull, v.Full); err != nil {
		return Policy{}, err
	}
	switch v.RequiredForDone {
	case TierQuick, TierFull:
	default:
		return Policy{}, &PolicyError{
			Code:    "UNKNOWN_TIER",
			Message: fmt.Sprintf("required_for_done names undefined tier %q (want quick or full)", v.RequiredForDone),
		}
	}
	return Policy{
		PolicyVersion:   v.PolicyVersion,
		Quick:           append([]string(nil), v.Quick...),
		Full:            append([]string(nil), v.Full...),
		RequiredForDone: v.RequiredForDone,
	}, nil
}

func validateTier(name string, steps []string) error {
	if len(steps) == 0 {
		return &PolicyError{
			Code:    "EMPTY_TIER",
			Message: fmt.Sprintf("tier %q has an empty step list", name),
		}
	}
	for _, step := range steps {
		if _, ok := knownSteps[step]; !ok {
			return &PolicyError{
				Code:    "UNKNOWN_STEP",
				Message: fmt.Sprintf("unknown step %q in tier %q", step, name),
			}
		}
	}
	return nil
}

// StepsForTier returns the step list for tier, or an error if unknown.
func (p Policy) StepsForTier(tier string) ([]string, error) {
	switch tier {
	case TierQuick:
		return append([]string(nil), p.Quick...), nil
	case TierFull:
		return append([]string(nil), p.Full...), nil
	case "":
		return p.StepsForTier(p.RequiredForDone)
	default:
		return nil, &PolicyError{
			Code:    "UNKNOWN_TIER",
			Message: fmt.Sprintf("unknown tier %q (want quick or full)", tier),
		}
	}
}
