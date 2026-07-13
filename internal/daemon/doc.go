// Package daemon wires the lease broker, reaper, and watcher into IPC handlers
// and the reclaim event loop.
//
// This package is translation and glue only: callers construct Broker /
// Provisioner / Reaper and pass them via Deps. It registers the six lease.*
// IPC methods and runs watcher decisions through Broker.Reclaim then
// Reaper.Reclaim. Orphan scans log only (auto-reclaim deferred to prune).
package daemon
