package main

import (
	"fmt"
	"os"
)

const version = "0.0.0-dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-h", "--help", "help":
			printHelp()
			return
		case "daemon":
			if len(os.Args) > 2 && os.Args[2] == "run" {
				os.Exit(runDaemon())
			}
			fmt.Fprintf(os.Stderr, "usage: simurgh daemon run\n")
			os.Exit(2)
		case "doctor":
			jsonOut := false
			for _, a := range os.Args[2:] {
				switch a {
				case "--json":
					jsonOut = true
				case "-h", "--help", "help":
					printHelp()
					return
				default:
					fmt.Fprintf(os.Stderr, "usage: simurgh doctor [--json]\n")
					os.Exit(3)
				}
			}
			os.Exit(runDoctor(jsonOut))
		case "acquire":
			os.Exit(runAcquire(os.Args[2:]))
		case "release":
			os.Exit(runRelease(os.Args[2:]))
		case "renew":
			os.Exit(runRenew(os.Args[2:]))
		case "status":
			os.Exit(runStatus(os.Args[2:]))
		case "list":
			os.Exit(runList(os.Args[2:]))
		case "env":
			os.Exit(runEnv(os.Args[2:]))
		case "exec":
			os.Exit(runExec(os.Args[2:]))
		case "warm":
			os.Exit(runWarm(os.Args[2:]))
		case "prune":
			os.Exit(runPrune(os.Args[2:]))
		}
	}

	fmt.Fprintf(os.Stderr, "simurgh %s (pre-v0 scaffold; no commands implemented yet)\n", version)
}

func printHelp() {
	fmt.Println(`simurgh — iOS simulator lease broker (pre-v0 scaffold)

Usage:
  simurgh acquire --model NAME [flags]
  simurgh release [<lease-id>] [--all --label k=v] [--json]
  simurgh renew <lease-id> [--ttl DUR] [--json]
  simurgh status [--queue] [--json]
  simurgh list [--label-selector k=v,...] [--json]
  simurgh env <lease-id> [--json]
  simurgh exec <lease-id> -- <cmd...>
  simurgh warm create --model NAME [--runtime VER] [--json]
  simurgh warm list [--json]
  simurgh warm remove --model NAME [--runtime VER] [--json]
  simurgh prune [--apply] [--json]
  simurgh daemon run
  simurgh doctor [--json]
  simurgh [help]

Acquire/release/renew/status/list are thin IPC clients over the daemon lease RPCs.
warm create|list|remove manages primed shut-down clone sources.
prune plans (and optionally applies) cleanup of orphan devices and held leases.
env prints eval-able isolation exports; exec runs a command with those env vars
(and xcodebuild isolation flags) injected. PATH shim is v0.1 — not built.
Daemon serves the Unix socket IPC endpoint (daemon.ping + lease.* + pool.status + warm.* + prune.*).
Doctor checks Xcode, simctl, iOS runtimes, socket perms, daemon liveness, stale leases, and disk usage.
See docs/ROADMAP.md for the build plan.`)
}
