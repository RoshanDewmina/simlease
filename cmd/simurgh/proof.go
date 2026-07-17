package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/RoshanDewmina/simurgh/internal/broker"
	"github.com/RoshanDewmina/simurgh/internal/proof"
)

const (
	exitProofOK          = 0
	exitProofError       = 1
	exitProofUnsatisfied = 3
)

func runProof(args []string) int {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "usage: simurgh proof status [flags]\n")
		return exitProofError
	}
	switch args[0] {
	case "status":
		return runProofStatus(args[1:])
	case "-h", "--help", "help":
		printProofHelp()
		return exitProofOK
	default:
		fmt.Fprintf(os.Stderr, "usage: simurgh proof status [flags]\n")
		return exitProofError
	}
}

func printProofHelp() {
	fmt.Println(`usage: simurgh proof status [--lease <id> | --lease-root <path>] [--policy <path>] [--tier quick|full] [--json]

Advisory check: does existing lease evidence satisfy .simurgh/verification.yaml?
Read-only — never creates, modifies, or deletes files. Does not enforce done.

Exit codes:
  0  satisfied, or no policy configured
  3  policy present but unsatisfied (advisory signal)
  1  error (bad yaml, unknown step, unresolvable lease)`)
}

func runProofStatus(args []string) int {
	var (
		leaseID    string
		leaseRoot  string
		policyPath string
		tier       string
		asJSON     bool
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help" || a == "help":
			printProofHelp()
			return exitProofOK
		case a == "--json":
			asJSON = true
		case a == "--lease":
			if i+1 >= len(args) {
				return proofErr(asJSON, "INVALID_INPUT", "--lease requires an id", "simurgh proof status --lease <id>")
			}
			i++
			leaseID = args[i]
		case strings.HasPrefix(a, "--lease="):
			leaseID = strings.TrimPrefix(a, "--lease=")
		case a == "--lease-root":
			if i+1 >= len(args) {
				return proofErr(asJSON, "INVALID_INPUT", "--lease-root requires a path", "simurgh proof status --lease-root <path>")
			}
			i++
			leaseRoot = args[i]
		case strings.HasPrefix(a, "--lease-root="):
			leaseRoot = strings.TrimPrefix(a, "--lease-root=")
		case a == "--policy":
			if i+1 >= len(args) {
				return proofErr(asJSON, "INVALID_INPUT", "--policy requires a path", "simurgh proof status --policy <path>")
			}
			i++
			policyPath = args[i]
		case strings.HasPrefix(a, "--policy="):
			policyPath = strings.TrimPrefix(a, "--policy=")
		case a == "--tier":
			if i+1 >= len(args) {
				return proofErr(asJSON, "INVALID_INPUT", "--tier requires quick|full", "simurgh proof status --tier quick|full")
			}
			i++
			tier = args[i]
		case strings.HasPrefix(a, "--tier="):
			tier = strings.TrimPrefix(a, "--tier=")
		case strings.HasPrefix(a, "-"):
			return proofErr(asJSON, "INVALID_INPUT", fmt.Sprintf("unknown flag %q", a), "simurgh proof status --help")
		default:
			return proofErr(asJSON, "INVALID_INPUT", fmt.Sprintf("unexpected argument %q", a), "simurgh proof status --help")
		}
	}

	if leaseID != "" && leaseRoot != "" {
		return proofErr(asJSON, "INVALID_INPUT", "use either --lease or --lease-root, not both", "simurgh proof status --lease <id>")
	}
	if tier != "" && tier != proof.TierQuick && tier != proof.TierFull {
		return proofErr(asJSON, "INVALID_INPUT", fmt.Sprintf("unknown tier %q", tier), "use --tier quick|full")
	}

	if policyPath == "" {
		policyPath = proof.DefaultPolicyPath
	}

	pol, err := proof.LoadPolicy(policyPath)
	if errors.Is(err, proof.ErrNoPolicy) {
		msg := "no policy configured"
		if asJSON {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
				"configured": false,
				"message":    msg,
				"satisfied":  true,
			})
		} else {
			fmt.Println(msg)
		}
		return exitProofOK
	}
	if err != nil {
		return proofPolicyErr(asJSON, err)
	}

	if leaseRoot == "" {
		if leaseID == "" {
			return proofErr(asJSON, "INVALID_INPUT", "lease root required when a policy is configured", "simurgh proof status --lease <id> | --lease-root <path>")
		}
		root, code := resolveProofLeaseRoot(leaseID, asJSON)
		if code != 0 {
			return code
		}
		leaseRoot = root
	}
	absRoot, err := filepath.Abs(leaseRoot)
	if err != nil {
		return proofErr(asJSON, "INVALID_INPUT", fmt.Sprintf("resolve lease-root: %v", err), "pass an absolute --lease-root path")
	}
	st, err := os.Stat(absRoot)
	if err != nil || !st.IsDir() {
		return proofErr(asJSON, "INVALID_INPUT", fmt.Sprintf("lease-root %q is not a directory", leaseRoot), "pass an existing lease directory")
	}

	ev, err := proof.ScanEvidence(absRoot)
	if err != nil {
		return proofErr(asJSON, "INTERNAL", err.Error(), "ensure pathshim/audit.log is valid JSONL if present")
	}
	rep, err := proof.Evaluate(pol, tier, ev)
	if err != nil {
		return proofPolicyErr(asJSON, err)
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fmt.Fprintf(os.Stderr, "simurgh proof status: %v\n", err)
			return exitProofError
		}
	} else {
		printProofReport(rep)
	}
	if rep.Satisfied {
		return exitProofOK
	}
	return exitProofUnsatisfied
}

func resolveProofLeaseRoot(leaseID string, asJSON bool) (string, int) {
	raw, err := dialAndCall(context.Background(), "lease.get", map[string]any{"id": leaseID})
	if err != nil {
		printIPCError(err, asJSON)
		return "", exitProofError
	}
	var wrap struct {
		Lease broker.Lease `json:"lease"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return "", proofErr(asJSON, "INTERNAL", fmt.Sprintf("decode lease: %v", err), "retry; if persistent, restart the daemon")
	}
	root, err := leaseRootFromEnv(wrap.Lease.Env.Env)
	if err != nil {
		return "", proofErr(asJSON, "INVALID_INPUT", err.Error(), "wait for acquire to finish; lease must be ACTIVE")
	}
	return root, 0
}

func printProofReport(rep proof.Report) {
	fmt.Printf("policy_version: %d\n", rep.PolicyVersion)
	fmt.Printf("tier: %s\n", rep.Tier)
	for _, s := range rep.Steps {
		status := "missing"
		if s.Satisfied {
			status = "satisfied"
		}
		fmt.Printf("  %s: %s — %s\n", s.Name, status, s.Reason)
	}
	if rep.Satisfied {
		fmt.Println("verdict: satisfied")
	} else {
		fmt.Println("verdict: unsatisfied")
	}
}

func proofErr(asJSON bool, code, message, fix string) int {
	printErrorTriple(code, message, fix, asJSON)
	return exitProofError
}

func proofPolicyErr(asJSON bool, err error) int {
	var pe *proof.PolicyError
	if errors.As(err, &pe) {
		return proofErr(asJSON, pe.Code, pe.Message, "fix .simurgh/verification.yaml (see docs/PROOF_GATE.md)")
	}
	return proofErr(asJSON, "INVALID_POLICY", err.Error(), "fix .simurgh/verification.yaml (see docs/PROOF_GATE.md)")
}
