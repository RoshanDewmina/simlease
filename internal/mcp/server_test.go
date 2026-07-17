package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/ipc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func connectTestClient(t *testing.T, srv *mcpsdk.Server) *mcpsdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	t1, t2 := mcpsdk.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "v0.0.0"}, nil)
	cs, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestNewServerRegistersLeaseTools(t *testing.T) {
	srv := newServer(serverConfig{
		Call: func(context.Context, string, any) (json.RawMessage, error) {
			t.Fatal("unexpected RPC call during ListTools")
			return nil, nil
		},
	})
	cs := connectTestClient(t, srv.Server)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	want := []string{
		"lease_acquire",
		"lease_renew",
		"lease_hold",
		"lease_unhold",
		"lease_release",
		"lease_status",
		"pool_status",
		"xcodebuildmcp_tools",
		"xcodebuildmcp_call",
		"xcodebuildmcp_stop",
	}
	if len(res.Tools) != len(want) {
		t.Fatalf("got %d tools, want %d: %#v", len(res.Tools), len(want), toolNames(res.Tools))
	}
	got := map[string]string{}
	for _, tool := range res.Tools {
		got[tool.Name] = tool.Description
	}
	for _, name := range want {
		desc, ok := got[name]
		if !ok {
			t.Errorf("missing tool %q", name)
			continue
		}
		if strings.TrimSpace(desc) == "" {
			t.Errorf("tool %q has empty description", name)
		}
	}
}

func toolNames(tools []*mcpsdk.Tool) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}

func TestLeaseAcquireMapsParamsAndResult(t *testing.T) {
	t.Setenv("SIMURGH_SESSION_ID", "explicit-mcp-session")
	var gotMethod string
	var gotParams map[string]any
	srv := newServer(serverConfig{
		Call: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			gotMethod = method
			raw, err := json.Marshal(params)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(raw, &gotParams); err != nil {
				return nil, err
			}
			return json.RawMessage(`{
				"reused": true,
				"lease": {
					"id": "lease-1",
					"state": "ACTIVE",
					"spec": {"platform":"iOS","model":"iPhone 17 Pro","runtime":"27.0"},
					"device": {"udid":"UDID-ABC","name":"simurgh-lease-1"},
					"expiresAt": "2026-07-13T12:00:00Z",
					"env": {
						"env": {"SIMURGH_UDID":"UDID-ABC","SIMURGH_LEASE_ID":"lease-1"},
						"xcodebuildFlags": ["-derivedDataPath","/tmp/dd"]
					}
				}
			}`), nil
		},
		LastLeasePath: filepath.Join(t.TempDir(), "last-lease"),
	})
	cs := connectTestClient(t, srv.Server)
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "lease_acquire",
		Arguments: map[string]any{
			"model":           "iPhone 17 Pro",
			"runtime":         "27.0",
			"labels":          map[string]string{"project": "test-project", "run": "a"},
			"ttl":             "30m",
			"reuse_if_exists": true,
			"wait":            true,
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %v", res.Content)
	}
	if gotMethod != "lease.acquire" {
		t.Fatalf("method = %q, want lease.acquire", gotMethod)
	}
	spec, _ := gotParams["spec"].(map[string]any)
	if spec["model"] != "iPhone 17 Pro" || spec["runtime"] != "27.0" || spec["platform"] != "iOS" {
		t.Fatalf("spec = %#v", gotParams["spec"])
	}
	if gotParams["reuseIfExists"] != true || gotParams["wait"] != true {
		t.Fatalf("flags = %#v", gotParams)
	}
	if gotParams["ttl"] != "30m" {
		t.Fatalf("ttl = %#v", gotParams["ttl"])
	}
	labels, _ := gotParams["labels"].(map[string]any)
	if labels["project"] != "test-project" || labels["run"] != "a" {
		t.Fatalf("labels = %#v", gotParams["labels"])
	}
	// Session-bound by default (P0-4): no detach param passed above, so the
	// lease must be registered ReleaseOnPIDExit against this MCP server.
	if gotParams["releaseOnPIDExit"] != true {
		t.Fatalf("releaseOnPIDExit = %#v, want true by default", gotParams["releaseOnPIDExit"])
	}
	owner, _ := gotParams["owner"].(map[string]any)
	if owner["agent"] != mcpAgentName || owner["sessionId"] != "explicit-mcp-session" {
		t.Fatalf("owner identity = %#v", owner)
	}

	out := decodeStructured[leaseGrantResult](t, res)
	if out.ID != "lease-1" || out.UDID != "UDID-ABC" {
		t.Fatalf("result = %+v", out)
	}
	if !out.Reused {
		t.Fatalf("result missing reused=true: %+v", out)
	}
	if out.Env["SIMURGH_UDID"] != "UDID-ABC" {
		t.Fatalf("env = %#v", out.Env)
	}
	if len(out.XcodebuildFlags) != 2 || out.XcodebuildFlags[0] != "-derivedDataPath" {
		t.Fatalf("xcodebuildFlags = %#v", out.XcodebuildFlags)
	}
}

