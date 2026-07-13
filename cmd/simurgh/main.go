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
		}
	}

	fmt.Fprintf(os.Stderr, "simurgh %s (pre-v0 scaffold; no commands implemented yet)\n", version)
}

func printHelp() {
	fmt.Println(`simurgh — iOS simulator lease broker (pre-v0 scaffold)

Usage:
  simurgh daemon run
  simurgh doctor [--json]
  simurgh [help]

Daemon serves the Unix socket IPC endpoint (daemon.ping).
Doctor checks Xcode, simctl, iOS runtimes, socket perms, and daemon liveness.
See docs/ROADMAP.md for the build plan.`)
}
