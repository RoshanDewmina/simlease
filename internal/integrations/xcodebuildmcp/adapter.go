package xcodebuildmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
	"github.com/RoshanDewmina/simurgh/internal/safety"
)

// DefaultPinnedVersion is the XcodeBuildMCP version Phase A composed with.
// Runtime RecordVersion may override via the installed binary.
const DefaultPinnedVersion = "2.6.2"

// LeaseGetter resolves a lease by ID (daemon IPC in production).
type LeaseGetter func(ctx context.Context, id string) (broker.Lease, error)

// PIDMeta is atomic on-disk ownership metadata for one adapter child.
type PIDMeta struct {
	LeaseID   string    `json:"leaseId"`
	PID       int       `json:"pid"`
	PGID      int       `json:"pgid"`
	StartedAt time.Time `json:"startedAt"`
	Command   []string  `json:"command"`
	Config    string    `json:"configPath"`
	LogPath   string    `json:"logPath"`
	Version   string    `json:"xcodebuildmcpVersion"`
}

// Status is the adapter status for one session/lease.
type Status struct {
	LeaseID  string    `json:"leaseId"`
	Running  bool      `json:"running"`
	PID      int       `json:"pid,omitempty"`
	Config   string    `json:"configPath,omitempty"`
	LogPath  string    `json:"logPath,omitempty"`
	Version  string    `json:"xcodebuildmcpVersion,omitempty"`
	Bindings *Bindings `json:"-"`
}

// Adapter owns XcodeBuildMCP lifecycle for Simurgh leases.
type Adapter struct {
	LeasesRoot string
	GetLease   LeaseGetter
	// Command is the argv used to launch XcodeBuildMCP MCP server.
	// Default: npx -y xcodebuildmcp@<version> mcp
	Command []string
	// Version pinned into launches / metadata when ProbeVersion is unset.
	Version string
	// ProbeVersion optionally discovers the installed version string.
	ProbeVersion func() (string, error)
	// Clock defaults to time.Now.
	Clock func() time.Time
	// LookPath resolves binaries; defaults to exec.LookPath.
	LookPath func(file string) (string, error)

	// stdinMu guards stdinHolders. The daemon constructs exactly one Adapter
	// for its whole lifetime (R-WP6 review finding B2) and reaches it from a
	// goroutine per IPC connection, so Start/Stop for different (or the same)
	// sessions can run concurrently — unlike the pre-B2-fix world where every
	// CLI invocation got its own throwaway *Adapter and this map was never
	// shared across goroutines. All other Adapter fields (LeasesRoot,
	// GetLease, Command, Version, ProbeVersion, Clock, LookPath) are set once
	// at construction and only ever read afterward, so they need no lock.
	stdinMu      sync.Mutex
	stdinHolders map[string]*os.File

	// sessionMu guards sessionLocks. sessionLocks serializes the ENTIRE
	// Start and Stop critical section per sessionID (R-WP6 third-review
	// finding: same-session concurrent Start calls TOCTOU-race the "refuse a
	// second child" guard — readPIDMeta/processAlive check, then real work
	// spawning a child, then writePIDMeta/stdinHolders write, none of it
	// serialized — so two overlapping starts for the same session could both
	// pass the guard and both spawn a child, with the second's metadata
	// write silently orphaning the first; a Stop racing a Start for the same
	// session had the same shape, since Stop's final os.Remove(PIDMeta)
	// never re-verified it was still describing the process it just killed).
	// This lock is IN ADDITION to stdinMu, not a replacement: stdinMu alone
	// already covers the original B2 finding (concurrent Start/Stop for
	// DIFFERENT sessions racing on the shared stdinHolders map access
	// itself), which TestXcodeBuildMCPStdinHoldersConcurrentAccessRace
	// exercises with 8 distinct sessions and must keep passing unchanged.
	// sessionLocks closes the wider, same-session race that a map-access
	// mutex alone cannot: it serializes the whole read-check-spawn-write (or
	// read-kill-remove) sequence, so overlapping Start/Start or Start/Stop
	// calls for one session can never interleave. Locks accumulate one per
	// distinct sessionID ever seen and are never removed, mirroring
	// internal/provisioner.Provisioner.lockForBase — that key space (base
	// device specs) is likewise small and bounded, and this one is bounded
	// by active lease/session IDs.
	sessionMu    sync.Mutex
	sessionLocks map[string]*sync.Mutex
}

// NewAdapter constructs an Adapter with defaults.
func NewAdapter(leasesRoot string, getLease LeaseGetter) *Adapter {
	return &Adapter{
		LeasesRoot:   leasesRoot,
		GetLease:     getLease,
		Version:      DefaultPinnedVersion,
		Clock:        time.Now,
		LookPath:     exec.LookPath,
		stdinHolders: map[string]*os.File{},
		sessionLocks: map[string]*sync.Mutex{},
	}
}

