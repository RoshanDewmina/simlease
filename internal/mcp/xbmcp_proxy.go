package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
	xbmcp "github.com/RoshanDewmina/simurgh/internal/integrations/xcodebuildmcp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// xbmcpChild is one owned stdio MCP client session for a lease.
type xbmcpChild struct {
	session *mcpsdk.ClientSession
	cmd     *exec.Cmd
	pid     int
}

type xbmcpToolsParams struct {
	LeaseID string `json:"lease_id" jsonschema:"lease id returned by lease_acquire"`
}

type xbmcpToolInfo struct {
	Name        string `json:"name" jsonschema:"child tool name"`
	Description string `json:"description" jsonschema:"child tool description"`
}

type xbmcpToolsResult struct {
	LeaseID string          `json:"lease_id" jsonschema:"lease id whose child was queried"`
	Tools   []xbmcpToolInfo `json:"tools" jsonschema:"tools exposed by the per-lease XcodeBuildMCP child"`
}

type xbmcpCallParams struct {
	LeaseID   string         `json:"lease_id" jsonschema:"lease id returned by lease_acquire"`
	Tool      string         `json:"tool" jsonschema:"name of the child XcodeBuildMCP tool to call"`
	Arguments map[string]any `json:"arguments,omitempty" jsonschema:"arbitrary JSON object forwarded as the child tool arguments"`
}

type xbmcpStopParams struct {
	LeaseID string `json:"lease_id" jsonschema:"lease id whose XcodeBuildMCP child should be stopped"`
}

type xbmcpStopResult struct {
	LeaseID string `json:"lease_id" jsonschema:"lease id"`
	Stopped bool   `json:"stopped" jsonschema:"true when a running child was stopped; false when none was running (idempotent)"`
}

func (s *server) registerXBMCPProxyTools(srv *mcpsdk.Server) {
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "xcodebuildmcp_tools",
		Description: "List tools on the per-lease XcodeBuildMCP child for lease_id. " +
			"Spawns the isolated child on first use (same isolation env as simurgh integration xcodebuildmcp start). " +
			"Prefer this over registering a raw global XcodeBuildMCP MCP server — tool calls through " +
			"xcodebuildmcp_call stay lease-scoped without mid-session client rebinding.",
	}, s.handleXBMCPTools)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "xcodebuildmcp_call",
		Description: "Forward a tools/call to the per-lease XcodeBuildMCP child for lease_id. " +
			"Spawns the isolated child on first use. Pass the child tool name and its arguments object. " +
			"Returns the child's result content verbatim (including isError). " +
			"Do not call raw mcp__XcodeBuildMCP__* tools from a global registration — they bypass lease isolation.",
	}, s.handleXBMCPCall)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name: "xcodebuildmcp_stop",
		Description: "Stop the per-lease XcodeBuildMCP child for lease_id if it is running. " +
			"Idempotent: succeeding when no child is running. " +
			"lease_release also stops a live child for that lease before releasing.",
	}, s.handleXBMCPStop)
}

func (s *server) handleXBMCPTools(ctx context.Context, _ *mcpsdk.CallToolRequest, in xbmcpToolsParams) (*mcpsdk.CallToolResult, xbmcpToolsResult, error) {
	var zero xbmcpToolsResult
	leaseID := strings.TrimSpace(in.LeaseID)
	if leaseID == "" {
		return nil, zero, fmt.Errorf("lease_id is required")
	}
	child, err := s.ensureXBMCPChild(ctx, leaseID)
	if err != nil {
		return nil, zero, err
	}
	listed, err := child.session.ListTools(ctx, nil)
	if err != nil {
		return nil, zero, fmt.Errorf("xcodebuildmcp_tools: list child tools: %w", err)
	}
	out := xbmcpToolsResult{LeaseID: leaseID, Tools: make([]xbmcpToolInfo, 0, len(listed.Tools))}
	for _, t := range listed.Tools {
		out.Tools = append(out.Tools, xbmcpToolInfo{Name: t.Name, Description: t.Description})
	}
	return nil, out, nil
}

func (s *server) handleXBMCPCall(ctx context.Context, _ *mcpsdk.CallToolRequest, in xbmcpCallParams) (*mcpsdk.CallToolResult, any, error) {
	leaseID := strings.TrimSpace(in.LeaseID)
	if leaseID == "" {
		return nil, nil, fmt.Errorf("lease_id is required")
	}
	tool := strings.TrimSpace(in.Tool)
	if tool == "" {
		return nil, nil, fmt.Errorf("tool is required")
	}
	child, err := s.ensureXBMCPChild(ctx, leaseID)
	if err != nil {
		return nil, nil, err
	}
	args := in.Arguments
	if args == nil {
		args = map[string]any{}
	}
	res, err := child.session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      tool,
		Arguments: args,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("xcodebuildmcp_call: child %s: %w", tool, err)
	}
	// Return the child's result verbatim (content + isError).
	return res, nil, nil
}

func (s *server) handleXBMCPStop(ctx context.Context, _ *mcpsdk.CallToolRequest, in xbmcpStopParams) (*mcpsdk.CallToolResult, xbmcpStopResult, error) {
	_ = ctx
	var zero xbmcpStopResult
	leaseID := strings.TrimSpace(in.LeaseID)
	if leaseID == "" {
		return nil, zero, fmt.Errorf("lease_id is required")
	}
	stopped := s.stopXBMCPChild(leaseID)
	return nil, xbmcpStopResult{LeaseID: leaseID, Stopped: stopped}, nil
}

func (s *server) lockForLease(leaseID string) *sync.Mutex {
	s.leaseMu.Lock()
	defer s.leaseMu.Unlock()
	if s.leaseLocks == nil {
		s.leaseLocks = map[string]*sync.Mutex{}
	}
	mu, ok := s.leaseLocks[leaseID]
	if !ok {
		mu = &sync.Mutex{}
		s.leaseLocks[leaseID] = mu
	}
	return mu
}

