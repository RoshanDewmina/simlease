package provisioner

import (
	"testing"
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
