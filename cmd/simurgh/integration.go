package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	xbmcp "github.com/RoshanDewmina/simurgh/internal/integrations/xcodebuildmcp"
)

func runIntegration(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: simurgh integration xcodebuildmcp start|stop|status --session <id> [--json]")
		return 3
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Println(`usage: simurgh integration xcodebuildmcp start|stop|status --session <id> [--json]
Own XcodeBuildMCP lifecycle for an ACTIVE Simurgh lease (session id accepts the
current lease id compatibility surface). start writes ephemeral MCP config under
the lease root and launches an owned child; stop kills only that child.`)
		return 0
	case "xcodebuildmcp":
		return runIntegrationXcodeBuildMCP(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "simurgh integration: unknown subsystem %q\n", args[0])
		return 3
	}
}

func runIntegrationXcodeBuildMCP(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: simurgh integration xcodebuildmcp start|stop|status --session <id> [--json]")
		return 3
	}
	action := args[0]
	rest := args[1:]
	switch action {
	case "-h", "--help", "help":
		fmt.Println(`usage: simurgh integration xcodebuildmcp start|stop|status --session <id> [--json]`)
		return 0
	case "start", "stop", "status":
		return runXBMCPAction(action, rest)
	default:
		fmt.Fprintf(os.Stderr, "simurgh integration xcodebuildmcp: unknown action %q\n", action)
		return 3
	}
}

func runXBMCPAction(action string, args []string) int {
	var (
		sessionID string
		asJSON    bool
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			asJSON = true
		case a == "--session":
			if i+1 >= len(args) {
				printErrorTriple("INVALID_INPUT", "--session requires a value", "simurgh integration xcodebuildmcp "+action+" --session <id>", asJSON)
				return 3
			}
			i++
			sessionID = args[i]
		case strings.HasPrefix(a, "--session="):
			sessionID = strings.TrimPrefix(a, "--session=")
		case a == "-h" || a == "--help" || a == "help":
			fmt.Println(`usage: simurgh integration xcodebuildmcp ` + action + ` --session <id> [--json]`)
			return 0
		case strings.HasPrefix(a, "-"):
			printErrorTriple("INVALID_INPUT", fmt.Sprintf("unknown flag %q", a), "simurgh integration xcodebuildmcp "+action+" --session <id>", asJSON)
			return 3
		default:
			printErrorTriple("INVALID_INPUT", fmt.Sprintf("unexpected argument %q", a), "simurgh integration xcodebuildmcp "+action+" --session <id>", asJSON)
			return 3
		}
	}
	if sessionID == "" {
		printErrorTriple("INVALID_INPUT", "--session <id> is required (lease id compatibility surface)", "simurgh integration xcodebuildmcp "+action+" --session <id>", asJSON)
		return 3
	}

	// Thin daemon client (R-WP6 review finding B2): Start/Stop/Status now run
	// daemon-side against the one long-lived Adapter constructed at daemon
	// startup, reached the same way lease.* calls already are. This is what
	// makes `start` (one CLI process) followed by a separate `status` CLI
	// process actually see the running child — a per-CLI-call in-process
	// Adapter (the pre-fix behavior) lost its stdin-holding pipe the moment
	// the `start` process exited.
	ctx := context.Background()
	raw, err := dialAndCall(ctx, "integration.xcodebuildmcp."+action, map[string]any{"sessionId": sessionID})
	if err != nil {
		return printXBMCPErr(err, asJSON)
	}

	switch action {
	case "start", "status":
		var st xbmcp.Status
		if uErr := json.Unmarshal(raw, &st); uErr != nil {
			printErrorTriple("INTERNAL", uErr.Error(), "simurgh doctor", asJSON)
			return 5
		}
		return printXBMCPStatus(st, asJSON)
	case "stop":
		if asJSON {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
				"leaseId": sessionID,
				"stopped": true,
			})
		} else {
			fmt.Printf("stopped xcodebuildmcp adapter for %s\n", sessionID)
		}
		return 0
	default:
		return 3
	}
}

func printXBMCPStatus(st xbmcp.Status, asJSON bool) int {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(st)
		return 0
	}
	state := "stopped"
	if st.Running {
		state = "running"
	}
	fmt.Printf("lease=%s state=%s pid=%d version=%s config=%s\n",
		st.LeaseID, state, st.PID, st.Version, st.Config)
	return 0
}

func printXBMCPErr(err error, asJSON bool) int {
	if err == nil {
		return 0
	}
	if _, isRPC := err.(*rpcError); isRPC {
		printIPCError(err, asJSON)
		return exitCodeForIPCError(err)
	}
	msg := err.Error()
	code := "INTERNAL"
	fix := "check daemon lease state and XcodeBuildMCP install"
	switch {
	case strings.Contains(msg, "want active") || strings.Contains(msg, "no ACTIVE"):
		code = "INVALID_STATE"
		fix = "acquire/wait until the lease is ACTIVE with a device"
	case strings.Contains(msg, "daemon is not running") || strings.Contains(msg, "DaemonNotRunning"):
		code = "DAEMON_NOT_RUNNING"
		fix = "simurgh daemon run"
	case strings.Contains(msg, "no owned adapter") || strings.Contains(msg, "already running"):
		code = "INVALID_STATE"
	}
	printErrorTriple(code, msg, fix, asJSON)
	return exitCodeForCode(code)
}

func exitCodeForCode(code string) int {
	switch code {
	case "INVALID_INPUT":
		return 3
	case "INVALID_STATE":
		return 3
	case "DAEMON_NOT_RUNNING":
		return 4
	default:
		return 5
	}
}