func (s *server) ensureXBMCPChild(ctx context.Context, leaseID string) (*xbmcpChild, error) {
	mu := s.lockForLease(leaseID)
	mu.Lock()
	defer mu.Unlock()

	s.childrenMu.Lock()
	existing := s.children[leaseID]
	s.childrenMu.Unlock()
	if existing != nil && existing.pid > 0 && xbmcpProcessAlive(existing.pid) {
		return existing, nil
	}
	if existing != nil {
		s.detachChildLocked(leaseID, existing)
	}

	lease, err := s.resolveBrokerLease(ctx, leaseID)
	if err != nil {
		return nil, err
	}
	leasesRoot, err := s.leasesRoot()
	if err != nil {
		return nil, err
	}
	bindings, err := xbmcp.BuildBindings(lease, leasesRoot)
	if err != nil {
		return nil, fmt.Errorf("xcodebuildmcp: %w", err)
	}

	cmdArgv := s.launchCommand()
	if len(cmdArgv) == 0 {
		return nil, fmt.Errorf("xcodebuildmcp: empty launch command")
	}
	cmd := exec.CommandContext(context.Background(), cmdArgv[0], cmdArgv[1:]...)
	cmd.Dir = bindings.Dirs.Root
	cmd.Env = mergeChildEnviron(os.Environ(), bindings.Env, s.launchExtraEnv(leaseID))
	// Never log cmd.Env — may contain credentials / isolation secrets.

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "simurgh-xbmcp-proxy", Version: mcpVersion}, nil)
	session, err := client.Connect(ctx, &mcpsdk.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return nil, fmt.Errorf(
			"xcodebuildmcp: failed to start child for %s: %w (fix: ensure XcodeBuildMCP is installed, or call simurgh integration xcodebuildmcp start --session %s for the CLI fallback)",
			leaseID, err, leaseID,
		)
	}
	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	child := &xbmcpChild{session: session, cmd: cmd, pid: pid}
	s.childrenMu.Lock()
	if s.children == nil {
		s.children = map[string]*xbmcpChild{}
	}
	s.children[leaseID] = child
	s.childrenMu.Unlock()
	return child, nil
}

func (s *server) resolveBrokerLease(ctx context.Context, leaseID string) (broker.Lease, error) {
	raw, err := s.cfg.Call(ctx, "lease.get", map[string]any{"id": leaseID})
	if err != nil {
		return broker.Lease{}, mapCallError(err)
	}
	var wrap struct {
		Lease broker.Lease `json:"lease"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return broker.Lease{}, fmt.Errorf("decode lease.get: %w", err)
	}
	if wrap.Lease.ID == "" {
		wrap.Lease.ID = leaseID
	}
	return wrap.Lease, nil
}

func (s *server) launchCommand() []string {
	if len(s.cfg.LaunchCommand) > 0 {
		return append([]string(nil), s.cfg.LaunchCommand...)
	}
	return xbmcp.DefaultLaunchCommand("")
}

func (s *server) launchExtraEnv(leaseID string) []string {
	if s.cfg.LaunchExtraEnvFunc != nil {
		return s.cfg.LaunchExtraEnvFunc(leaseID)
	}
	return append([]string(nil), s.cfg.LaunchExtraEnv...)
}

func (s *server) leasesRoot() (string, error) {
	if s.cfg.LeasesRoot != "" {
		return s.cfg.LeasesRoot, nil
	}
	home, err := simurghHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "leases"), nil
}

func (s *server) stopXBMCPChild(leaseID string) bool {
	mu := s.lockForLease(leaseID)
	mu.Lock()
	defer mu.Unlock()

	s.childrenMu.Lock()
	child := s.children[leaseID]
	delete(s.children, leaseID)
	s.childrenMu.Unlock()
	if child == nil {
		return false
	}
	s.killChild(child)
	return true
}

func (s *server) detachChildLocked(leaseID string, child *xbmcpChild) {
	s.childrenMu.Lock()
	if s.children[leaseID] == child {
		delete(s.children, leaseID)
	}
	s.childrenMu.Unlock()
	s.killChild(child)
}

func (s *server) stopAllChildren() {
	s.childrenMu.Lock()
	ids := make([]string, 0, len(s.children))
	for id := range s.children {
		ids = append(ids, id)
	}
	s.childrenMu.Unlock()
	for _, id := range ids {
		s.stopXBMCPChild(id)
	}
}

func (s *server) killChild(child *xbmcpChild) {
	if child == nil {
		return
	}
	if child.session != nil {
		_ = child.session.Close()
	}
	pid := child.pid
	if pid > 0 && xbmcpProcessAlive(pid) {
		_ = syscall.Kill(pid, syscall.SIGTERM)
		deadline := time.Now().Add(3 * time.Second)
		for xbmcpProcessAlive(pid) && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if xbmcpProcessAlive(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

func (s *server) childPID(leaseID string) int {
	s.childrenMu.Lock()
	defer s.childrenMu.Unlock()
	c := s.children[leaseID]
	if c == nil {
		return 0
	}
	return c.pid
}

func (s *server) attachLifecycle(ctx context.Context) {
	if ctx == nil {
		return
	}
	s.lifecycleOnce.Do(func() {
		go func() {
			<-ctx.Done()
			s.stopAllChildren()
		}()
	})
}

func xbmcpProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func mergeChildEnviron(base []string, override map[string]string, extra []string) []string {
	out := append([]string(nil), base...)
	for k, v := range override {
		out = append(out, k+"="+v)
	}
	out = append(out, extra...)
	return out
}
