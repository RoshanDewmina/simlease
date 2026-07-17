package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
	"github.com/RoshanDewmina/simurgh/internal/ipc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	envRunFakeXBMCP               = "SIMURGH_TEST_FAKE_XBMCP"
	envFakeXBMCPEnvFile           = "SIMURGH_TEST_XBMCP_ENVFILE"
	envFakeXBMCPGrandchildPIDFile = "SIMURGH_TEST_XBMCP_GRANDCHILD_PIDFILE"
)

func TestMain(m *testing.M) {
	if os.Getenv(envRunFakeXBMCP) == "1" {
		runFakeXBMCPChild()
		return
	}
	os.Exit(m.Run())
}

// runFakeXBMCPChild is the helper-process fake stdio MCP server: records env
// to SIMURGH_TEST_XBMCP_ENVFILE and exposes one echo tool.
// When SIMURGH_TEST_XBMCP_GRANDCHILD_PIDFILE is set, spawns a sleeper in the
// same process group (npx-wrapper stand-in) and writes its PID to that file.
func runFakeXBMCPChild() {
	if path := os.Getenv(envFakeXBMCPEnvFile); path != "" {
		_ = os.WriteFile(path, []byte(strings.Join(os.Environ(), "\n")+"\n"), 0o600)
	}
	if path := os.Getenv(envFakeXBMCPGrandchildPIDFile); path != "" {
		sleep := exec.Command("sleep", "120")
		if err := sleep.Start(); err == nil && sleep.Process != nil {
			_ = os.WriteFile(path, []byte(strconv.Itoa(sleep.Process.Pid)+"\n"), 0o600)
			// Intentionally not Wait()'d — stands in for an npx grandchild
			// that must die via process-group teardown, not parent Wait.
			go func() { _ = sleep.Wait() }()
		}
	}
	ctx := context.Background()
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "fake-xbmcp", Version: "test"}, nil)
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "echo",
		Description: "echoes the message argument",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, in struct {
		Message string `json:"message"`
	}) (*mcpsdk.CallToolResult, any, error) {
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "echo:" + in.Message}},
		}, nil, nil
	})
	if err := srv.Run(ctx, &mcpsdk.StdioTransport{}); err != nil {
		fmt.Fprintf(os.Stderr, "fake-xbmcp: %v\n", err)
		os.Exit(1)
	}
}

func fakeXBMCPLaunchCommand() []string {
	// Re-exec this test binary as a helper process (SDK cmd_test pattern).
	// -test.run=^$ runs no tests; TestMain sees envRunFakeXBMCP and serves MCP.
	return []string{os.Args[0], "-test.run=^$", "-test.v=false"}
}

func leaseGetJSON(id, udid string) json.RawMessage {
	raw, err := json.Marshal(map[string]any{
		"reused": false,
		"lease": broker.Lease{
			ID:    id,
			State: broker.LeaseActive,
			Device: &broker.Device{
				UDID: udid,
				Name: "simurgh-" + id,
			},
		},
	})
	if err != nil {
		panic(err)
	}
	return raw
}

type proxyFixture struct {
	srv     *Server
	cs      *mcpsdk.ClientSession
	root    string
	envFile string
	cancel  context.CancelFunc
}

