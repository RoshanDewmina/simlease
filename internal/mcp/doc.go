// Package mcp implements the Model Context Protocol stdio server for simurgh.
//
// It is a thin translation layer over the daemon's existing lease/pool RPCs
// (lease.acquire|renew|release|get, pool.status), dialed via internal/ipc —
// the same shape as cmd/simurgh's CLI commands, with an MCP tool-call interface
// instead of os.Args flags.
//
// The ten tools (lease_acquire, lease_renew, lease_hold, lease_unhold,
// lease_release, lease_status, pool_status, plus lease-scoped
// xcodebuildmcp_tools / xcodebuildmcp_call / xcodebuildmcp_stop) are the agent
// surface from docs/ARCHITECTURE.md §8 and the issue #4 proxy fix. Tool
// descriptions are agent-facing API surface: they state when to call and what
// not to do (e.g. never pick a UDID from simctl yourself).
package mcp
