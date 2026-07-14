package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/ipc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	mcpAgentName = "simurgh-mcp"
	mcpVersion   = "0.0.0-dev"
)

var mcpProcessSessionID = newRandomSessionID("mcp")

// CallFunc dials the daemon and invokes one RPC method. Tests inject a fake;
// production uses defaultDialAndCall.
type CallFunc func(ctx context.Context, method string, params any) (json.RawMessage, error)

type serverConfig struct {
	Call          CallFunc
	LastLeasePath string // absolute path; empty → DefaultLastLeasePath()
}

// NewServer constructs the simurgh MCP server with all five tools registered.
func NewServer() *mcpsdk.Server {
	return newServer(serverConfig{})
}

func newServer(cfg serverConfig) *mcpsdk.Server {
	if cfg.Call == nil {
		cfg.Call = defaultDialAndCall
	}
	s := &server{cfg: cfg}
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "simurgh",
		Title:   "simurgh",
		Version: mcpVersion,
	}, nil)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "lease_acquire",
		Description: "Acquire an isolated iOS Simulator lease before any simulator interaction. " +
			"Never pick a UDID from simctl yourself — always go through this tool. " +
			"Returns the device UDID plus the exact env vars and xcodebuild flags to use for isolated builds " +
			"(see the env and xcodebuildFlags fields) — pass these to xcodebuild / simurgh exec rather than using default paths. " +
			"Call once per parallel agent or session that needs its own simulator; do not share one lease across concurrent agents. " +
			"Leases are session-bound by default: when this agent session ends, the lease is reclaimed automatically. " +
			"Pass detach=true only when the lease must deliberately outlive this session.",
	}, s.handleLeaseAcquire)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "lease_renew",
		Description: "Extend an active lease's TTL before it expires. " +
			"Call when your work will outlast the current expiry (from lease_acquire or lease_status). " +
			"Do not acquire a second lease just to keep working — renew the lease you already hold. " +
			"Requires an explicit lease_id; this tool does not fall back to the last-lease pointer.",
	}, s.handleLeaseRenew)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "lease_release",
		Description: "Release a lease when you are finished with the simulator so the device can be deleted and capacity returned to the pool. " +
			"Always release before ending your session. Do not leave leases dangling — they hold pool capacity and keep clone devices around until cleaned up. " +
			"Pass the lease_id returned by lease_acquire. Prefer release over abandoning the lease and hoping a reaper finds it.",
	}, s.handleLeaseRelease)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "lease_status",
		Description: "Inspect a lease's current state, device UDID, expiry, and isolation env/flags. " +
			"Call to confirm a lease is still ACTIVE before building or launching, or to recover details after a disconnect. " +
			"If lease_id is omitted, uses the caller's most recently acquired lease (~/.simurgh/last-lease). " +
			"Do not invent UDIDs from simctl list — use the UDID from this tool or from lease_acquire.",
	}, s.handleLeaseStatus)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "pool_status",
		Description: "Report pool capacity and how many leases are active, queued, or otherwise. " +
			"Call before lease_acquire when you need to know whether the pool is under pressure, or after acquire/release to confirm pool accounting. " +
			"Do not use this to pick a device or UDID — always go through lease_acquire for that.",
	}, s.handlePoolStatus)

	return srv
}

type server struct {
	cfg serverConfig
}

// --- path helpers (duplicated from cmd/simurgh; avoid importing package main) ---

func simurghHomeDir() (string, error) {
	if h := os.Getenv("SIMURGH_HOME"); h != "" {
		return h, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".simurgh"), nil
}

// DefaultSocketPath returns ~/.simurgh/simurghd.sock (honors $SIMURGH_HOME).
func DefaultSocketPath() (string, error) {
	home, err := simurghHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "simurghd.sock"), nil
}

// DefaultLogPath returns ~/.simurgh/logs/simurghd.log (honors $SIMURGH_HOME).
func DefaultLogPath() (string, error) {
	home, err := simurghHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "logs", "simurghd.log"), nil
}

