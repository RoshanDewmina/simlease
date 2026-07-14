package xcodebuildmcp

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/RoshanDewmina/simurgh/internal/broker"
	"github.com/RoshanDewmina/simurgh/internal/isolator"
)

func activeLease(id, udid string) broker.Lease {
	return broker.Lease{
		ID:    id,
		State: broker.LeaseActive,
		Device: &broker.Device{
			UDID:   udid,
			Name:   "simurgh-clone[" + id + "]",
			Booted: true,
		},
	}
}

func TestXcodeBuildMCPConfigBindsIsolation(t *testing.T) {
	root := t.TempDir()
	lease := activeLease("lease-a", "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE")
	b, err := BuildBindings(lease, root)
	if err != nil {
		t.Fatalf("BuildBindings: %v", err)
	}
	wantKeys := []string{
		"SIMURGH_LEASE_ID",
		"SIMURGH_UDID",
		"SIMURGH_DERIVED_DATA",
		"CLANG_MODULE_CACHE_PATH",
		"SIMURGH_RESULT_BUNDLE_DIR",
		"SIMURGH_SWIFTPM_DIR",
		"XCODEBUILDMCP_SIMULATOR_ID",
		"XCODEBUILDMCP_DERIVED_DATA_PATH",
	}
	for _, k := range wantKeys {
		if b.Env[k] == "" {
			t.Fatalf("missing env %s", k)
		}
	}
	if b.Env["SIMURGH_LEASE_ID"] != "lease-a" {
		t.Fatalf("SIMURGH_LEASE_ID=%q", b.Env["SIMURGH_LEASE_ID"])
	}
	if b.Env["SIMURGH_UDID"] != lease.Device.UDID {
		t.Fatalf("SIMURGH_UDID=%q", b.Env["SIMURGH_UDID"])
	}
	if b.Env["XCODEBUILDMCP_SIMULATOR_ID"] != lease.Device.UDID {
		t.Fatalf("XCODEBUILDMCP_SIMULATOR_ID=%q", b.Env["XCODEBUILDMCP_SIMULATOR_ID"])
	}
	if b.Env["SIMURGH_DERIVED_DATA"] != b.Dirs.DerivedData {
		t.Fatalf("derived data mismatch: %q vs %q", b.Env["SIMURGH_DERIVED_DATA"], b.Dirs.DerivedData)
	}
	if b.Env["XCODEBUILDMCP_DERIVED_DATA_PATH"] != b.Dirs.DerivedData {
		t.Fatalf("xbmcp derived data mismatch")
	}
	if err := isolator.ValidateWritableCachesUnderLeaseRoot(b.Dirs); err != nil {
		t.Fatalf("writable caches: %v", err)
	}
	joinedFlags := strings.Join(b.CLIFlags, " ")
	if !strings.Contains(joinedFlags, lease.Device.UDID) || !strings.Contains(joinedFlags, b.Dirs.DerivedData) {
		t.Fatalf("CLIFlags missing bindings: %v", b.CLIFlags)
	}
	joinedExtra := strings.Join(b.ExtraArgs, " ")
	if !strings.Contains(joinedExtra, b.Dirs.SwiftPM) || !strings.Contains(joinedExtra, lease.Device.UDID) {
		t.Fatalf("ExtraArgs missing source-packages/destination: %v", b.ExtraArgs)
	}

	paths, err := WriteEphemeralConfig(b, "xcodebuildmcp", []string{"mcp"})
	if err != nil {
		t.Fatalf("WriteEphemeralConfig: %v", err)
	}
	raw, err := os.ReadFile(paths.MCPJSON)
	if err != nil {
		t.Fatalf("read mcp.json: %v", err)
	}
	text := string(raw)
	for _, k := range wantKeys {
		if !strings.Contains(text, k) {
			t.Fatalf("mcp.json missing %s", k)
		}
	}
	yaml, err := os.ReadFile(paths.SessionYAML)
	if err != nil {
		t.Fatalf("read config.yaml: %v", err)
	}
	if !strings.Contains(string(yaml), "simulatorId:") || !strings.Contains(string(yaml), "derivedDataPath:") {
		t.Fatalf("config.yaml missing session defaults:\n%s", yaml)
	}
}

func TestXcodeBuildMCPRejectsInactiveLease(t *testing.T) {
	root := t.TempDir()
	cases := []broker.Lease{
		{ID: "lease-q", State: broker.LeaseQueued, Device: &broker.Device{UDID: "U"}},
		{ID: "lease-p", State: broker.LeaseProvisioning, Device: &broker.Device{UDID: "U"}},
		{ID: "lease-r", State: broker.LeaseReleased, Device: &broker.Device{UDID: "U"}},
		{ID: "lease-a", State: broker.LeaseActive, Device: nil},
		{ID: "lease-a2", State: broker.LeaseActive, Device: &broker.Device{UDID: ""}},
	}
	for _, lease := range cases {
		if _, err := BuildBindings(lease, root); err == nil {
			t.Fatalf("expected error for state=%s device=%v", lease.State, lease.Device)
		}
	}
}

func TestXcodeBuildMCPNoCommittedUDID(t *testing.T) {
	// Ephemeral config under lease root may contain UDID; repository sources must not.
	udidRE := regexp.MustCompile(`[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}`)
	pkgDir := "."
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(pkgDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if udidRE.Match(raw) {
			t.Fatalf("%s embeds a UDID literal — machine IDs must stay ephemeral", name)
		}
	}

	root := t.TempDir()
	lease := activeLease("lease-ephemeral", "11111111-2222-3333-4444-555555555555")
	b, err := BuildBindings(lease, root)
	if err != nil {
		t.Fatalf("BuildBindings: %v", err)
	}
	paths, err := WriteEphemeralConfig(b, "xcodebuildmcp", []string{"mcp"})
	if err != nil {
		t.Fatalf("WriteEphemeralConfig: %v", err)
	}
	// Ephemeral files live under lease root, not the repo.
	if !strings.HasPrefix(paths.MCPJSON, root) {
		t.Fatalf("mcp.json not under lease root: %s", paths.MCPJSON)
	}
	raw, _ := os.ReadFile(paths.MCPJSON)
	if !strings.Contains(string(raw), lease.Device.UDID) {
		t.Fatalf("ephemeral mcp.json should bind UDID under lease root")
	}
}
