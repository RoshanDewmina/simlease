//go:build darwin_sim

package main

import (
	"os"
	"strings"

	xbmcp "github.com/RoshanDewmina/simurgh/internal/integrations/xcodebuildmcp"
)

// applyXBMCPTestCommandOverride and applyTestSkipPressureOverride are the
// darwin_sim-only real implementations of the two test-only env-var seams
// used by cmd/simurgh/daemon.go's runDaemon(): SIMURGH_XBMCP_TEST_COMMAND
// (substitute the XcodeBuildMCP child command) and SIMURGH_TEST_SKIP_PRESSURE
// (bypass the RAM/thermal/disk pressure gate). Both exist solely so
// cmd/simurgh/integration_xcodebuildmcp_daemon_test.go (also darwin_sim-tagged,
// and built with -tags darwin_sim so this file is the one that links in) can
// drive the real daemon-owned adapter without depending on network/npm or
// this dev host's momentary free-RAM headroom.
//
// R-WP6 second-review Fix 2: before this split, both env vars were read
// unconditionally inside runDaemon() with no gate at all — reachable in the
// real `simurgh daemon run` binary via any process env, not just deliberate
// test harnesses. Moving the reads behind this build tag means a normal
// `go build ./...` (no tags — how the shipped binary is built) links
// xbmcp_test_seams_other.go instead, whose functions never call os.Getenv at
// all: the env vars have zero effect regardless of what any process sets,
// because the code path that would read them was never compiled in.
// See xbmcp_test_seams_other.go for the no-op counterpart.

func applyXBMCPTestCommandOverride(adapter *xbmcp.Adapter) {
	if adapter == nil {
		return
	}
	if raw := strings.TrimSpace(os.Getenv("SIMURGH_XBMCP_TEST_COMMAND")); raw != "" {
		adapter.Command = strings.Fields(raw)
	}
}

func applyTestSkipPressureOverride(pressureOK func() (bool, string)) func() (bool, string) {
	if os.Getenv("SIMURGH_TEST_SKIP_PRESSURE") == "1" {
		return func() (bool, string) { return true, "" }
	}
	return pressureOK
}