// DefaultLastLeasePath returns ~/.simurgh/last-lease (honors $SIMURGH_HOME).
func DefaultLastLeasePath() (string, error) {
	home, err := simurghHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "last-lease"), nil
}

func (s *server) lastLeasePath() (string, error) {
	if s.cfg.LastLeasePath != "" {
		return s.cfg.LastLeasePath, nil
	}
	return DefaultLastLeasePath()
}

// --- dial seam ---

type rpcError struct {
	e *ipc.Error
}

func (r *rpcError) Error() string {
	if r == nil || r.e == nil {
		return "rpc error"
	}
	return formatIPCError(r.e)
}

func formatIPCError(e *ipc.Error) string {
	if e == nil {
		return "rpc error"
	}
	msg := fmt.Sprintf("%s: %s", e.Code, e.Message)
	if e.Fix != "" {
		msg += fmt.Sprintf(" (fix: %s)", e.Fix)
	}
	return msg
}

func defaultDialAndCall(ctx context.Context, method string, params any) (json.RawMessage, error) {
	socketPath, err := DefaultSocketPath()
	if err != nil {
		return nil, err
	}
	logPath, err := DefaultLogPath()
	if err != nil {
		return nil, err
	}
	conn, err := ipc.Dial(ctx, socketPath, logPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	var rawParams json.RawMessage
	if params != nil {
		rawParams, err = json.Marshal(params)
		if err != nil {
			return nil, err
		}
	}
	req := ipc.Request{
		V:      ipc.ProtocolVersion,
		ID:     fmt.Sprintf("mcp-%d", time.Now().UnixNano()),
		Method: method,
		Params: rawParams,
	}
	if err := ipc.Encode(conn, req); err != nil {
		return nil, err
	}
	resp, err := ipc.NewDecoder(conn).DecodeResponse()
	if err != nil {
		return nil, err
	}
	if resp.ID != req.ID {
		return nil, fmt.Errorf("response id mismatch: got %q want %q", resp.ID, req.ID)
	}
	if !resp.OK {
		if resp.Error != nil {
			return nil, &rpcError{e: resp.Error}
		}
		return nil, fmt.Errorf("%s returned ok=false", method)
	}
	return resp.Result, nil
}

func mapCallError(err error) error {
	if err == nil {
		return nil
	}
	var re *rpcError
	if errors.As(err, &re) && re.e != nil {
		return fmt.Errorf("%s", formatIPCError(re.e))
	}
	var dn *ipc.DaemonNotRunningError
	if errors.As(err, &dn) && dn.ErrorEnvelope != nil {
		return fmt.Errorf("%s", formatIPCError(dn.ErrorEnvelope))
	}
	return err
}

// --- tool params / results ---

type leaseAcquireParams struct {
	Model         string            `json:"model" jsonschema:"iOS device model name, e.g. iPhone 17 Pro"`
	Runtime       string            `json:"runtime,omitempty" jsonschema:"optional iOS runtime version, e.g. 27.0"`
	Labels        map[string]string `json:"labels,omitempty" jsonschema:"optional key/value labels; reuse requires an exact map containing non-empty project"`
	TTL           string            `json:"ttl,omitempty" jsonschema:"optional lease TTL duration string, e.g. 30m"`
	ReuseIfExists bool              `json:"reuse_if_exists,omitempty" jsonschema:"if true, renew compatible ACTIVE or attach to PROVISIONING; requires same DeviceSpec, exact labels with non-empty project, and non-empty Owner.Agent+SessionID. Missing partition allocates new. SIMURGH_SESSION_ID is explicit; random fallback lasts this MCP process only. Metadata partitions reuse, not authentication"`
	Wait          bool              `json:"wait,omitempty" jsonschema:"if true, wait when the pool is at capacity instead of failing immediately"`
	Detach        bool              `json:"detach,omitempty" jsonschema:"if true, the lease survives this agent session ending; default false means the lease is reclaimed when this session's MCP server process exits"`
	Inactivity    string            `json:"inactivity_timeout,omitempty" jsonschema:"optional inactivity timeout duration string, e.g. 10m; the lease is reclaimed after this long without activity"`
}

type leaseIDParams struct {
	LeaseID string `json:"lease_id" jsonschema:"lease id returned by lease_acquire"`
	TTL     string `json:"ttl,omitempty" jsonschema:"optional new TTL duration string, e.g. 30m"`
}

type leaseStatusParams struct {
	LeaseID string `json:"lease_id,omitempty" jsonschema:"lease id to inspect; omit to use ~/.simurgh/last-lease"`
}

type leaseGrantResult struct {
	ID              string            `json:"id" jsonschema:"lease id"`
	State           string            `json:"state" jsonschema:"lease state, e.g. ACTIVE"`
	Reused          bool              `json:"reused" jsonschema:"true when this acquire renewed or attached to an existing compatible lease"`
	UDID            string            `json:"udid,omitempty" jsonschema:"simulator device UDID"`
	Model           string            `json:"model,omitempty" jsonschema:"requested device model"`
	Runtime         string            `json:"runtime,omitempty" jsonschema:"requested iOS runtime"`
	ExpiresAt       time.Time         `json:"expiresAt" jsonschema:"lease expiry timestamp"`
	Env             map[string]string `json:"env,omitempty" jsonschema:"isolation env vars for builds and launches"`
	XcodebuildFlags []string          `json:"xcodebuildFlags,omitempty" jsonschema:"xcodebuild flags for isolated derived data and destination"`
	// CAPACITY_QUEUED uses this same typed result additively so existing success
	// callers keep their schema while MCP clients receive structured queue data.
	Code          string `json:"code,omitempty" jsonschema:"stable error code; CAPACITY_QUEUED when acquire is queued"`
	Message       string `json:"message,omitempty" jsonschema:"human-readable queue detail"`
	Fix           string `json:"fix,omitempty" jsonschema:"actionable next command"`
	QueuePosition *int   `json:"queuePosition,omitempty" jsonschema:"1-based position in the host-global queue"`
	Reason        string `json:"reason,omitempty" jsonschema:"stable queue reason: simulator_slot, provisioning_slot, per_spec_slot, or pressure"`
	ResourceClass string `json:"resourceClass,omitempty" jsonschema:"stable contended resource class"`
}

type leaseRenewResult struct {
	ID        string    `json:"id" jsonschema:"lease id"`
	ExpiresAt time.Time `json:"expiresAt" jsonschema:"new lease expiry timestamp"`
}

type leaseReleaseResult struct {
	ID             string `json:"id" jsonschema:"released lease id"`
	State          string `json:"state" jsonschema:"lease state after release"`
	Confirmed      bool   `json:"confirmed" jsonschema:"true when the daemon accepted the release"`
	CleanupSummary string `json:"cleanupSummary" jsonschema:"short summary of post-release cleanup"`
}

type poolStatusResult struct {
	Capacity                  int                    `json:"capacity" jsonschema:"host-global active plus provisioning simulator capacity"`
	PerSpecCapacity           int                    `json:"perSpecCapacity" jsonschema:"optional per-device-spec capacity; zero means unlimited"`
	MaxProvisioningOperations int                    `json:"maxProvisioningOperations" jsonschema:"optional host-global provisioning-operation limit; zero means unlimited"`
	Leases                    map[string]int         `json:"leases" jsonschema:"lease counts by state (active, queued, ...)"`
	Pressure                  poolStatusPressureView `json:"pressure" jsonschema:"host RAM/thermal/disk pressure before grant"`
}

type poolStatusPressureView struct {
	OK      bool                       `json:"ok" jsonschema:"true when all determinable pressure signals are OK"`
	Reason  string                     `json:"reason" jsonschema:"short reason naming failing signal(s); empty when ok"`
	Signals []poolStatusPressureSignal `json:"signals" jsonschema:"per-signal RAM/thermal/disk detail"`
}

type poolStatusPressureSignal struct {
	Name   string `json:"name" jsonschema:"signal name (ram, thermal, disk)"`
	OK     bool   `json:"ok" jsonschema:"true when this signal is non-blocking"`
	Detail string `json:"detail" jsonschema:"human-readable signal detail"`
}

type wireLeaseWrap struct {
	Lease  wireLease `json:"lease"`
	Reused bool      `json:"reused"`
}

type wireLease struct {
	ID        string       `json:"id"`
	State     string       `json:"state"`
	Spec      wireSpec     `json:"spec"`
	Device    *wireDevice  `json:"device,omitempty"`
	ExpiresAt time.Time    `json:"expiresAt"`
	Env       wireLeaseEnv `json:"env"`
}

type wireSpec struct {
	Platform string `json:"platform"`
	Model    string `json:"model"`
	Runtime  string `json:"runtime"`
}

type wireDevice struct {
	UDID string `json:"udid"`
	Name string `json:"name"`
}

type wireLeaseEnv struct {
	Env             map[string]string `json:"env"`
	XcodebuildFlags []string          `json:"xcodebuildFlags"`
}

func grantFromWire(w wireLease, reused bool) leaseGrantResult {
	out := leaseGrantResult{
		ID:              w.ID,
		State:           w.State,
		Reused:          reused,
		Model:           w.Spec.Model,
		Runtime:         w.Spec.Runtime,
		ExpiresAt:       w.ExpiresAt,
		Env:             w.Env.Env,
		XcodebuildFlags: w.Env.XcodebuildFlags,
	}
	if w.Device != nil {
		out.UDID = w.Device.UDID
	}
	return out
}

func mcpSessionID() string {
	if explicit := strings.TrimSpace(os.Getenv("SIMURGH_SESSION_ID")); explicit != "" {
		return explicit
	}
	// Random and stable for this MCP server process only.
	return mcpProcessSessionID
}

func newRandomSessionID(prefix string) string {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		panic(fmt.Sprintf("simurgh mcp: generate session identity: %v", err))
	}
	return prefix + ":" + hex.EncodeToString(entropy[:])
}