func TestMCPSessionIDExplicitAndStableProcessFallback(t *testing.T) {
	t.Setenv("SIMURGH_SESSION_ID", "explicit-mcp-session")
	if first, second := mcpSessionID(), mcpSessionID(); first != "explicit-mcp-session" || second != first {
		t.Fatalf("explicit session IDs=%q/%q", first, second)
	}

	t.Setenv("SIMURGH_SESSION_ID", "")
	first := mcpSessionID()
	second := mcpSessionID()
	if first == "" || second != first || !strings.HasPrefix(first, "mcp:") {
		t.Fatalf("process fallback IDs=%q/%q", first, second)
	}
}

func TestNewRandomSessionIDDiffersAcrossFreshGeneration(t *testing.T) {
	first := newRandomSessionID("mcp")
	second := newRandomSessionID("mcp")
	if first == second {
		t.Fatalf("fresh random session IDs collided: %q", first)
	}
}

func TestLeaseAcquireDetachAndInactivity(t *testing.T) {
	var gotParams map[string]any
	srv := newServer(serverConfig{
		Call: func(_ context.Context, _ string, params any) (json.RawMessage, error) {
			raw, err := json.Marshal(params)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(raw, &gotParams); err != nil {
				return nil, err
			}
			return json.RawMessage(`{
				"lease": {
					"id": "lease-2",
					"state": "ACTIVE",
					"spec": {"platform":"iOS","model":"iPhone 17 Pro"},
					"device": {"udid":"U2"},
					"expiresAt": "2026-07-13T12:00:00Z",
					"env": {"env": {}, "xcodebuildFlags": []}
				}
			}`), nil
		},
		LastLeasePath: filepath.Join(t.TempDir(), "last-lease"),
	})
	cs := connectTestClient(t, srv.Server)
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "lease_acquire",
		Arguments: map[string]any{
			"model":              "iPhone 17 Pro",
			"detach":             true,
			"inactivity_timeout": "10m",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %v", res.Content)
	}
	if gotParams["releaseOnPIDExit"] != false {
		t.Fatalf("releaseOnPIDExit = %#v, want false with detach=true", gotParams["releaseOnPIDExit"])
	}
	timeouts, _ := gotParams["timeouts"].(map[string]any)
	if timeouts["inactivity"] != "10m" {
		t.Fatalf("timeouts = %#v", gotParams["timeouts"])
	}
}

