package provisioner

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestParseDeviceTypesJSON(t *testing.T) {
	data := []byte(`{
		"devicetypes": [
			{"name": "iPhone 17 Pro", "identifier": "com.apple.CoreSimulator.SimDeviceType.iPhone-17-Pro"},
			{"name": "iPhone SE (3rd generation)", "identifier": "com.apple.CoreSimulator.SimDeviceType.iPhone-SE-3rd-generation"}
		]
	}`)
	types, err := parseDeviceTypesJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 2 {
		t.Fatalf("len=%d want 2", len(types))
	}
	if types[0].Name != "iPhone 17 Pro" || types[0].Identifier == "" {
		t.Fatalf("got %+v", types[0])
	}
}

func TestWaitBootedPollReadinessMappings(t *testing.T) {
	t.Run("timeout classified", func(t *testing.T) {
		client := &execSimctlClient{
			listDevices: func(context.Context) ([]SimDevice, error) {
				return []SimDevice{{UDID: "U1", State: "Booting"}}, nil
			},
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		err := client.WaitBooted(ctx, "U1")
		var timeout *ReadinessTimeoutError
		if !errors.As(err, &timeout) {
			t.Fatalf("err=%T %v; want ReadinessTimeoutError", err, err)
		}
	})

	t.Run("caller cancellation retained", func(t *testing.T) {
		client := &execSimctlClient{
			listDevices: func(context.Context) ([]SimDevice, error) {
				return []SimDevice{{UDID: "U1", State: "Booting"}}, nil
			},
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := client.WaitBooted(ctx, "U1")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%T %v; want context.Canceled", err, err)
		}
	})
}

func TestParseRuntimesJSON(t *testing.T) {
	data := []byte(`{
		"runtimes": [
			{
				"name": "iOS 27.0",
				"identifier": "com.apple.CoreSimulator.SimRuntime.iOS-27-0",
				"version": "27.0",
				"isAvailable": true
			},
			{
				"name": "watchOS 27.0",
				"identifier": "com.apple.CoreSimulator.SimRuntime.watchOS-27-0",
				"version": "27.0",
				"isAvailable": true
			}
		]
	}`)
	runtimes, err := parseRuntimesJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimes) != 2 {
		t.Fatalf("len=%d want 2", len(runtimes))
	}
	if runtimes[0].PlatformName != "iOS" {
		t.Fatalf("iOS platform=%q", runtimes[0].PlatformName)
	}
	if runtimes[1].PlatformName != "" {
		t.Fatalf("watchOS platform=%q want empty", runtimes[1].PlatformName)
	}
}

func TestParseDevicesJSON(t *testing.T) {
	data := []byte(`{
		"devices": {
			"com.apple.CoreSimulator.SimRuntime.iOS-27-0": [
				{
					"udid": "AAAA-BBBB",
					"name": "iPhone 17 Pro",
					"state": "Shutdown",
					"isAvailable": true,
					"deviceTypeIdentifier": "com.apple.CoreSimulator.SimDeviceType.iPhone-17-Pro"
				}
			]
		}
	}`)
	devices, err := parseDevicesJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 {
		t.Fatalf("len=%d want 1", len(devices))
	}
	d := devices[0]
	if d.UDID != "AAAA-BBBB" || d.RuntimeIdentifier != "com.apple.CoreSimulator.SimRuntime.iOS-27-0" {
		t.Fatalf("got %+v", d)
	}
}