// --- handlers ---

func (s *server) handleLeaseAcquire(ctx context.Context, _ *mcpsdk.CallToolRequest, in leaseAcquireParams) (*mcpsdk.CallToolResult, leaseGrantResult, error) {
	var zero leaseGrantResult
	if strings.TrimSpace(in.Model) == "" {
		return nil, zero, fmt.Errorf("model is required")
	}
	params := map[string]any{
		"spec": map[string]string{
			"platform": "iOS",
			"model":    in.Model,
			"runtime":  in.Runtime,
		},
		"labels":        in.Labels,
		"ttl":           in.TTL,
		"reuseIfExists": in.ReuseIfExists,
		"wait":          in.Wait,
		// Session-bound by default (P0-4): this MCP server process lives
		// exactly as long as the agent session that spawned it, so binding
		// the lease to our own PID makes "agent crashed/window closed" and
		// "lease reclaimed" the same event — the out-of-box behavior the
		// crash-cleanup story (B4) is allowed to claim.
		"releaseOnPIDExit": !in.Detach,
		"owner": map[string]any{
			"pid":       os.Getpid(),
			"agent":     mcpAgentName,
			"sessionId": mcpSessionID(),
		},
		"timeouts": map[string]string{"inactivity": in.Inactivity},
	}
	raw, err := s.cfg.Call(ctx, "lease.acquire", params)
	if err != nil {
		var re *rpcError
		if errors.As(err, &re) && re.e != nil && re.e.Code == "CAPACITY_QUEUED" {
			queue := leaseGrantResult{
				Code:          re.e.Code,
				Message:       re.e.Message,
				Fix:           re.e.Fix,
				QueuePosition: re.e.QueuePosition,
				Reason:        re.e.Reason,
				ResourceClass: re.e.ResourceClass,
			}
			return &mcpsdk.CallToolResult{
				IsError: true,
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: formatIPCError(re.e)}},
			}, queue, nil
		}
		return nil, zero, mapCallError(err)
	}
	var wrap wireLeaseWrap
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, zero, fmt.Errorf("decode lease.acquire: %w", err)
	}
	if err := s.writeLastLease(wrap.Lease.ID); err != nil {
		return nil, zero, fmt.Errorf("write last-lease: %w", err)
	}
	return nil, grantFromWire(wrap.Lease, wrap.Reused), nil
}

