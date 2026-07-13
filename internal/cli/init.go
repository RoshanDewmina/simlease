package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
)

// DefaultSimurghYAML is the minimal project config written by WriteSimurghYAML.
// Fields mirror broker.DeviceSpec (platform, model, runtime) plus labels.
const DefaultSimurghYAML = `spec:
  platform: iOS
  model: iPhone 17 Pro
  runtime: ""
labels: {}
`

// MCPServerEntry matches the confirmed .mcp.json stdio server schema.
type MCPServerEntry struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

// WriteSimurghYAML writes the default .simurgh.yaml if path does not exist,
// or always when force is true. Returns whether a write occurred.
func WriteSimurghYAML(path string, force bool) (written bool, err error) {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return false, nil
		} else if !os.IsNotExist(err) {
			return false, err
		}
	}
	if err := os.WriteFile(path, []byte(DefaultSimurghYAML), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// AgentRulesSnippet returns a short, paste-ready agent guidance block for
// AGENTS.md / CLAUDE.md. It mirrors the M3.1 MCP tool discipline.
func AgentRulesSnippet() string {
	return `## Simurgh (iOS Simulator leases)

Use the Simurgh MCP tools for all simulator work: lease_acquire, lease_renew,
lease_release, lease_status, pool_status.
Never pick a UDID from simctl yourself — always go through lease_acquire.
Always call lease_release when done so the device is torn down and capacity
returns to the pool.
Leases are session-bound by default: if this session crashes or ends without
releasing, the lease is reclaimed automatically. Do not pass detach=true
unless a lease must deliberately outlive the session.
If using the CLI instead of MCP, acquire with --rm so the lease dies with
your shell session.
`
}

// UpsertMCPEntry upserts name -> entry under mcpServers in path.
// Creates the file when missing. Never overwrites a malformed existing file.
// changed is false when the entry already matches (no write).
func UpsertMCPEntry(path string, name string, entry MCPServerEntry) (changed bool, err error) {
	if entry.Env == nil {
		entry.Env = map[string]string{}
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return false, err
		}
		return writeMCPConfig(path, map[string]json.RawMessage{
			name: mustMarshalEntry(entry),
		})
	}

	var doc struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}
	if doc.MCPServers == nil {
		doc.MCPServers = make(map[string]json.RawMessage)
	}

	if existing, ok := doc.MCPServers[name]; ok {
		var cur MCPServerEntry
		if err := json.Unmarshal(existing, &cur); err == nil {
			if cur.Env == nil {
				cur.Env = map[string]string{}
			}
			if reflect.DeepEqual(cur, entry) {
				return false, nil
			}
		}
	}

	doc.MCPServers[name] = mustMarshalEntry(entry)
	return writeMCPConfig(path, doc.MCPServers)
}

func mustMarshalEntry(entry MCPServerEntry) json.RawMessage {
	b, err := json.Marshal(entry)
	if err != nil {
		panic(err) // unreachable for MCPServerEntry
	}
	return b
}

func writeMCPConfig(path string, servers map[string]json.RawMessage) (bool, error) {
	doc := struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}{MCPServers: servers}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return false, err
	}
	return true, nil
}
