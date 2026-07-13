package provisioner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// DeviceType is a simctl device type entry.
type DeviceType struct {
	Name       string
	Identifier string
}

// Runtime is a simctl runtime entry.
type Runtime struct {
	Name         string
	Identifier   string
	Version      string
	IsAvailable  bool
	PlatformName string // derived: "iOS" when Name has an iOS prefix
}

// SimDevice is a simctl device entry (provisioner-local; not broker.Device).
type SimDevice struct {
	UDID                 string
	Name                 string
	State                string
	RuntimeIdentifier    string
	DeviceTypeIdentifier string
	IsAvailable          bool
}

// SimctlClient is the seam between Provisioner and real simctl.
// Product code shells out only through execSimctlClient.
type SimctlClient interface {
	ListDeviceTypes(ctx context.Context) ([]DeviceType, error)
	ListRuntimes(ctx context.Context) ([]Runtime, error)
	ListDevices(ctx context.Context) ([]SimDevice, error)
	Create(ctx context.Context, name, deviceTypeID, runtimeID string) (udid string, err error)
	Clone(ctx context.Context, sourceUDID, newName string) (udid string, err error)
	Rename(ctx context.Context, udid, newName string) error
	Boot(ctx context.Context, udid string) error
	WaitBooted(ctx context.Context, udid string) error
	Shutdown(ctx context.Context, udid string) error
}

// execSimctlClient runs xcrun simctl via exec.CommandContext.
// This is the ONLY place in this package that shells out.
type execSimctlClient struct{}

// NewExecClient returns the real simctl-backed client.
func NewExecClient() SimctlClient {
	return &execSimctlClient{}
}

func (c *execSimctlClient) ListDeviceTypes(ctx context.Context) ([]DeviceType, error) {
	out, err := c.run(ctx, "list", "devicetypes", "-j")
	if err != nil {
		return nil, err
	}
	return parseDeviceTypesJSON(out)
}

func (c *execSimctlClient) ListRuntimes(ctx context.Context) ([]Runtime, error) {
	out, err := c.run(ctx, "list", "runtimes", "-j")
	if err != nil {
		return nil, err
	}
	return parseRuntimesJSON(out)
}

func (c *execSimctlClient) ListDevices(ctx context.Context) ([]SimDevice, error) {
	out, err := c.run(ctx, "list", "devices", "-j")
	if err != nil {
		return nil, err
	}
	return parseDevicesJSON(out)
}

func (c *execSimctlClient) Create(ctx context.Context, name, deviceTypeID, runtimeID string) (string, error) {
	out, err := c.run(ctx, "create", name, deviceTypeID, runtimeID)
	if err != nil {
		return "", err
	}
	udid := strings.TrimSpace(string(out))
	if udid == "" {
		return "", fmt.Errorf("simctl create returned empty udid")
	}
	return udid, nil
}

func (c *execSimctlClient) Clone(ctx context.Context, sourceUDID, newName string) (string, error) {
	out, err := c.run(ctx, "clone", sourceUDID, newName)
	if err != nil {
		return "", err
	}
	udid := strings.TrimSpace(string(out))
	if udid == "" {
		return "", fmt.Errorf("simctl clone returned empty udid")
	}
	return udid, nil
}

func (c *execSimctlClient) Rename(ctx context.Context, udid, newName string) error {
	_, err := c.run(ctx, "rename", udid, newName)
	return err
}

func (c *execSimctlClient) Boot(ctx context.Context, udid string) error {
	_, err := c.run(ctx, "boot", udid)
	return err
}

func (c *execSimctlClient) Shutdown(ctx context.Context, udid string) error {
	_, err := c.run(ctx, "shutdown", udid)
	return err
}

// WaitBooted polls `simctl list devices -j` until the device reports Booted
// or ctx is done. Polling is preferred over `bootstatus -b` because the latter
// can outlive a reasonable provision timeout on cold boots while the device
// state already shows Booted via list.
func (c *execSimctlClient) WaitBooted(ctx context.Context, udid string) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		devices, err := c.ListDevices(ctx)
		if err == nil {
			for _, d := range devices {
				if d.UDID == udid && d.State == "Booted" {
					return nil
				}
			}
		} else if ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *execSimctlClient) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "xcrun", append([]string{"simctl"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("xcrun simctl %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.Bytes(), nil
}

func parseDeviceTypesJSON(data []byte) ([]DeviceType, error) {
	var raw struct {
		DeviceTypes []struct {
			Name       string `json:"name"`
			Identifier string `json:"identifier"`
		} `json:"devicetypes"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse device types: %w", err)
	}
	out := make([]DeviceType, 0, len(raw.DeviceTypes))
	for _, dt := range raw.DeviceTypes {
		out = append(out, DeviceType{Name: dt.Name, Identifier: dt.Identifier})
	}
	return out, nil
}

func parseRuntimesJSON(data []byte) ([]Runtime, error) {
	var raw struct {
		Runtimes []struct {
			Name        string `json:"name"`
			Identifier  string `json:"identifier"`
			Version     string `json:"version"`
			IsAvailable bool   `json:"isAvailable"`
		} `json:"runtimes"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse runtimes: %w", err)
	}
	out := make([]Runtime, 0, len(raw.Runtimes))
	for _, rt := range raw.Runtimes {
		platform := ""
		if strings.HasPrefix(rt.Name, "iOS") {
			platform = "iOS"
		}
		out = append(out, Runtime{
			Name:         rt.Name,
			Identifier:   rt.Identifier,
			Version:      rt.Version,
			IsAvailable:  rt.IsAvailable,
			PlatformName: platform,
		})
	}
	return out, nil
}

func parseDevicesJSON(data []byte) ([]SimDevice, error) {
	var raw struct {
		Devices map[string][]struct {
			UDID                 string `json:"udid"`
			Name                 string `json:"name"`
			State                string `json:"state"`
			IsAvailable          bool   `json:"isAvailable"`
			DeviceTypeIdentifier string `json:"deviceTypeIdentifier"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse devices: %w", err)
	}
	var out []SimDevice
	for runtimeID, devices := range raw.Devices {
		for _, d := range devices {
			out = append(out, SimDevice{
				UDID:                 d.UDID,
				Name:                 d.Name,
				State:                d.State,
				RuntimeIdentifier:    runtimeID,
				DeviceTypeIdentifier: d.DeviceTypeIdentifier,
				IsAvailable:          d.IsAvailable,
			})
		}
	}
	return out, nil
}