// lockForSession returns the mutex serializing Start/Stop for sessionID,
// creating it on first use. Distinct sessionIDs get distinct locks, so
// unrelated sessions are never serialized against each other — only
// same-session Start/Start or Start/Stop calls block on one another.
func (a *Adapter) lockForSession(sessionID string) *sync.Mutex {
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	if a.sessionLocks == nil {
		a.sessionLocks = map[string]*sync.Mutex{}
	}
	mu, ok := a.sessionLocks[sessionID]
	if !ok {
		mu = &sync.Mutex{}
		a.sessionLocks[sessionID] = mu
	}
	return mu
}

func (a *Adapter) version() string {
	if a.ProbeVersion != nil {
		if v, err := a.ProbeVersion(); err == nil && v != "" {
			return v
		}
	}
	if a.Version != "" {
		return a.Version
	}
	return DefaultPinnedVersion
}

func (a *Adapter) launchCommand() []string {
	if len(a.Command) > 0 {
		return append([]string(nil), a.Command...)
	}
	ver := a.version()
	return []string{"npx", "-y", "xcodebuildmcp@" + ver, "mcp"}
}

// Start resolves an ACTIVE lease, writes ephemeral config, and launches an
// owned XcodeBuildMCP child under the lease isolation environment.
func (a *Adapter) Start(ctx context.Context, sessionID string) (Status, error) {
	if a.GetLease == nil {
		return Status{}, fmt.Errorf("xcodebuildmcp: lease getter is required")
	}
	if a.LeasesRoot == "" {
		return Status{}, fmt.Errorf("xcodebuildmcp: leases root is required")
	}

	// Serialize the whole read-check-spawn-write sequence for this
	// sessionID: closes the same-session Start/Start TOCTOU (see
	// sessionLocks doc on the Adapter struct).
	sessMu := a.lockForSession(sessionID)
	sessMu.Lock()
	defer sessMu.Unlock()

	lease, err := a.GetLease(ctx, sessionID)
	if err != nil {
		return Status{}, err
	}
	b, err := BuildBindings(lease, a.LeasesRoot)
	if err != nil {
		return Status{}, err
	}

	cmdArgv := a.launchCommand()
	paths, err := WriteEphemeralConfig(b, cmdArgv[0], cmdArgv[1:])
	if err != nil {
		return Status{}, err
	}
	ver := a.version()
	if err := atomicWriteFile(paths.VersionFile, []byte(ver+"\n"), 0o600); err != nil {
		return Status{}, err
	}

	// Refuse to start a second child for the same lease without Stop.
	if existing, err := readPIDMeta(paths.PIDMeta); err == nil {
		if processAlive(existing.PID) && existing.LeaseID == lease.ID {
			return Status{}, fmt.Errorf("xcodebuildmcp: adapter already running for %s (pid %d)", lease.ID, existing.PID)
		}
	}

	logFile, err := os.OpenFile(paths.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return Status{}, fmt.Errorf("xcodebuildmcp: open log: %w", err)
	}

	rStdin, wStdin, err := os.Pipe()
	if err != nil {
		_ = logFile.Close()
		return Status{}, fmt.Errorf("xcodebuildmcp: stdin pipe: %w", err)
	}

	cmd := exec.CommandContext(ctx, cmdArgv[0], cmdArgv[1:]...)
	cmd.Stdin = rStdin
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Dir = b.Dirs.Root
	cmd.Env = mergeEnviron(os.Environ(), b.Env)
	// New session: Stop may signal the process group; parent SIGHUP must not
	// cascade into an orphaned kill of unrelated processes.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		_ = rStdin.Close()
		_ = wStdin.Close()
		_ = logFile.Close()
		return Status{}, fmt.Errorf("xcodebuildmcp: start: %w", err)
	}
	_ = rStdin.Close()
	_ = logFile.Close()

	pgid := cmd.Process.Pid
	if actual, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
		pgid = actual
	}
	clock := a.Clock
	if clock == nil {
		clock = time.Now
	}
	meta := PIDMeta{
		LeaseID:   lease.ID,
		PID:       cmd.Process.Pid,
		PGID:      pgid,
		StartedAt: clock().UTC(),
		Command:   cmdArgv,
		Config:    paths.MCPJSON,
		LogPath:   paths.Log,
		Version:   ver,
	}
	if err := writePIDMeta(paths.PIDMeta, meta); err != nil {
		_ = wStdin.Close()
		_ = signalOwned(meta, syscall.SIGKILL)
		return Status{}, err
	}
	a.stdinMu.Lock()
	if a.stdinHolders == nil {
		a.stdinHolders = map[string]*os.File{}
	}
	a.stdinHolders[lease.ID] = wStdin
	a.stdinMu.Unlock()
	// Detach Wait into background so Stop can still signal; reaping happens on Stop.
	go func() { _ = cmd.Wait() }()

	return Status{
		LeaseID:  lease.ID,
		Running:  true,
		PID:      meta.PID,
		Config:   paths.MCPJSON,
		LogPath:  paths.Log,
		Version:  ver,
		Bindings: &b,
	}, nil
}

