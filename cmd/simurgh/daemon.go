package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
	"github.com/RoshanDewmina/simurgh/internal/controlplane"
	"github.com/RoshanDewmina/simurgh/internal/daemon"
	xbmcp "github.com/RoshanDewmina/simurgh/internal/integrations/xcodebuildmcp"
	"github.com/RoshanDewmina/simurgh/internal/ipc"
	"github.com/RoshanDewmina/simurgh/internal/logrotate"
	"github.com/RoshanDewmina/simurgh/internal/pressure"
	"github.com/RoshanDewmina/simurgh/internal/provisioner"
	"github.com/RoshanDewmina/simurgh/internal/reaper"
	"github.com/RoshanDewmina/simurgh/internal/safety"
	"github.com/RoshanDewmina/simurgh/internal/store"
)

const (
	daemonStatusPingTimeout = 2 * time.Second
	daemonStatusLogLines    = 50
)

// simurghHomeDir resolves the on-disk root: $SIMURGH_HOME if set, else ~/.simurgh.
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

// DefaultSocketPath returns the production Unix socket path.
func DefaultSocketPath() (string, error) {
	home, err := simurghHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "simurghd.sock"), nil
}

// DefaultLogPath returns the production daemon log path.
func DefaultLogPath() (string, error) {
	home, err := simurghHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "logs", "simurghd.log"), nil
}

// DefaultLeasesRoot returns ~/.simurgh/leases (honors $SIMURGH_HOME).
func DefaultLeasesRoot() (string, error) {
	home, err := simurghHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "leases"), nil
}

// DefaultLastLeasePath returns ~/.simurgh/last-lease (honors $SIMURGH_HOME).
func DefaultLastLeasePath() (string, error) {
	home, err := simurghHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "last-lease"), nil
}

// DefaultStatePath returns ~/.simurgh/state.json (honors $SIMURGH_HOME).
func DefaultStatePath() (string, error) {
	home, err := simurghHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "state.json"), nil
}