func newProxyFixture(t *testing.T, leases map[string]json.RawMessage) *proxyFixture {
	t.Helper()
	root := t.TempDir()
	envFile := filepath.Join(root, "child.env")
	lifecycle, cancel := context.WithCancel(context.Background())

	cfg := serverConfig{
		Call: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			if method != "lease.get" && method != "lease.release" {
				return nil, fmt.Errorf("unexpected method %s", method)
			}
			raw, err := json.Marshal(params)
			if err != nil {
				return nil, err
			}
			var p struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			leaseRaw, ok := leases[p.ID]
			if !ok {
				env, err := ipc.NewError("LEASE_NOT_FOUND", "lease not found: "+p.ID, "simurgh acquire --model <model>")
				if err != nil {
					return nil, err
				}
				return nil, &rpcError{e: env}
			}
			return leaseRaw, nil
		},
		LastLeasePath:  filepath.Join(root, "last-lease"),
		LeasesRoot:     filepath.Join(root, "leases"),
		LaunchCommand:  fakeXBMCPLaunchCommand(),
		LaunchExtraEnv: []string{envRunFakeXBMCP + "=1", envFakeXBMCPEnvFile + "=" + envFile},
		LifecycleCtx:   lifecycle,
	}
	srv := newServer(cfg)
	cs := connectTestClient(t, srv.Server)
	f := &proxyFixture{srv: srv, cs: cs, root: root, envFile: envFile, cancel: cancel}
	t.Cleanup(func() {
		cancel()
		srv.impl.stopAllChildren()
	})
	return f
}

