package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

func runHold(args []string) int {
	var (
		leaseID string
		extend  time.Duration
		asJSON  bool
		posSeen bool
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--duration":
			if i+1 >= len(args) {
				printErrorTriple("INVALID_INPUT", "--duration requires a duration", "simurgh hold <lease-id> [--duration 30m]", asJSON)
				return 3
			}
			i++
			d, err := time.ParseDuration(args[i])
			if err != nil {
				printErrorTriple("INVALID_INPUT", fmt.Sprintf("duration: %v", err), "simurgh hold <lease-id> [--duration 30m]", asJSON)
				return 3
			}
			extend = d
		case a == "--json":
			asJSON = true
		case a == "-h" || a == "--help" || a == "help":
			fmt.Println("usage: simurgh hold <lease-id> [--duration 30m] [--json]")
			return 0
		case strings.HasPrefix(a, "-"):
			printErrorTriple("INVALID_INPUT", fmt.Sprintf("unknown flag %q", a), "simurgh hold <lease-id> [--duration 30m]", asJSON)
			return 3
		default:
			if posSeen {
				printErrorTriple("INVALID_INPUT", "only one lease-id allowed", "simurgh hold <lease-id>", asJSON)
				return 3
			}
			leaseID = a
			posSeen = true
		}
	}
	if leaseID == "" {
		printErrorTriple("INVALID_INPUT", "lease-id is required", "simurgh hold <lease-id> [--duration 30m]", asJSON)
		return 3
	}
	params := map[string]any{"id": leaseID}
	if extend > 0 {
		params["duration"] = extend.String()
	}
	raw, err := dialAndCall(context.Background(), "lease.hold", params)
	if err != nil {
		printIPCError(err, asJSON)
		return exitCodeForIPCError(err)
	}
	if err := printLeaseResult(raw, asJSON); err != nil {
		fmt.Fprintf(os.Stderr, "simurgh hold: %v\n", err)
		return 5
	}
	return 0
}

func runUnhold(args []string) int {
	var (
		leaseID string
		asJSON  bool
		posSeen bool
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			asJSON = true
		case a == "-h" || a == "--help" || a == "help":
			fmt.Println("usage: simurgh unhold <lease-id> [--json]")
			return 0
		case strings.HasPrefix(a, "-"):
			printErrorTriple("INVALID_INPUT", fmt.Sprintf("unknown flag %q", a), "simurgh unhold <lease-id>", asJSON)
			return 3
		default:
			if posSeen {
				printErrorTriple("INVALID_INPUT", "only one lease-id allowed", "simurgh unhold <lease-id>", asJSON)
				return 3
			}
			leaseID = a
			posSeen = true
		}
	}
	if leaseID == "" {
		printErrorTriple("INVALID_INPUT", "lease-id is required", "simurgh unhold <lease-id>", asJSON)
		return 3
	}
	raw, err := dialAndCall(context.Background(), "lease.unhold", map[string]any{"id": leaseID})
	if err != nil {
		printIPCError(err, asJSON)
		return exitCodeForIPCError(err)
	}
	if err := printLeaseResult(raw, asJSON); err != nil {
		fmt.Fprintf(os.Stderr, "simurgh unhold: %v\n", err)
		return 5
	}
	return 0
}
