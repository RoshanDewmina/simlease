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

func TestNewServerRegistersFiveTools(t *testing.T) {
	srv := newServer(serverConfig{
		Call: func(context.Context, string, any) (json.RawMessage, error) {
			t.Fatal("unexpected RPC call during ListTools")
			return nil, nil
		},
	})
	cs := connectTestClient(t, srv)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	want := []string{
		"lease_acquire",
		"lease_renew",
		"lease_release",
		"lease_status",
		"pool_status",
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
	cs := connectTestClient(t, srv)
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "lease_acquire",
		Arguments: map[string]any{
			"model":           "iPhone 17 Pro",
			"runtime":         "27.0",
			"labels":          map[string]string{"run": "a"},
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
	if labels["run"] != "a" {
		t.Fatalf("labels = %#v", gotParams["labels"])
	}

	out := decodeStructured[leaseGrantResult](t, res)
	if out.ID != "lease-1" || out.UDID != "UDID-ABC" {
		t.Fatalf("result = %+v", out)
	}
	if out.Env["SIMURGH_UDID"] != "UDID-ABC" {
		t.Fatalf("env = %#v", out.Env)
	}
	if len(out.XcodebuildFlags) != 2 || out.XcodebuildFlags[0] != "-derivedDataPath" {
		t.Fatalf("xcodebuildFlags = %#v", out.XcodebuildFlags)
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
	cs := connectTestClient(t, srv)
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
	cs := connectTestClient(t, srv)
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
	cs := connectTestClient(t, srv)
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
	cs := connectTestClient(t, srv)
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
	cs := connectTestClient(t, srv)
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
	cs := connectTestClient(t, srv)
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
				"leases": {"active": 1, "queued": 0, "released": 2}
			}`), nil
		},
	})
	cs := connectTestClient(t, srv)
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
	if out.Leases["active"] != 1 || out.Leases["queued"] != 0 {
		t.Fatalf("leases = %#v", out.Leases)
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
	cs := connectTestClient(t, srv)
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
