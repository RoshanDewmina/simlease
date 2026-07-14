// Package controlplane implements the R-WP3 session/job model and independent
// build-slot scheduler.
//
// Durability: Session, Job, Worker, Workspace, Artifact, and Event state in this
// package is intentionally in-memory and non-durable for R-WP3. Daemon restart
// drops control-plane state; device leases remain owned by internal/broker
// (and its optional journal). SQLite persistence is out of scope (later WP).
//
// Compatibility: existing lease CLI / MCP / IPC methods remain the public
// surface and stay byte-compatible. This package layers Session/Job around
// broker leases via an internal adapter; lease.acquire never returns build_slot
// queue errors — only the build scheduler surfaces ResourceClassBuildSlot.
//
// Device deny order from R-WP2 is unchanged (simulator_slot → provisioning_slot
// → per_spec_slot → pressure). Build grants use resource class build_slot on a
// separate pool (SIMURGH_BUILD_SLOTS).
package controlplane
