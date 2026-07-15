//go:build !darwin_sim

package main

import xbmcp "github.com/RoshanDewmina/simurgh/internal/integrations/xcodebuildmcp"

// applyXBMCPTestCommandOverride and applyTestSkipPressureOverride are pure
// no-ops in every non-darwin_sim build, including the real shipped
// `simurgh daemon run` binary (a tagless `go build ./...`). Neither function
// reads SIMURGH_XBMCP_TEST_COMMAND nor SIMURGH_TEST_SKIP_PRESSURE here — the
// env-var-reading code lives only in xbmcp_test_seams_darwin_sim.go, which
// this build never links in. So no process environment, however it was set
// (including a detached daemon inheriting its triggering CLI subprocess's
// env), can substitute the XcodeBuildMCP child command or bypass the
// pressure gate in production. See xbmcp_test_seams_darwin_sim.go for the
// darwin_sim-only real implementation used by CLI regression tests.

func applyXBMCPTestCommandOverride(_ *xbmcp.Adapter) {}

func applyTestSkipPressureOverride(pressureOK func() (bool, string)) func() (bool, string) {
	return pressureOK
}