func waitFile(t *testing.T, path string, d time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil && len(raw) > 0 {
			return raw
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
	return nil
}

func processAlivePID(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func waitDeadPID(t *testing.T, pid int, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if !processAlivePID(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pid %d still alive after %s", pid, d)
}

func TestXBMCPProxyCallSpawnsWithIsolationEnv(t *testing.T) {
	leases := map[string]json.RawMessage{
		"lease-env": leaseGetJSON("lease-env", "AAAAAAAA-0000-0000-0000-0000000000E1"),
	}
	f := newProxyFixture(t, leases)

	res, err := f.cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "xcodebuildmcp_call",
		Arguments: map[string]any{
			"lease_id":  "lease-env",
			"tool":      "echo",
			"arguments": map[string]any{"message": "hi"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %v", toolErrorText(t, res))
	}

	envText := string(waitFile(t, f.envFile, 3*time.Second))
	for _, want := range []string{
		"SIMURGH_LEASE_ID=lease-env",
		"SIMURGH_UDID=AAAAAAAA-0000-0000-0000-0000000000E1",
		"XCODEBUILDMCP_SIMULATOR_ID=AAAAAAAA-0000-0000-0000-0000000000E1",
	} {
		if !strings.Contains(envText, want) {
			t.Fatalf("child env missing %q\n%s", want, envText)
		}
	}
	pid := f.srv.impl.childPID("lease-env")
	if pid <= 0 || !processAlivePID(pid) {
		t.Fatalf("child pid=%d not alive", pid)
	}
}

func TestXBMCPProxyCallForwardsArgsAndResult(t *testing.T) {
	leases := map[string]json.RawMessage{
		"lease-fwd": leaseGetJSON("lease-fwd", "AAAAAAAA-0000-0000-0000-0000000000F2"),
	}
	f := newProxyFixture(t, leases)

	res, err := f.cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "xcodebuildmcp_call",
		Arguments: map[string]any{
			"lease_id":  "lease-fwd",
			"tool":      "echo",
			"arguments": map[string]any{"message": "round-trip"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected IsError: %v", toolErrorText(t, res))
	}
	var got string
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			got += tc.Text
		}
	}
	if got != "echo:round-trip" {
		t.Fatalf("result text=%q want echo:round-trip", got)
	}

	list, err := f.cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "xcodebuildmcp_tools",
		Arguments: map[string]any{"lease_id": "lease-fwd"},
	})
	if err != nil {
		t.Fatalf("tools: %v", err)
	}
	if list.IsError {
		t.Fatalf("tools error: %v", toolErrorText(t, list))
	}
	out := decodeStructured[xbmcpToolsResult](t, list)
	if len(out.Tools) != 1 || out.Tools[0].Name != "echo" {
		t.Fatalf("tools=%+v", out.Tools)
	}
	if !strings.Contains(out.Tools[0].Description, "echo") {
		t.Fatalf("description=%q", out.Tools[0].Description)
	}
}

func TestXBMCPProxyTwoLeasesDistinctChildren(t *testing.T) {
	leases := map[string]json.RawMessage{
		"lease-a": leaseGetJSON("lease-a", "AAAAAAAA-0000-0000-0000-0000000000AA"),
		"lease-b": leaseGetJSON("lease-b", "BBBBBBBB-0000-0000-0000-0000000000BB"),
	}
	// Separate env files per child: inject via per-spawn path under lease root.
	// Use a fixture that writes env next to each lease; override LaunchExtraEnv
	// is shared, so use a dir and let the fake write to SIMURGH_LEASE_ID-named file.
	root := t.TempDir()
	lifecycle, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	envDir := filepath.Join(root, "envs")
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := serverConfig{
		Call: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			raw, _ := json.Marshal(params)
			var p struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(raw, &p)
			leaseRaw, ok := leases[p.ID]
			if !ok {
				env, _ := ipc.NewError("LEASE_NOT_FOUND", "lease not found: "+p.ID, "simurgh acquire --model <model>")
				return nil, &rpcError{e: env}
			}
			return leaseRaw, nil
		},
		LastLeasePath: filepath.Join(root, "last-lease"),
		LeasesRoot:    filepath.Join(root, "leases"),
		LaunchCommand: fakeXBMCPLaunchCommand(),
		// Env file path includes a token replaced? Use LaunchExtraEnvFactory instead.
		LaunchExtraEnv: nil,
		LifecycleCtx:   lifecycle,
		LaunchExtraEnvFunc: func(leaseID string) []string {
			return []string{
				envRunFakeXBMCP + "=1",
				envFakeXBMCPEnvFile + "=" + filepath.Join(envDir, leaseID+".env"),
			}
		},
	}
	srv := newServer(cfg)
	t.Cleanup(func() {
		cancel()
		srv.impl.stopAllChildren()
	})
	cs := connectTestClient(t, srv.Server)

	for _, id := range []string{"lease-a", "lease-b"} {
		res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
			Name: "xcodebuildmcp_call",
			Arguments: map[string]any{
				"lease_id":  id,
				"tool":      "echo",
				"arguments": map[string]any{"message": id},
			},
		})
		if err != nil {
			t.Fatalf("%s CallTool: %v", id, err)
		}
		if res.IsError {
			t.Fatalf("%s error: %v", id, toolErrorText(t, res))
		}
	}

	pidA := srv.impl.childPID("lease-a")
	pidB := srv.impl.childPID("lease-b")
	if pidA <= 0 || pidB <= 0 || pidA == pidB {
		t.Fatalf("want distinct live PIDs, got A=%d B=%d", pidA, pidB)
	}
	if !processAlivePID(pidA) || !processAlivePID(pidB) {
		t.Fatalf("children not alive A=%d B=%d", pidA, pidB)
	}
	envA := string(waitFile(t, filepath.Join(envDir, "lease-a.env"), 3*time.Second))
	envB := string(waitFile(t, filepath.Join(envDir, "lease-b.env"), 3*time.Second))
	if !strings.Contains(envA, "SIMURGH_LEASE_ID=lease-a") || !strings.Contains(envB, "SIMURGH_LEASE_ID=lease-b") {
		t.Fatalf("env attribution failed\nA:\n%s\nB:\n%s", envA, envB)
	}
	if strings.Contains(envA, "SIMURGH_LEASE_ID=lease-b") || strings.Contains(envB, "SIMURGH_LEASE_ID=lease-a") {
		t.Fatalf("env cross-contamination\nA:\n%s\nB:\n%s", envA, envB)
	}
}

