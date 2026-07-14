package xcodebuildmcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/RoshanDewmina/simurgh/internal/broker"
	"github.com/RoshanDewmina/simurgh/internal/isolator"
)

// Required Simurgh isolation env keys forced into every adapter child.
var requiredSimurghEnv = []string{
	"SIMURGH_LEASE_ID",
	"SIMURGH_UDID",
	"SIMURGH_DERIVED_DATA",
	"CLANG_MODULE_CACHE_PATH",
	"SIMURGH_RESULT_BUNDLE_DIR",
	"SIMURGH_SWIFTPM_DIR",
}

// Bindings are the exact isolation + XcodeBuildMCP session defaults for one lease.
type Bindings struct {
	LeaseID     string
	UDID        string
	Dirs        isolator.LeaseDirs
	Env         map[string]string
	XBMCPEnv    map[string]string // XCODEBUILDMCP_* session-default env
	CLIFlags    []string          // explicit flags supported by installed XcodeBuildMCP CLI
	ExtraArgs   []string          // xcodebuild extraArgs (source-packages + destination)
	DeveloperDir string           // optional DEVELOPER_DIR
}

// BuildBindings derives isolation bindings strictly from an ACTIVE lease and
// isolator lease-root layout. Paths never escape the lease root.
func BuildBindings(lease broker.Lease, leasesRoot string) (Bindings, error) {
	if err := requireActiveLease(lease); err != nil {
		return Bindings{}, err
	}
	if leasesRoot == "" {
		return Bindings{}, fmt.Errorf("xcodebuildmcp: leases root is required")
	}
	dirs, err := isolator.EnsureLeaseDir(leasesRoot, lease.ID)
	if err != nil {
		return Bindings{}, err
	}
	iso := isolator.BuildEnv(dirs, lease.ID, lease.Device.UDID)
	if err := isolator.ValidateWritableCachesUnderLeaseRoot(dirs); err != nil {
		return Bindings{}, err
	}
	for _, k := range requiredSimurghEnv {
		v := iso.Env[k]
		if v == "" {
			return Bindings{}, fmt.Errorf("xcodebuildmcp: missing isolation env %s", k)
		}
		if !pathUnderLeaseRoot(dirs.Root, v) && k != "SIMURGH_LEASE_ID" && k != "SIMURGH_UDID" {
			return Bindings{}, fmt.Errorf("xcodebuildmcp: %s=%q escapes lease root %q", k, v, dirs.Root)
		}
	}

	env := map[string]string{}
	for k, v := range iso.Env {
		env[k] = v
	}
	developerDir := strings.TrimSpace(os.Getenv("DEVELOPER_DIR"))
	if developerDir != "" {
		env["DEVELOPER_DIR"] = developerDir
	}

	destination := "platform=iOS Simulator,id=" + lease.Device.UDID
	extraArgs := []string{
		"-clonedSourcePackagesDirPath", dirs.SwiftPM,
		"-destination", destination,
	}

	xbmcpEnv := map[string]string{
		"XCODEBUILDMCP_SIMULATOR_ID":     lease.Device.UDID,
		"XCODEBUILDMCP_DERIVED_DATA_PATH": dirs.DerivedData,
		"XCODEBUILDMCP_SENTRY_DISABLED":  "true",
	}
	for k, v := range xbmcpEnv {
		env[k] = v
	}

	cliFlags := []string{
		"--simulator-id", lease.Device.UDID,
		"--derived-data-path", dirs.DerivedData,
	}

	return Bindings{
		LeaseID:      lease.ID,
		UDID:         lease.Device.UDID,
		Dirs:         dirs,
		Env:          env,
		XBMCPEnv:     xbmcpEnv,
		CLIFlags:     cliFlags,
		ExtraArgs:    extraArgs,
		DeveloperDir: developerDir,
	}, nil
}

func requireActiveLease(lease broker.Lease) error {
	if lease.ID == "" {
		return fmt.Errorf("xcodebuildmcp: lease id is required")
	}
	if lease.State != broker.LeaseActive {
		return fmt.Errorf("xcodebuildmcp: lease %s is %s (want active)", lease.ID, lease.State)
	}
	if lease.Device == nil || lease.Device.UDID == "" {
		return fmt.Errorf("xcodebuildmcp: lease %s has no ACTIVE device/UDID", lease.ID)
	}
	return nil
}

func pathUnderLeaseRoot(root, path string) bool {
	if path == "" || root == "" {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(absRoot), filepath.Clean(absPath))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