func (s *server) handleLeaseRenew(ctx context.Context, _ *mcpsdk.CallToolRequest, in leaseIDParams) (*mcpsdk.CallToolResult, leaseRenewResult, error) {
	var zero leaseRenewResult
	if strings.TrimSpace(in.LeaseID) == "" {
		return nil, zero, fmt.Errorf("lease_id is required")
	}
	params := map[string]any{"id": in.LeaseID}
	if in.TTL != "" {
		params["ttl"] = in.TTL
	}
	raw, err := s.cfg.Call(ctx, "lease.renew", params)
	if err != nil {
		return nil, zero, mapCallError(err)
	}
	var wrap wireLeaseWrap
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, zero, fmt.Errorf("decode lease.renew: %w", err)
	}
	return nil, leaseRenewResult{ID: wrap.Lease.ID, ExpiresAt: wrap.Lease.ExpiresAt}, nil
}

func (s *server) handleLeaseRelease(ctx context.Context, _ *mcpsdk.CallToolRequest, in leaseIDParams) (*mcpsdk.CallToolResult, leaseReleaseResult, error) {
	var zero leaseReleaseResult
	if strings.TrimSpace(in.LeaseID) == "" {
		return nil, zero, fmt.Errorf("lease_id is required")
	}
	raw, err := s.cfg.Call(ctx, "lease.release", map[string]any{"id": in.LeaseID})
	if err != nil {
		return nil, zero, mapCallError(err)
	}
	var wrap wireLeaseWrap
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, zero, fmt.Errorf("decode lease.release: %w", err)
	}
	summary := "lease released"
	if wrap.Lease.Device != nil && wrap.Lease.Device.UDID != "" {
		summary = "lease released; device teardown requested for " + wrap.Lease.Device.UDID
	}
	return nil, leaseReleaseResult{
		ID:             wrap.Lease.ID,
		State:          wrap.Lease.State,
		Confirmed:      true,
		CleanupSummary: summary,
	}, nil
}

