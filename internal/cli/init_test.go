package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteSimurghYAML_CreatesThenSkipsWithoutForce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".simurgh.yaml")

	written, err := WriteSimurghYAML(path, false)
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	if !written {
		t.Fatal("first write: want written=true")
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after first write: %v", err)
	}
	if !strings.Contains(string(first), "platform:") || !strings.Contains(string(first), "iPhone 17 Pro") {
		t.Fatalf("unexpected yaml content:\n%s", first)
	}
	if !strings.Contains(string(first), "labels:") {
		t.Fatalf("missing labels:\n%s", first)
	}

	written, err = WriteSimurghYAML(path, false)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if written {
		t.Fatal("second write without force: want written=false")
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after second write: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("file changed without force:\n before=%q\n after=%q", first, second)
	}
}

func TestWriteSimurghYAML_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".simurgh.yaml")
	if err := os.WriteFile(path, []byte("custom: true\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	written, err := WriteSimurghYAML(path, true)
	if err != nil {
		t.Fatalf("force write: %v", err)
	}
	if !written {
		t.Fatal("force write: want written=true")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(got), "custom:") {
		t.Fatalf("force did not overwrite custom content:\n%s", got)
	}
	if !strings.Contains(string(got), "iPhone 17 Pro") {
		t.Fatalf("force write missing default model:\n%s", got)
	}
}

func TestAgentRulesSnippet(t *testing.T) {
	s := AgentRulesSnippet()
	if !strings.Contains(s, "lease_acquire") {
		t.Fatalf("snippet missing lease_acquire:\n%s", s)
	}
	if !strings.Contains(strings.ToLower(s), "never pick a udid") {
		t.Fatalf("snippet missing never-pick-UDID guidance:\n%s", s)
	}
	if !strings.Contains(s, "lease_release") && !strings.Contains(strings.ToLower(s), "release") {
		t.Fatalf("snippet missing release guidance:\n%s", s)
	}
}

func TestUpsertMCPEntry_CreatesFreshFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".mcp.json")
	entry := defaultSimurghMCPEntry()

	changed, err := UpsertMCPEntry(path, "simurgh", entry)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if !changed {
		t.Fatal("want changed=true on create")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var doc struct {
		MCPServers map[string]MCPServerEntry `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse: %v\n%s", err, raw)
	}
	got, ok := doc.MCPServers["simurgh"]
	if !ok {
		t.Fatalf("missing simurgh entry:\n%s", raw)
	}
	if got.Type != "stdio" || got.Command != "simurgh" || len(got.Args) != 1 || got.Args[0] != "mcp" {
		t.Fatalf("unexpected entry: %+v", got)
	}
	if got.Env == nil {
		t.Fatal("env should be present (empty object), not nil")
	}
}

func TestUpsertMCPEntry_PreservesOtherEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".mcp.json")
	otherRaw := []byte(`{
  "type": "stdio",
  "command": "other",
  "args": ["serve"],
  "env": {"A": "1"}
}`)
	seed := []byte(`{
  "mcpServers": {
    "other-server": {
      "type": "stdio",
      "command": "other",
      "args": ["serve"],
      "env": {"A": "1"}
    }
  }
}
`)
	if err := os.WriteFile(path, seed, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	changed, err := UpsertMCPEntry(path, "simurgh", defaultSimurghMCPEntry())
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if !changed {
		t.Fatal("want changed=true when adding simurgh")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var doc struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse: %v\n%s", err, raw)
	}
	other, ok := doc.MCPServers["other-server"]
	if !ok {
		t.Fatalf("other-server missing:\n%s", raw)
	}
	// Canonicalize both sides so whitespace differences in the container don't
	// hide a real mutation of the other entry's fields.
	var want, got any
	if err := json.Unmarshal(otherRaw, &want); err != nil {
		t.Fatalf("parse want: %v", err)
	}
	if err := json.Unmarshal(other, &got); err != nil {
		t.Fatalf("parse got: %v", err)
	}
	wantBytes, _ := json.Marshal(want)
	gotBytes, _ := json.Marshal(got)
	if string(wantBytes) != string(gotBytes) {
		t.Fatalf("other-server changed:\n want=%s\n got=%s", wantBytes, gotBytes)
	}
	if _, ok := doc.MCPServers["simurgh"]; !ok {
		t.Fatalf("simurgh missing:\n%s", raw)
	}
}

func TestUpsertMCPEntry_IdempotentSecondCall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".mcp.json")
	entry := defaultSimurghMCPEntry()

	if _, err := UpsertMCPEntry(path, "simurgh", entry); err != nil {
		t.Fatalf("first: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	changed, err := UpsertMCPEntry(path, "simurgh", entry)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if changed {
		t.Fatal("second identical upsert: want changed=false")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("file rewritten on no-op upsert:\n before=%q\n after=%q", before, after)
	}
}

func TestUpsertMCPEntry_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".mcp.json")
	seed := []byte("{not valid json")
	if err := os.WriteFile(path, seed, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	changed, err := UpsertMCPEntry(path, "simurgh", defaultSimurghMCPEntry())
	if err == nil {
		t.Fatal("want error for malformed JSON")
	}
	if changed {
		t.Fatal("malformed: want changed=false")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(after) != string(seed) {
		t.Fatalf("malformed file was overwritten:\n before=%q\n after=%q", seed, after)
	}
}

func defaultSimurghMCPEntry() MCPServerEntry {
	return MCPServerEntry{
		Type:    "stdio",
		Command: "simurgh",
		Args:    []string{"mcp"},
		Env:     map[string]string{},
	}
}