func runDaemon() int {
	// Auto-start via ipc.Dial uses Setsid + stdin=/dev/null, but a controlling
	// terminal can still deliver SIGHUP when the parent shell exits. Ignore it
	// so the daemon survives parent death; SIGINT/SIGTERM still shut down.
	signal.Ignore(syscall.SIGHUP)

	socketPath, err := DefaultSocketPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "simurgh daemon run: %v\n", err)
		return 1
	}
	leasesRoot, err := DefaultLeasesRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "simurgh daemon run: %v\n", err)
		return 1
	}
	if err := os.MkdirAll(leasesRoot, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "simurgh daemon run: leases dir: %v\n", err)
		return 1
	}
	statePath, err := DefaultStatePath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "simurgh daemon run: %v\n", err)
		return 1
	}
	logPath, err := DefaultLogPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "simurgh daemon run: %v\n", err)
		return 1
	}
	logWriter, err := logrotate.New(logPath, logrotate.DefaultMaxBytes, logrotate.DefaultMaxBackups)
	if err != nil {
		fmt.Fprintf(os.Stderr, "simurgh daemon run: log: %v\n", err)
		return 1
	}
	defer logWriter.Close()

	logger := slog.New(slog.NewTextHandler(logWriter, nil))
	printf := slogPrintf(logger)

	ln, err := ipc.Listen(socketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "simurgh daemon run: %v\n", err)
		return 1
	}
	defer ln.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	prov := provisioner.New(provisioner.NewExecClient())
	st := store.New(statePath)
	capacity := 0 // 0 = broker.New computes it from the RAM/CPU seams
	if v := os.Getenv("SIMURGH_CAPACITY"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			fmt.Fprintf(os.Stderr, "simurgh daemon run: SIMURGH_CAPACITY must be a positive integer, got %q\n", v)
			return 1
		}
		capacity = n
	}
	perSpec := 0
	if v := os.Getenv("SIMURGH_PER_SPEC_CAPACITY"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			fmt.Fprintf(os.Stderr, "simurgh daemon run: SIMURGH_PER_SPEC_CAPACITY must be a positive integer, got %q\n", v)
			return 1
		}
		perSpec = n
	}
	maxProv := 0
	if v := os.Getenv("SIMURGH_MAX_PROVISIONING"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			fmt.Fprintf(os.Stderr, "simurgh daemon run: SIMURGH_MAX_PROVISIONING must be a positive integer, got %q\n", v)
			return 1
		}
		maxProv = n
	}
	pressureOK := func() (bool, string) {
		r := pressure.Check(leasesRoot)
		return r.OK, r.Reason
	}
	// Test-only seam: this dev host's baseline free+inactive RAM regularly
	// sits under the 4 GiB pressure-gate default under normal daily load
	// (browser/editor tabs), which is real and correct production behavior
	// but has nothing to do with R-WP6/B2 (adapter process-lifetime
	// ownership) — it would otherwise make an unrelated resource gate the
	// bottleneck for a CLI-integration test that needs a real ACTIVE lease.
	// R-WP6 second-review Fix 2: the SIMURGH_TEST_SKIP_PRESSURE env-var read
	// itself is compiled in only under -tags darwin_sim (see
	// xbmcp_test_seams_darwin_sim.go / xbmcp_test_seams_other.go) — a
	// tagless `go build ./...` (the real shipped binary) links the no-op, so
	// this seam has zero effect in production no matter what any process's
	// environment contains.
	pressureOK = applyTestSkipPressureOverride(pressureOK)
	brk := broker.New(broker.BrokerOptions{
		Provisioner:               prov,
		Store:                     st,
		Logger:                    printf,
		Capacity:                  capacity,
		PerSpecCapacity:           perSpec,
		MaxProvisioningOperations: maxProv,
		PressureOK:                pressureOK,
	})
	// Rehydrate before any client can connect so ownership is crash-durable.
	if err := brk.Rehydrate(); err != nil {
		fmt.Fprintf(os.Stderr, "simurgh daemon run: rehydrate: %v\n", err)
		return 1
	}
	buildSlots, err := controlplane.BuildSlotsFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "simurgh daemon run: %v\n", err)
		return 1
	}
	cp, err := controlplane.New(controlplane.Options{
		BuildSlots: buildSlots,
		Broker:     brk,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "simurgh daemon run: controlplane: %v\n", err)
		return 1
	}
	safetyClient := &simctlSafetyClient{}
	rp := reaper.New(reaper.ReaperOptions{
		LeasesRoot: leasesRoot,
		Lister:     safetyClient,
		Deleter:    safetyClient,
	})

	// Single long-lived XcodeBuildMCP adapter for the daemon's whole lifetime
	// (R-WP6 review finding B2): its stdinHolders map must survive across
	// separate CLI invocations of `simurgh integration xcodebuildmcp
	// start|stop|status`, which a per-CLI-call Adapter (the pre-fix behavior)
	// could never do — the moment the one-shot `start` process exited, its
	// held stdin fd closed and the child saw EOF. GetLease reaches the broker
	// directly in-process; no IPC round-trip needed since the adapter now
	// lives where the broker lives.
	xbmcpAdapter := xbmcp.NewAdapter(leasesRoot, func(_ context.Context, id string) (broker.Lease, error) {
		lease, ok := brk.Get(id)
		if !ok {
			// R-WP6 second-review Fix 3: reuse the broker's own typed
			// "lease not found" error (not a bare fmt.Errorf) so
			// internal/daemon's xbmcpErrorToIPC classifies it as
			// LEASE_NOT_FOUND/exit 3 — matching the pre-B2-fix CLI→IPC
			// lease.get path exactly, instead of falling through to
			// generic INTERNAL/exit 5.
			return broker.Lease{}, broker.NewLeaseNotFoundError(id)
		}
		return lease, nil
	})
	if ver, err := xbmcp.RecordVersion(nil); err == nil && ver != "" {
		xbmcpAdapter.Version = ver
		xbmcpAdapter.ProbeVersion = func() (string, error) { return ver, nil }
	}
	// Test-only seam: lets CLI-level regression tests substitute a fake
	// long-lived child for the real `npx xcodebuildmcp mcp` server (which
	// needs network + the real npm package) — mirrors the Adapter.Command
	// override the in-process live test (runtime_live_test.go) already uses.
	// R-WP6 second-review Fix 2: same compile-time gate as pressureOK above —
	// the SIMURGH_XBMCP_TEST_COMMAND env-var read only exists under -tags
	// darwin_sim; a tagless build's applyXBMCPTestCommandOverride is a no-op.
	applyXBMCPTestCommandOverride(xbmcpAdapter)

	srv := ipc.NewServer(ln)
	deps := daemon.Deps{
		Broker:        brk,
		Reaper:        rp,
		Provisioner:   prov,
		ControlPlane:  cp,
		LeasesRoot:    leasesRoot,
		DeviceLister:  safetyClient,
		DeviceDeleter: safetyClient,
		XBMCPAdapter:  xbmcpAdapter,
	}
	daemon.RegisterLeaseHandlers(srv, deps)
	daemon.RegisterXcodeBuildMCPHandlers(srv, deps)
	// Rehydrate may have converted a crashed PROVISIONING lease to FAILED,
	// freeing a global slot while other specs remain QUEUED. Advance once now,
	// after all daemon dependencies and handlers are ready, instead of waiting
	// for the first 5s periodic reclaim tick.
	if err := daemon.AdvanceRehydratedQueues(ctx, deps); err != nil {
		fmt.Fprintf(os.Stderr, "simurgh daemon run: advance rehydrated queues: %v\n", err)
		return 1
	}

	// Default reclaim intervals (not yet configurable via config.yaml):
	// watch every 5s; orphan scan every 5m (log-only; auto-reclaim deferred).
	loop := &daemon.ReclaimLoop{Deps: deps, Logger: printf}
	go loop.Run(ctx, 5*time.Second, 5*time.Minute)

	logger.Info("daemon listening", "socket", socketPath, "log", logPath)
	if err := srv.Serve(ctx); err != nil {
		logger.Error("daemon serve stopped", "err", err)
		fmt.Fprintf(os.Stderr, "simurgh daemon run: %v\n", err)
		return 1
	}
	logger.Info("daemon stopped")
	return 0
}

