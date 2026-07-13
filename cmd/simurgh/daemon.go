package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
	"github.com/RoshanDewmina/simurgh/internal/daemon"
	"github.com/RoshanDewmina/simurgh/internal/ipc"
	"github.com/RoshanDewmina/simurgh/internal/provisioner"
	"github.com/RoshanDewmina/simurgh/internal/reaper"
	"github.com/RoshanDewmina/simurgh/internal/safety"
	"github.com/RoshanDewmina/simurgh/internal/store"
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
	brk := broker.New(broker.BrokerOptions{
		Provisioner: prov,
		Store:       st,
		// Capacity / RAM / pressure use Broker defaults (formula + always-OK).
	})
	// Rehydrate before any client can connect so ownership is crash-durable.
	if err := brk.Rehydrate(); err != nil {
		fmt.Fprintf(os.Stderr, "simurgh daemon run: rehydrate: %v\n", err)
		return 1
	}
	safetyClient := &simctlSafetyClient{}
	rp := reaper.New(reaper.ReaperOptions{
		LeasesRoot: leasesRoot,
		Lister:     safetyClient,
		Deleter:    safetyClient,
	})

	srv := ipc.NewServer(ln)
	daemon.RegisterLeaseHandlers(srv, daemon.Deps{
		Broker:     brk,
		Reaper:     rp,
		LeasesRoot: leasesRoot,
	})

	// Default reclaim intervals (not yet configurable via config.yaml):
	// watch every 5s; orphan scan every 5m (log-only; auto-reclaim deferred).
	loop := &daemon.ReclaimLoop{Deps: daemon.Deps{
		Broker:     brk,
		Reaper:     rp,
		LeasesRoot: leasesRoot,
	}}
	go loop.Run(ctx, 5*time.Second, 5*time.Minute)

	if err := srv.Serve(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "simurgh daemon run: %v\n", err)
		return 1
	}
	return 0
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