func TestLeaseAcquireWritesLastLease(t *testing.T) {
	lastPath := filepath.Join(t.TempDir(), "last-lease")
	srv := newServer(serverConfig{
		Call: func(context.Context, string, any) (json.RawMessage, error) {
			return json.RawMessage(`{
				"lease": {
					"id": "lease-written",
					"state": "ACTIVE",
					"spec": {"platform":"iOS","model":"iPhone 17 Pro"},
					"device": {"udid":"U1"},
					"expiresAt": "2026-07-13T12:00:00Z",
					"env": {"env": {"SIMURGH_UDID":"U1"}, "xcodebuildFlags": []}
				}
			}`), nil
		},
		LastLeasePath: lastPath,
	})
	cs := connectTestClient(t, srv.Server)
	_, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "lease_acquire",
		Arguments: map[string]any{"model": "iPhone 17 Pro"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	raw, err := os.ReadFile(lastPath)
	if err != nil {
		t.Fatalf("read last-lease: %v", err)
	}
	if strings.TrimSpace(string(raw)) != "lease-written" {
		t.Fatalf("last-lease = %q", raw)
	}
}

func TestLeaseRenewMapsParamsAndResult(t *testing.T) {
	var gotMethod string
	var gotParams map[string]any
	srv := newServer(serverConfig{
		Call: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			gotMethod = method
			raw, _ := json.Marshal(params)
			_ = json.Unmarshal(raw, &gotParams)
			return json.RawMessage(`{
				"lease": {
					"id": "lease-1",
					"state": "ACTIVE",
					"spec": {"platform":"iOS","model":"iPhone 17 Pro"},
					"expiresAt": "2026-07-13T13:00:00Z"
				}
			}`), nil
		},
	})
	cs := connectTestClient(t, srv.Server)
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "lease_renew",
		Arguments: map[string]any{"lease_id": "lease-1", "ttl": "45m"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	if gotMethod != "lease.renew" {
		t.Fatalf("method = %q", gotMethod)
	}
	if gotParams["id"] != "lease-1" || gotParams["ttl"] != "45m" {
		t.Fatalf("params = %#v", gotParams)
	}
	out := decodeStructured[leaseRenewResult](t, res)
	if out.ID != "lease-1" {
		t.Fatalf("result = %+v", out)
	}
	wantExp := time.Date(2026, 7, 13, 13, 0, 0, 0, time.UTC)
	if !out.ExpiresAt.Equal(wantExp) {
		t.Fatalf("expiresAt = %v, want %v", out.ExpiresAt, wantExp)
	}
}

func TestLeaseReleaseMapsResult(t *testing.T) {
	srv := newServer(serverConfig{
		Call: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			if method != "lease.release" {
				t.Fatalf("method = %q", method)
			}
			raw, _ := json.Marshal(params)
			var p map[string]any
			_ = json.Unmarshal(raw, &p)
			if p["id"] != "lease-1" {
				t.Fatalf("params = %#v", p)
			}
			return json.RawMessage(`{
				"lease": {
					"id": "lease-1",
					"state": "RELEASED",
					"spec": {"platform":"iOS","model":"iPhone 17 Pro"},
					"device": {"udid":"UDID-ABC"}
				}
			}`), nil
		},
	})
	cs := connectTestClient(t, srv.Server)
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "lease_release",
		Arguments: map[string]any{"lease_id": "lease-1"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	out := decodeStructured[leaseReleaseResult](t, res)
	if out.ID != "lease-1" || out.State != "RELEASED" || !out.Confirmed {
		t.Fatalf("result = %+v", out)
	}
	if out.CleanupSummary == "" {
		t.Fatal("expected non-empty cleanupSummary")
	}
}

func TestLeaseStatusWithID(t *testing.T) {
	srv := newServer(serverConfig{
		Call: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			if method != "lease.get" {
				t.Fatalf("method = %q", method)
			}
			raw, _ := json.Marshal(params)
			var p map[string]any
			_ = json.Unmarshal(raw, &p)
			if p["id"] != "lease-9" {
				t.Fatalf("params = %#v", p)
			}
			return json.RawMessage(`{
				"lease": {
					"id": "lease-9",
					"state": "ACTIVE",
					"spec": {"platform":"iOS","model":"iPhone 17 Pro"},
					"device": {"udid":"U9"},
					"expiresAt": "2026-07-13T12:00:00Z",
					"env": {"env": {"SIMURGH_UDID":"U9"}, "xcodebuildFlags": ["-destination","id=U9"]}
				}
			}`), nil
		},
	})
	cs := connectTestClient(t, srv.Server)
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "lease_status",
		Arguments: map[string]any{"lease_id": "lease-9"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	out := decodeStructured[leaseGrantResult](t, res)
	if out.ID != "lease-9" || out.UDID != "U9" {
		t.Fatalf("result = %+v", out)
	}
}

func TestLeaseStatusFallsBackToLastLeaseFile(t *testing.T) {
	dir := t.TempDir()
	lastPath := filepath.Join(dir, "last-lease")
	if err := os.WriteFile(lastPath, []byte("lease-from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var gotID string
	srv := newServer(serverConfig{
		LastLeasePath: lastPath,
		Call: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			if method != "lease.get" {
				t.Fatalf("method = %q", method)
			}
			raw, _ := json.Marshal(params)
			var p map[string]any
			_ = json.Unmarshal(raw, &p)
			gotID, _ = p["id"].(string)
			return json.RawMessage(`{
				"lease": {
					"id": "lease-from-file",
					"state": "RELEASED",
					"spec": {"platform":"iOS","model":"iPhone 17 Pro"},
					"expiresAt": "2026-07-13T12:00:00Z"
				}
			}`), nil
		},
	})
	cs := connectTestClient(t, srv.Server)
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "lease_status",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	if gotID != "lease-from-file" {
		t.Fatalf("id = %q", gotID)
	}
	out := decodeStructured[leaseGrantResult](t, res)
	if out.ID != "lease-from-file" || out.State != "RELEASED" {
		t.Fatalf("result = %+v", out)
	}
}

func TestLeaseStatusNoLastLeaseReturnsClearError(t *testing.T) {
	srv := newServer(serverConfig{
		LastLeasePath: filepath.Join(t.TempDir(), "missing-last-lease"),
		Call: func(context.Context, string, any) (json.RawMessage, error) {
			t.Fatal("should not dial when last-lease is missing")
			return nil, nil
		},
	})
	cs := connectTestClient(t, srv.Server)
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "lease_status",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true")
	}
	text := toolErrorText(t, res)
	if !strings.Contains(text, "no lease acquired yet") {
		t.Fatalf("error text = %q", text)
	}
}

func TestPoolStatusMapsResult(t *testing.T) {
	srv := newServer(serverConfig{
		Call: func(_ context.Context, method string, _ any) (json.RawMessage, error) {
			if method != "pool.status" {
				t.Fatalf("method = %q", method)
			}
			return json.RawMessage(`{
				"capacity": 4,
				"perSpecCapacity": 2,
				"maxProvisioningOperations": 1,
				"buildSlots": 3,
				"activeBuilds": 1,
				"queuedBuilds": 2,
				"leases": {"active": 1, "queued": 0, "released": 2},
				"pressure": {
					"ok": true,
					"reason": "",
					"signals": [
						{"name": "ram", "ok": true, "detail": "available 8589934592 bytes (free+inactive); minimum 4294967296"},
						{"name": "thermal", "ok": true, "detail": "thermal state nominal (no warning level recorded)"},
						{"name": "disk", "ok": true, "detail": "available 50000000000 bytes; minimum 5368709120"}
					]
				}
			}`), nil
		},
	})
	cs := connectTestClient(t, srv.Server)
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "pool_status",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	out := decodeStructured[poolStatusResult](t, res)
	if out.Capacity != 4 {
		t.Fatalf("capacity = %v", out.Capacity)
	}
	if out.PerSpecCapacity != 2 {
		t.Fatalf("perSpecCapacity = %v want 2", out.PerSpecCapacity)
	}
	if out.MaxProvisioningOperations != 1 {
		t.Fatalf("maxProvisioningOperations = %v want 1", out.MaxProvisioningOperations)
	}
	if out.BuildSlots != 3 {
		t.Fatalf("buildSlots = %v want 3", out.BuildSlots)
	}
	if out.ActiveBuilds != 1 {
		t.Fatalf("activeBuilds = %v want 1", out.ActiveBuilds)
	}
	if out.QueuedBuilds != 2 {
		t.Fatalf("queuedBuilds = %v want 2", out.QueuedBuilds)
	}
	if out.Leases["active"] != 1 || out.Leases["queued"] != 0 {
		t.Fatalf("leases = %#v", out.Leases)
	}
	if !out.Pressure.OK || len(out.Pressure.Signals) != 3 {
		t.Fatalf("pressure = %#v", out.Pressure)
	}
}

func TestLeaseAcquireCapacityQueuedHasStructuredResourceReason(t *testing.T) {
	position := 3
	srv := newServer(serverConfig{
		Call: func(context.Context, string, any) (json.RawMessage, error) {
			return nil, &rpcError{e: &ipc.Error{
				Code:          "CAPACITY_QUEUED",
				Message:       "3 leases ahead [provisioning_slot]",
				Fix:           "simurgh status --queue",
				QueuePosition: &position,
				Reason:        "provisioning_slot",
				ResourceClass: "provisioning_slot",
			}}
		},
	})
	cs := connectTestClient(t, srv.Server)
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "lease_acquire",
		Arguments: map[string]any{"model": "iPhone 17 Pro"},
	})
	if err != nil {
		t.Fatalf("CallTool protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true")
	}
	out := decodeStructured[leaseGrantResult](t, res)
	if out.Code != "CAPACITY_QUEUED" {
		t.Fatalf("code = %q", out.Code)
	}
	if out.QueuePosition == nil || *out.QueuePosition != position {
		t.Fatalf("queuePosition = %v want %d", out.QueuePosition, position)
	}
	if out.Reason != "provisioning_slot" {
		t.Fatalf("reason = %q", out.Reason)
	}
	if out.ResourceClass != "provisioning_slot" {
		t.Fatalf("resourceClass = %q", out.ResourceClass)
	}
	text := toolErrorText(t, res)
	if !strings.Contains(text, "CAPACITY_QUEUED") ||
		!strings.Contains(text, "simurgh status --queue") {
		t.Fatalf("error text = %q", text)
	}
}