func TestXBMCPProxyStopThenRespawn(t *testing.T) {
	leases := map[string]json.RawMessage{
		"lease-stop": leaseGetJSON("lease-stop", "AAAAAAAA-0000-0000-0000-0000000000S3"),
	}
	f := newProxyFixture(t, leases)

	_, err := f.cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "xcodebuildmcp_call",
		Arguments: map[string]any{
			"lease_id":  "lease-stop",
			"tool":      "echo",
			"arguments": map[string]any{"message": "1"},
		},
	})
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	pid1 := f.srv.impl.childPID("lease-stop")
	if pid1 <= 0 {
		t.Fatal("no child after first call")
	}

	stopRes, err := f.cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "xcodebuildmcp_stop",
		Arguments: map[string]any{"lease_id": "lease-stop"},
	})
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if stopRes.IsError {
		t.Fatalf("stop error: %v", toolErrorText(t, stopRes))
	}
	waitDeadPID(t, pid1, 3*time.Second)

	// Idempotent second stop.
	stop2, err := f.cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "xcodebuildmcp_stop",
		Arguments: map[string]any{"lease_id": "lease-stop"},
	})
	if err != nil || stop2.IsError {
		t.Fatalf("idempotent stop: err=%v isError=%v", err, stop2 != nil && stop2.IsError)
	}

	_, err = f.cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "xcodebuildmcp_call",
		Arguments: map[string]any{
			"lease_id":  "lease-stop",
			"tool":      "echo",
			"arguments": map[string]any{"message": "2"},
		},
	})
	if err != nil {
		t.Fatalf("respawn call: %v", err)
	}
	pid2 := f.srv.impl.childPID("lease-stop")
	if pid2 <= 0 || pid2 == pid1 {
		t.Fatalf("want new live pid after respawn, got pid2=%d pid1=%d", pid2, pid1)
	}
	if !processAlivePID(pid2) {
		t.Fatalf("respawned pid %d not alive", pid2)
	}
}

func TestXBMCPProxyUnknownLeaseStructuredErrorNoSpawn(t *testing.T) {
	f := newProxyFixture(t, map[string]json.RawMessage{})

	res, err := f.cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "xcodebuildmcp_call",
		Arguments: map[string]any{
			"lease_id":  "lease-missing",
			"tool":      "echo",
			"arguments": map[string]any{"message": "nope"},
		},
	})
	if err != nil {
		t.Fatalf("protocol error (want tool-level structured error): %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true for unknown lease")
	}
	text := toolErrorText(t, res)
	if !strings.Contains(text, "LEASE_NOT_FOUND") {
		t.Fatalf("want LEASE_NOT_FOUND in error, got %q", text)
	}
	if !strings.Contains(text, "fix:") {
		t.Fatalf("want remediation fix in error, got %q", text)
	}
	if f.srv.impl.childPID("lease-missing") != 0 {
		t.Fatal("child spawned for unknown lease")
	}
	if _, err := os.Stat(f.envFile); !os.IsNotExist(err) {
		t.Fatalf("env file should not exist when spawn skipped, err=%v", err)
	}
}

func TestXBMCPProxyLifecycleCancelKillsChildren(t *testing.T) {
	leases := map[string]json.RawMessage{
		"lease-life": leaseGetJSON("lease-life", "AAAAAAAA-0000-0000-0000-0000000000L6"),
	}
	f := newProxyFixture(t, leases)

	_, err := f.cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "xcodebuildmcp_call",
		Arguments: map[string]any{
			"lease_id":  "lease-life",
			"tool":      "echo",
			"arguments": map[string]any{"message": "alive"},
		},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	pid := f.srv.impl.childPID("lease-life")
	if pid <= 0 || !processAlivePID(pid) {
		t.Fatalf("child not alive pid=%d", pid)
	}

	f.cancel() // cancel LifecycleCtx
	waitDeadPID(t, pid, 5*time.Second)
}

