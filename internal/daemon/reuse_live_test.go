//go:build darwin_sim

package daemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
	"github.com/RoshanDewmina/simurgh/internal/isolator"
	"github.com/RoshanDewmina/simurgh/internal/provisioner"
	"github.com/RoshanDewmina/simurgh/internal/reaper"
)

// TestReuseIfExistsLive performs two real compatible acquisitions and asserts
// the same lease ID, UDID, and isolation paths without reprovisioning.
// Cleanup deletes only the clone UDID this test created.
func TestReuseIfExistsLive(t *testing.T) {
	t.Setenv("SIMURGH_SESSION_ID", "r-wp1-live-session")
	client := provisioner.NewExecClient()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	runtimes, err := client.ListRuntimes(ctx)
	if err != nil {
		t.Fatalf("ListRuntimes: %v", err)
	}
	hasIOS := false
	for _, rt := range runtimes {
		if rt.IsAvailable && (rt.PlatformName == "iOS" || strings.HasPrefix(rt.Name, "iOS")) {
			hasIOS = true
			break
		}
	}
	if !hasIOS {
		t.Skip("no iOS runtime installed")
	}

	leasesRoot := t.TempDir()
	prov := provisioner.New(client)
	b := broker.New(broker.BrokerOptions{
		Capacity:    2,
		Provisioner: prov,
		Clock:       time.Now,
		PressureOK:  func() (bool, string) { return true, "" },
	})
	deps := Deps{
		Broker:      b,
		Provisioner: prov,
		LeasesRoot:  leasesRoot,
		Reaper: reaper.New(reaper.ReaperOptions{
			LeasesRoot: leasesRoot,
			Lister:     &fakeLister{},
			Deleter:    &fakeDeleter{},
			StartedAt:  func() time.Time { return time.Now().Add(-time.Minute) },
			Now:        time.Now,
		}),
	}
	h := newHandlers(deps)

	labels := map[string]string{"project": "r-wp1-live", "task": "reuse"}
	owner := map[string]any{
		"pid": 1, "agent": "r-wp1-live", "sessionId": os.Getenv("SIMURGH_SESSION_ID"),
	}

	firstResp := callHandler(t, h.acquire, "live-1", map[string]any{
		"spec":          map[string]string{"platform": "iOS", "model": "iPhone 17 Pro"},
		"labels":        labels,
		"ttl":           "15m",
		"wait":          true,
		"reuseIfExists": false,
		"owner":         owner,
	})
	if !firstResp.OK {
		t.Fatalf("first acquire: %+v", firstResp.Error)
	}
	firstWrap := decodeLiveLease(t, firstResp.Result)
	first := firstWrap.Lease
	if firstWrap.Reused {
		t.Fatal("first live allocation reported reused=true")
	}
	if first.Device == nil || first.Device.UDID == "" {
		t.Fatalf("first missing device: %+v", first)
	}
	defer cleanupLiveClone(t, first.Device.UDID)

	dirs := isolator.LeaseDirs{
		Root:        filepath.Join(leasesRoot, first.ID),
		DerivedData: filepath.Join(leasesRoot, first.ID, "DerivedData"),
		ModuleCache: filepath.Join(leasesRoot, first.ID, "ModuleCache"),
		SwiftPM:     filepath.Join(leasesRoot, first.ID, "SwiftPM"),
		Results:     filepath.Join(leasesRoot, first.ID, "Results"),
		Tmp:         filepath.Join(leasesRoot, first.ID, "tmp"),
	}
	env1 := isolator.BuildEnv(dirs, first.ID, first.Device.UDID)

	secondResp := callHandler(t, h.acquire, "live-2", map[string]any{
		"spec":          map[string]string{"platform": "iOS", "model": "iPhone 17 Pro"},
		"labels":        labels,
		"ttl":           "20m",
		"wait":          true,
		"reuseIfExists": true,
		"owner": map[string]any{
			"pid": 2, "agent": "r-wp1-live", "sessionId": os.Getenv("SIMURGH_SESSION_ID"),
		},
	})
	if !secondResp.OK {
		t.Fatalf("reuse acquire: %+v", secondResp.Error)
	}
	secondWrap := decodeLiveLease(t, secondResp.Result)
	second := secondWrap.Lease
	if !secondWrap.Reused {
		t.Fatal("second live compatible acquisition missing reused=true")
	}
	if second.ID != first.ID {
		t.Fatalf("lease ID=%s want %s (reuse)", second.ID, first.ID)
	}
	if second.Device == nil || second.Device.UDID != first.Device.UDID {
		t.Fatalf("UDID=%v want %s", second.Device, first.Device.UDID)
	}
	if second.Env.Env["SIMURGH_DERIVED_DATA"] != env1.Env["SIMURGH_DERIVED_DATA"] {
		t.Fatalf("DerivedData path changed: %q vs %q",
			second.Env.Env["SIMURGH_DERIVED_DATA"], env1.Env["SIMURGH_DERIVED_DATA"])
	}
	if second.Env.Env["SIMURGH_UDID"] != first.Device.UDID {
		t.Fatalf("SIMURGH_UDID=%q want %s", second.Env.Env["SIMURGH_UDID"], first.Device.UDID)
	}

	if _, err := b.Release(context.Background(), first.ID); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

type liveLeaseResult struct {
	Lease  broker.Lease `json:"lease"`
	Reused bool         `json:"reused"`
}

func decodeLiveLease(t *testing.T, raw []byte) liveLeaseResult {
	t.Helper()
	var wrap liveLeaseResult
	if err := json.Unmarshal(raw, &wrap); err != nil {
		t.Fatalf("unmarshal lease: %v raw=%s", err, raw)
	}
	return wrap
}

func cleanupLiveClone(t *testing.T, udid string) {
	t.Helper()
	_ = exec.Command("xcrun", "simctl", "shutdown", udid).Run()
	if out, err := exec.Command("xcrun", "simctl", "delete", udid).CombinedOutput(); err != nil {
		t.Errorf("cleanup delete %s: %v\n%s", udid, err, out)
	}
}