// Stop kills only the verified owned child for sessionID. Never broad pgrep.
func (a *Adapter) Stop(ctx context.Context, sessionID string) error {
	_ = ctx
	if a.LeasesRoot == "" {
		return fmt.Errorf("xcodebuildmcp: leases root is required")
	}

	// Serialize the whole read-metadata-through-kill-through-remove-metadata
	// sequence for this sessionID: closes Start racing Stop for the same
	// session (see sessionLocks doc on the Adapter struct) — without this, a
	// Stop's final os.Remove(PIDMeta) could delete a concurrently-started
	// new Start's metadata instead of the one it just killed.
	sessMu := a.lockForSession(sessionID)
	sessMu.Lock()
	defer sessMu.Unlock()

	// Stop has destructive side effects (kills a process, removes files), so
	// it fails closed on an unvalidated sessionID rather than silently
	// treating a malformed/traversal-shaped ID as "not found" — reuse the
	// same allowlist internal/safety already establishes for lease paths.
	leaseDir, err := safety.ValidateLeasePath(a.LeasesRoot, sessionID)
	if err != nil {
		return fmt.Errorf("xcodebuildmcp: invalid session %s: %w", sessionID, err)
	}
	paths := AdapterDir(leaseDir)
	meta, err := readPIDMeta(paths.PIDMeta)
	if err != nil {
		return fmt.Errorf("xcodebuildmcp: no owned adapter for %s: %w", sessionID, err)
	}
	if meta.LeaseID != sessionID {
		return fmt.Errorf("xcodebuildmcp: pid metadata lease %s does not match session %s", meta.LeaseID, sessionID)
	}
	a.stdinMu.Lock()
	holder := a.stdinHolders[sessionID]
	delete(a.stdinHolders, sessionID)
	a.stdinMu.Unlock()
	if holder != nil {
		_ = holder.Close()
	}
	if processAlive(meta.PID) {
		if err := signalOwned(meta, syscall.SIGTERM); err != nil {
			_ = signalOwned(meta, syscall.SIGKILL)
		}
		deadline := time.Now().Add(3 * time.Second)
		for processAlive(meta.PID) && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if processAlive(meta.PID) {
			_ = signalOwned(meta, syscall.SIGKILL)
		}
	}
	_ = os.Remove(paths.PIDMeta)
	return nil
}

// Status reports whether the owned adapter child is still running.
func (a *Adapter) Status(ctx context.Context, sessionID string) (Status, error) {
	_ = ctx
	// Read path, same allowlist as Stop: an invalid/traversal-shaped
	// sessionID can never have anything running under it, so it collapses
	// into the same "not running" result the existing no-metadata case
	// already returns — no new error shape, but never a raw path join.
	leaseDir, err := safety.ValidateLeasePath(a.LeasesRoot, sessionID)
	if err != nil {
		return Status{LeaseID: sessionID, Running: false}, nil
	}
	paths := AdapterDir(leaseDir)
	meta, err := readPIDMeta(paths.PIDMeta)
	if err != nil {
		return Status{LeaseID: sessionID, Running: false}, nil
	}
	running := meta.LeaseID == sessionID && processAlive(meta.PID)
	return Status{
		LeaseID: sessionID,
		Running: running,
		PID:     meta.PID,
		Config:  meta.Config,
		LogPath: meta.LogPath,
		Version: meta.Version,
	}, nil
}

func writePIDMeta(path string, meta PIDMeta) error {
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(path, raw, 0o600)
}

func readPIDMeta(path string) (PIDMeta, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return PIDMeta{}, err
	}
	var meta PIDMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return PIDMeta{}, err
	}
	if meta.PID <= 0 || meta.LeaseID == "" {
		return PIDMeta{}, fmt.Errorf("invalid pid metadata")
	}
	return meta, nil
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil
}

// signalOwned sends sig to the process group recorded in meta only when the
// live process still matches the owned PID. Never scans the process table.
func signalOwned(meta PIDMeta, sig syscall.Signal) error {
	if meta.PID <= 0 {
		return fmt.Errorf("xcodebuildmcp: invalid pid")
	}
	if !processAlive(meta.PID) {
		return nil
	}
	pgid := meta.PGID
	if pgid <= 0 {
		pgid = meta.PID
	}
	// Negative pgid → process group. Verified ownership is the pid metadata file.
	return syscall.Kill(-pgid, sig)
}

func mergeEnviron(base []string, override map[string]string) []string {
	out := append([]string(nil), base...)
	for k, v := range override {
		out = append(out, k+"="+v)
	}
	return out
}