func TestXBMCPProxyStopKillsProcessGroup(t *testing.T) {
	leases := map[string]json.RawMessage{
		"lease-pg": leaseGetJSON("lease-pg", "AAAAAAAA-0000-0000-0000-0000000000PG"),
	}
	root := t.TempDir()
	envFile := filepath.Join(root, "child.env")
	gcPIDFile := filepath.Join(root, "grandchild.pid")
	lifecycle, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cfg := serverConfig{
		Call: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			if method != "lease.get" && method != "lease.release" {
				return nil, fmt.Errorf("unexpected method %s", method)
			}
			raw, err := json.Marshal(params)
			if err != nil {
				return nil, err
			}
			var p struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			leaseRaw, ok := leases[p.ID]
			if !ok {
				env, err := ipc.NewError("LEASE_NOT_FOUND", "lease not found: "+p.ID, "simurgh acquire --model <model>")
				if err != nil {
					return nil, err
				}
				return nil, &rpcError{e: env}
			}
			return leaseRaw, nil
		},
		LastLeasePath: filepath.Join(root, "last-lease"),
		LeasesRoot:    filepath.Join(root, "leases"),
		LaunchCommand: fakeXBMCPLaunchCommand(),
		LaunchExtraEnv: []string{
			envRunFakeXBMCP + "=1",
			envFakeXBMCPEnvFile + "=" + envFile,
			envFakeXBMCPGrandchildPIDFile + "=" + gcPIDFile,
		},
		LifecycleCtx: lifecycle,
	}
	srv := newServer(cfg)
	t.Cleanup(func() {
		cancel()
		srv.impl.stopAllChildren()
	})
	cs := connectTestClient(t, srv.Server)

	_, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "xcodebuildmcp_call",
		Arguments: map[string]any{
			"lease_id":  "lease-pg",
			"tool":      "echo",
			"arguments": map[string]any{"message": "pg"},
		},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	wrapperPID := srv.impl.childPID("lease-pg")
	if wrapperPID <= 0 || !processAlivePID(wrapperPID) {
		t.Fatalf("wrapper pid=%d not alive", wrapperPID)
	}
	gcRaw := strings.TrimSpace(string(waitFile(t, gcPIDFile, 3*time.Second)))
	gcPID, err := strconv.Atoi(gcRaw)
	if err != nil || gcPID <= 0 {
		t.Fatalf("grandchild pid file %q: %v", gcRaw, err)
	}
	if !processAlivePID(gcPID) {
		t.Fatalf("grandchild pid %d not alive before stop", gcPID)
	}

	stopRes, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "xcodebuildmcp_stop",
		Arguments: map[string]any{"lease_id": "lease-pg"},
	})
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if stopRes.IsError {
		t.Fatalf("stop error: %v", toolErrorText(t, stopRes))
	}
	waitDeadPID(t, wrapperPID, 5*time.Second)
	waitDeadPID(t, gcPID, 5*time.Second)
}

func TestXBMCPProxyLeaseReleaseStopsChild(t *testing.T) {
	leases := map[string]json.RawMessage{
		"lease-rel": leaseGetJSON("lease-rel", "AAAAAAAA-0000-0000-0000-0000000000R7"),
	}
	f := newProxyFixture(t, leases)

	_, err := f.cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "xcodebuildmcp_call",
		Arguments: map[string]any{
			"lease_id":  "lease-rel",
			"tool":      "echo",
			"arguments": map[string]any{"message": "before-release"},
		},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	pid := f.srv.impl.childPID("lease-rel")
	if pid <= 0 || !processAlivePID(pid) {
		t.Fatalf("child not alive pid=%d", pid)
	}

	rel, err := f.cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "lease_release",
		Arguments: map[string]any{"lease_id": "lease-rel"},
	})
	if err != nil {
		t.Fatalf("lease_release: %v", err)
	}
	if rel.IsError {
		t.Fatalf("lease_release error: %v", toolErrorText(t, rel))
	}
	waitDeadPID(t, pid, 5*time.Second)
	if f.srv.impl.childPID("lease-rel") != 0 {
		t.Fatal("child map still has entry after lease_release")
	}
}
