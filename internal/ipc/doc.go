// Package ipc implements the versioned newline-delimited JSON protocol
// used between Simurgh clients and the daemon, plus the Unix-socket
// listener, method dispatch server, and client dial/auto-start helper.
// Socket and log paths are always injected by callers; this package never
// resolves ~/.simurgh itself.
package ipc