// slogPrintf adapts *slog.Logger to the broker/ReclaimLoop Logger seam
// (func(format string, args ...any)) without widening those field types.
func slogPrintf(l *slog.Logger) func(format string, args ...any) {
	return func(format string, args ...any) {
		l.Info(fmt.Sprintf(format, args...))
	}
}

// runDaemonStatus reports whether the daemon is reachable via a direct dial
// (never ipc.Dial — must not auto-start). Exit 0 if reachable, 4 if not.
// --logs tails the last daemonStatusLogLines of DefaultLogPath(); a missing
// or empty log file is not an error ("no logs yet").
func runDaemonStatus(logsFlag, jsonFlag bool) int {
	socketPath, err := DefaultSocketPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "simurgh daemon status: %v\n", err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), daemonStatusPingTimeout)
	defer cancel()
	reachable := dialPing(socketPath)(ctx) == nil

	var logLines []string
	var logNote string
	if logsFlag {
		logPath, err := DefaultLogPath()
		if err != nil {
			fmt.Fprintf(os.Stderr, "simurgh daemon status: %v\n", err)
			return 1
		}
		lines, err := tailLogLines(logPath, daemonStatusLogLines)
		if err != nil {
			if os.IsNotExist(err) {
				logNote = "no logs yet"
			} else {
				fmt.Fprintf(os.Stderr, "simurgh daemon status: read logs: %v\n", err)
				return 1
			}
		} else if len(lines) == 0 {
			logNote = "no logs yet"
		} else {
			logLines = lines
		}
	}

	if jsonFlag {
		out := map[string]any{
			"reachable": reachable,
			"socket":    socketPath,
		}
		if logsFlag {
			if logNote != "" {
				out["logs"] = logNote
			} else {
				out["logs"] = logLines
			}
		}
		_ = json.NewEncoder(os.Stdout).Encode(out)
	} else {
		if reachable {
			fmt.Println("reachable")
		} else {
			fmt.Println("unreachable")
		}
		if logsFlag {
			if logNote != "" {
				fmt.Println(logNote)
			} else {
				for _, line := range logLines {
					fmt.Println(line)
				}
			}
		}
	}

	if reachable {
		return 0
	}
	return 4
}

// tailLogLines returns up to n trailing lines from path.
// Small files are read whole; larger files are read from a trailing window.
func tailLogLines(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	const maxWindow = 256 << 10 // 256 KiB is plenty for ~50 text lines
	var data []byte
	if info.Size() <= maxWindow {
		data, err = io.ReadAll(f)
		if err != nil {
			return nil, err
		}
	} else {
		if _, err := f.Seek(-maxWindow, io.SeekEnd); err != nil {
			return nil, err
		}
		data, err = io.ReadAll(f)
		if err != nil {
			return nil, err
		}
		// Drop the first partial line after a mid-file seek.
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		}
	}

	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return nil, nil
	}
	all := strings.Split(text, "\n")
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}

// simctlSafetyClient adapts real simctl list/shutdown/delete to safety.DeviceLister
// and safety.DeviceDeleter. Provisioner's SimctlClient does not expose Shutdown/Delete
// (provisioner never deletes), so the daemon owns this thin adapter.
type simctlSafetyClient struct{}

func (c *simctlSafetyClient) ListDevices(ctx context.Context) ([]safety.Device, error) {
	out, err := simctlRun(ctx, "list", "devices", "-j")
	if err != nil {
		return nil, err
	}
	var raw struct {
		Devices map[string][]struct {
			UDID string `json:"udid"`
			Name string `json:"name"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("simctl list devices: %w", err)
	}
	var devices []safety.Device
	for _, group := range raw.Devices {
		for _, d := range group {
			devices = append(devices, safety.Device{UDID: d.UDID, Name: d.Name})
		}
	}
	return devices, nil
}

func (c *simctlSafetyClient) Shutdown(ctx context.Context, udid string) error {
	_, err := simctlRun(ctx, "shutdown", udid)
	return err
}

func (c *simctlSafetyClient) Delete(ctx context.Context, udid string) error {
	_, err := simctlRun(ctx, "delete", udid)
	return err
}

func simctlRun(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "xcrun", append([]string{"simctl"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := bytes.TrimSpace(stderr.Bytes())
		if len(msg) == 0 {
			msg = bytes.TrimSpace(stdout.Bytes())
		}
		if len(msg) > 0 {
			return nil, fmt.Errorf("xcrun simctl %v: %w: %s", args, err, msg)
		}
		return nil, fmt.Errorf("xcrun simctl %v: %w", args, err)
	}
	return stdout.Bytes(), nil
}