func (s *server) handleLeaseStatus(ctx context.Context, _ *mcpsdk.CallToolRequest, in leaseStatusParams) (*mcpsdk.CallToolResult, leaseGrantResult, error) {
	var zero leaseGrantResult
	id := strings.TrimSpace(in.LeaseID)
	if id == "" {
		var err error
		id, err = s.readLastLease()
		if err != nil {
			return nil, zero, fmt.Errorf("no lease acquired yet — call lease_acquire first")
		}
	}
	raw, err := s.cfg.Call(ctx, "lease.get", map[string]any{"id": id})
	if err != nil {
		return nil, zero, mapCallError(err)
	}
	var wrap wireLeaseWrap
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, zero, fmt.Errorf("decode lease.get: %w", err)
	}
	return nil, grantFromWire(wrap.Lease, false), nil
}

func (s *server) handlePoolStatus(ctx context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, poolStatusResult, error) {
	var zero poolStatusResult
	raw, err := s.cfg.Call(ctx, "pool.status", map[string]any{})
	if err != nil {
		return nil, zero, mapCallError(err)
	}
	var out poolStatusResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, zero, fmt.Errorf("decode pool.status: %w", err)
	}
	return nil, out, nil
}

func (s *server) writeLastLease(id string) error {
	path, err := s.lastLeasePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(id+"\n"), 0o600)
}

func (s *server) readLastLease() (string, error) {
	path, err := s.lastLeasePath()
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(raw))
	if id == "" {
		return "", fmt.Errorf("last-lease file is empty")
	}
	return id, nil
}
