package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/RoshanDewmina/simurgh/internal/broker"
)

func runPrune(args []string) int {
	var apply, asJSON bool
	for _, a := range args {
		switch a {
		case "--apply":
			apply = true
		case "--json":
			asJSON = true
		case "-h", "--help", "help":
			fmt.Println("usage: simurgh prune [--apply] [--json]")
			return 0
		default:
			printErrorTriple("INVALID_INPUT", fmt.Sprintf("unknown flag %q", a), "simurgh prune [--apply] [--json]", asJSON)
			return 3
		}
	}

	if apply {
		return runPruneApply(asJSON)
	}
	return runPrunePlan(asJSON)
}

func runPrunePlan(asJSON bool) int {
	raw, err := dialAndCall(context.Background(), "prune.plan", map[string]any{})
	if err != nil {
		printIPCError(err, asJSON)
		return exitCodeForIPCError(err)
	}
	if asJSON {
		var pretty any
		if err := json.Unmarshal(raw, &pretty); err != nil {
			fmt.Fprintf(os.Stderr, "simurgh prune: %v\n", err)
			return 5
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(pretty)
		return 0
	}

	var plan struct {
		Held               []broker.Lease `json:"held"`
		Orphans            []string       `json:"orphans"`
		Active             []broker.Lease `json:"active"`
		OrphanScanDeferred bool           `json:"orphanScanDeferred"`
	}
	if err := json.Unmarshal(raw, &plan); err != nil {
		fmt.Fprintf(os.Stderr, "simurgh prune: %v\n", err)
		return 5
	}
	fmt.Printf("held=%d orphans=%d active=%d\n", len(plan.Held), len(plan.Orphans), len(plan.Active))
	if plan.OrphanScanDeferred {
		fmt.Println("note: orphan scan deferred (daemon still within post-start reconciliation window)")
	}
	for _, l := range plan.Held {
		udid := ""
		if l.Device != nil {
			udid = l.Device.UDID
		}
		fmt.Printf("  held  %s state=%s udid=%s\n", l.ID, l.State, udid)
	}
	for _, u := range plan.Orphans {
		fmt.Printf("  orphan %s\n", u)
	}
	for _, l := range plan.Active {
		udid := ""
		if l.Device != nil {
			udid = l.Device.UDID
		}
		fmt.Printf("  active %s state=%s udid=%s\n", l.ID, l.State, udid)
	}
	return 0
}

func runPruneApply(asJSON bool) int {
	raw, err := dialAndCall(context.Background(), "prune.apply", map[string]any{})
	if err != nil {
		printIPCError(err, asJSON)
		return exitCodeForIPCError(err)
	}
	if asJSON {
		var pretty any
		if err := json.Unmarshal(raw, &pretty); err != nil {
			fmt.Fprintf(os.Stderr, "simurgh prune: %v\n", err)
			return 5
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(pretty)
		return 0
	}
	var summary struct {
		HeldReclaimed  int      `json:"heldReclaimed"`
		OrphansDeleted int      `json:"orphansDeleted"`
		Errors         []string `json:"errors"`
	}
	if err := json.Unmarshal(raw, &summary); err != nil {
		fmt.Fprintf(os.Stderr, "simurgh prune: %v\n", err)
		return 5
	}
	fmt.Printf("heldReclaimed=%d orphansDeleted=%d errors=%d\n",
		summary.HeldReclaimed, summary.OrphansDeleted, len(summary.Errors))
	for _, e := range summary.Errors {
		fmt.Printf("  error: %s\n", e)
	}
	return 0
}
