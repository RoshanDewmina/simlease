package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/cli"
	"github.com/RoshanDewmina/simurgh/internal/ipc"
)

const doctorPingTimeout = 2 * time.Second

func runDoctor(jsonOut bool) int {
	socketPath, err := DefaultSocketPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "simurgh doctor: %v\n", err)
		return 4
	}
	leasesRoot, err := DefaultLeasesRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "simurgh doctor: %v\n", err)
		return 4
	}

	opts := cli.DoctorOptions{
		XcodeVersion:   xcodeVersion,
		SimctlRuntimes: simctlRuntimes,
		SocketPath:     socketPath,
		DialPing:       dialPing(socketPath),
		ListLeases:     listLeasesForDoctor,
		LeasesRoot:     leasesRoot,
	}

	checks := cli.RunChecks(context.Background(), opts)
	if err := printDoctorChecks(checks, jsonOut); err != nil {
		fmt.Fprintf(os.Stderr, "simurgh doctor: %v\n", err)
		return 4
	}
	return cli.ExitCode(checks)
}

func printDoctorChecks(checks []cli.Check, jsonOut bool) error {
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(checks)
	}
	for _, c := range checks {
		status := "ok"
		if !c.OK {
			status = "FAIL"
		}
		fmt.Printf("%-4s %-8s %s\n", status, c.Name, c.Message)
		if !c.OK {
			fmt.Printf("     code: %s\n", c.Code)
			fmt.Printf("     fix:  %s\n", c.Fix)
		}
	}
	return nil
}

func xcodeVersion(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "xcodebuild", "-version").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func simctlRuntimes(ctx context.Context) ([]string, error) {
	out, err := exec.CommandContext(ctx, "xcrun", "simctl", "list", "runtimes", "-j").Output()
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Runtimes []struct {
			Name        string `json:"name"`
			IsAvailable bool   `json:"isAvailable"`
		} `json:"runtimes"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("parse simctl runtimes JSON: %w", err)
	}
	names := make([]string, 0, len(parsed.Runtimes))
	for _, r := range parsed.Runtimes {
		if !r.IsAvailable || r.Name == "" {
			continue
		}
		names = append(names, r.Name)
	}
	return names, nil
}

// dialPing returns a non-auto-starting daemon.ping probe. It dials the socket
// directly (never ipc.Dial) so doctor cannot spawn a daemon as a side effect.
func dialPing(socketPath string) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		timeout := doctorPingTimeout
		if deadline, ok := ctx.Deadline(); ok {
			if rem := time.Until(deadline); rem > 0 && rem < timeout {
				timeout = rem
			}
		}

		conn, err := net.DialTimeout("unix", socketPath, timeout)
		if err != nil {
			return err
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(timeout))

		req := ipc.Request{
			V:      ipc.ProtocolVersion,
			ID:     "doctor-ping",
			Method: ipc.MethodDaemonPing,
		}
		if err := ipc.Encode(conn, req); err != nil {
			return err
		}
		resp, err := ipc.NewDecoder(conn).DecodeResponse()
		if err != nil {
			return err
		}
		if resp.ID != req.ID {
			return fmt.Errorf("ping response id mismatch: got %q want %q", resp.ID, req.ID)
		}
		if !resp.OK {
			if resp.Error != nil {
				return fmt.Errorf("%s: %s", resp.Error.Code, resp.Error.Message)
			}
			return fmt.Errorf("daemon.ping returned ok=false")
		}
		return nil
	}
}

func listLeasesForDoctor(ctx context.Context) ([]cli.LeaseExpiryInfo, error) {
	raw, err := dialAndCall(ctx, "lease.list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var wrap struct {
		Leases []struct {
			ID        string    `json:"id"`
			State     string    `json:"state"`
			ExpiresAt time.Time `json:"expiresAt"`
		} `json:"leases"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, err
	}
	out := make([]cli.LeaseExpiryInfo, 0, len(wrap.Leases))
	for _, l := range wrap.Leases {
		out = append(out, cli.LeaseExpiryInfo{
			ID:        l.ID,
			State:     l.State,
			ExpiresAt: l.ExpiresAt,
		})
	}
	return out, nil
}
