package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/RoshanDewmina/simurgh/internal/cli"
)

func runInit(args []string) int {
	var force, asJSON bool
	for _, a := range args {
		switch a {
		case "--force":
			force = true
		case "--json":
			asJSON = true
		case "-h", "--help", "help":
			fmt.Println(`usage: simurgh init [--force] [--json]
Write .simurgh.yaml, register simurgh mcp in .mcp.json, and print an agent rules snippet.
Idempotent by default; --force overwrites an existing .simurgh.yaml.`)
			return 0
		default:
			fmt.Fprintf(os.Stderr, "usage: simurgh init [--force] [--json]\n")
			return 3
		}
	}

	yamlPath := ".simurgh.yaml"
	mcpPath := ".mcp.json"
	entry := cli.MCPServerEntry{
		Type:    "stdio",
		Command: "simurgh",
		Args:    []string{"mcp"},
		Env:     map[string]string{},
	}

	_, yamlStatErr := os.Stat(yamlPath)
	yamlExisted := yamlStatErr == nil

	yamlWritten, err := cli.WriteSimurghYAML(yamlPath, force)
	if err != nil {
		fmt.Fprintf(os.Stderr, "simurgh init: write %s: %v\n", yamlPath, err)
		return 1
	}

	_, mcpStatErr := os.Stat(mcpPath)
	mcpExisted := mcpStatErr == nil

	mcpChanged, err := cli.UpsertMCPEntry(mcpPath, "simurgh", entry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "simurgh init: upsert %s: %v\n", mcpPath, err)
		return 1
	}

	snippet := cli.AgentRulesSnippet()
	report := initReport{
		SimurghYAML: fileAction(yamlPath, yamlWritten, yamlExisted),
		MCPJSON:     mcpAction(mcpPath, mcpChanged, mcpExisted),
		AgentRules:  snippet,
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(os.Stderr, "simurgh init: %v\n", err)
			return 1
		}
		return 0
	}

	fmt.Println(report.SimurghYAML.Status, report.SimurghYAML.Path)
	fmt.Println(report.MCPJSON.Status, report.MCPJSON.Path)
	fmt.Println()
	fmt.Println("--- agent rules (paste into AGENTS.md / CLAUDE.md) ---")
	fmt.Print(snippet)
	return 0
}

type initReport struct {
	SimurghYAML initFileAction `json:"simurghYaml"`
	MCPJSON     initFileAction `json:"mcpJson"`
	AgentRules  string         `json:"agentRules"`
}

type initFileAction struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

func fileAction(path string, written, existed bool) initFileAction {
	switch {
	case !written:
		return initFileAction{Path: path, Status: "exists (skipped)"}
	case existed:
		return initFileAction{Path: path, Status: "overwrote"}
	default:
		return initFileAction{Path: path, Status: "created"}
	}
}

func mcpAction(path string, changed, existed bool) initFileAction {
	switch {
	case !changed:
		return initFileAction{Path: path, Status: "unchanged"}
	case !existed:
		return initFileAction{Path: path, Status: "created"}
	default:
		return initFileAction{Path: path, Status: "updated"}
	}
}
