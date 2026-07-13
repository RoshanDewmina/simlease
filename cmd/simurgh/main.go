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
		}
	}

	fmt.Fprintf(os.Stderr, "simurgh %s (pre-v0 scaffold; no commands implemented yet)\n", version)
}

func printHelp() {
	fmt.Println(`simurgh — iOS simulator lease broker (pre-v0 scaffold)

Usage:
  simurgh [help]

This binary is a placeholder. Daemon, CLI, and MCP commands arrive in M0+ milestones.
See docs/ROADMAP.md for the build plan.`)
}