func TestDaemonErrorSurfacesAsToolErrorWithMessageAndFix(t *testing.T) {
	srv := newServer(serverConfig{
		Call: func(context.Context, string, any) (json.RawMessage, error) {
			return nil, &rpcError{e: &ipc.Error{
				Code:    "LEASE_NOT_FOUND",
				Message: `lease "x" not found`,
				Fix:     "simurgh list",
			}}
		},
	})
	cs := connectTestClient(t, srv.Server)
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "lease_release",
		Arguments: map[string]any{"lease_id": "x"},
	})
	if err != nil {
		t.Fatalf("CallTool protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true")
	}
	text := toolErrorText(t, res)
	if !strings.Contains(text, "LEASE_NOT_FOUND") ||
		!strings.Contains(text, `lease "x" not found`) ||
		!strings.Contains(text, "simurgh list") {
		t.Fatalf("error text = %q", text)
	}
}

func TestLeaseAcquireWaitMidResponseEOFDaemonDisconnected(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "smg-mcp-eof-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("SIMURGH_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "logs"), 0o700); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}

	socketPath := filepath.Join(home, "simurghd.sock")
	ln, err := ipc.Listen(socketPath)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		req, err := ipc.NewDecoder(c).DecodeRequest()
		if err != nil {
			return
		}
		if req.Method != "lease.acquire" {
			return
		}
		// Mid-response EOF: drop after the wait:true acquire request is in.
	}()

	srv := NewServer()
	cs := connectTestClient(t, srv.Server)
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "lease_acquire",
		Arguments: map[string]any{
			"model": "iPhone 17 Pro",
			"wait":  true,
		},
	})
	if err != nil {
		t.Fatalf("CallTool protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true")
	}
	text := toolErrorText(t, res)
	if strings.TrimSpace(text) == "EOF" || text == "EOF" {
		t.Fatalf("want DAEMON_DISCONNECTED triple, got bare EOF: %q", text)
	}
	if !strings.Contains(text, ipc.CodeDaemonDisconnected) {
		t.Fatalf("error text missing code: %q", text)
	}
	if !strings.Contains(text, "fix:") || !strings.Contains(text, "simurgh daemon status") {
		t.Fatalf("error text missing fix: %q", text)
	}
	if !strings.Contains(text, "connection to daemon lost") {
		t.Fatalf("error text missing message: %q", text)
	}
}

