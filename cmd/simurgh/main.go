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
		}
	}

	fmt.Fprintf(os.Stderr, "simurgh %s (pre-v0 scaffold; no commands implemented yet)\n", version)
}

func printHelp() {
	fmt.Println(`simurgh — iOS simulator lease broker (pre-v0 scaffold)

Usage:
  simurgh daemon run
  simurgh [help]

Daemon serves the Unix socket IPC endpoint (daemon.ping).
See docs/ROADMAP.md for the build plan.`)
}