func decodeStructured[T any](t *testing.T, res *mcpsdk.CallToolResult) T {
	t.Helper()
	var out T
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured: %v", err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal structured %s: %v", raw, err)
	}
	return out
}

func toolErrorText(t *testing.T, res *mcpsdk.CallToolResult) string {
	t.Helper()
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	if len(parts) == 0 {
		if err := res.GetError(); err != nil {
			return err.Error()
		}
		t.Fatalf("no error text in result: %#v", res)
	}
	return strings.Join(parts, "\n")
}

// Ensure rpcError is classified like the CLI wrapper.
func TestRPCErrorFormat(t *testing.T) {
	err := &rpcError{e: &ipc.Error{Code: "X", Message: "m", Fix: "f"}}
	if !strings.Contains(err.Error(), "X") || !strings.Contains(err.Error(), "m") {
		t.Fatalf("Error() = %q", err.Error())
	}
	var target *rpcError
	if !errors.As(err, &target) {
		t.Fatal("errors.As failed")
	}
}

func TestDefaultLastLeasePathFailsClosedOnHomeMismatch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SIMURGH_HOME", dir)
	got, err := DefaultLastLeasePath()
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(dir, "last-lease") {
		t.Fatalf("DefaultLastLeasePath() = %q", got)
	}
}
