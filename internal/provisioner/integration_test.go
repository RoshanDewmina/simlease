//go:build darwin_sim

package provisioner

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
)

// TestProvisionRealClone exercises the real simctl path on this Mac.
//
// Cleanup: this test shuts down and deletes ONLY the exact UDID it created in
// this run (raw exec of simctl delete). That is test-only self-cleanup of an
// artifact the test itself created — not product code, and not routed through
// the not-yet-built internal/safety package. Product provisioner code never
// deletes or erases devices. The simurgh-base[...] device is intentionally
// left in place (idempotent base reuse; not matched by the clone/warm deletion
// invariant).
func TestProvisionRealClone(t *testing.T) {
	client := NewExecClient()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
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

	before := listSimurghNames(t)

	p := New(client)
	dev, err := p.Provision(ctx, broker.ProvisionRequest{
		LeaseID: "test-m1.2",
		Spec: broker.DeviceSpec{
			Platform: "iOS",
			Model:    "iPhone 17 Pro",
			// Runtime omitted — exercise newest-installed resolution.
		},
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}

	// Self-cleanup of this test's own clone only (exact UDID), even if later
	// assertions fail. Raw exec — test-only, not product code / not safety.
	defer cleanupClone(t, dev.UDID)

	t.Logf("created clone udid=%s name=%s runtime=%s cloneOf=%s booted=%v",
		dev.UDID, dev.Name, dev.Runtime, dev.CloneOf, dev.Booted)

	cloneRe := regexp.MustCompile(`^simurgh-clone\[.*\]$`)
	if !cloneRe.MatchString(dev.Name) {
		t.Fatalf("name %q does not match ^simurgh-clone\\[.*\\]$", dev.Name)
	}
	if !dev.Booted {
		t.Fatal("expected Booted=true")
	}
	if dev.UDID == "" || dev.CloneOf == "" {
		t.Fatalf("missing UDID/CloneOf: %+v", dev)
	}

	afterCreate := listSimurghNames(t)
	t.Logf("simurgh devices after create: %v", afterCreate)
	found := false
	for _, name := range afterCreate {
		if strings.Contains(name, dev.Name) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("clone name %q not listed after create; before=%v after=%v", dev.Name, before, afterCreate)
	}
}

func cleanupClone(t *testing.T, udid string) {
	t.Helper()
	shutdown := exec.Command("xcrun", "simctl", "shutdown", udid)
	_ = shutdown.Run() // may already be shutting down; ignore
	del := exec.Command("xcrun", "simctl", "delete", udid)
	if out, err := del.CombinedOutput(); err != nil {
		t.Errorf("cleanup delete %s: %v\n%s", udid, err, out)
		return
	}
	afterDelete := listSimurghNames(t)
	t.Logf("simurgh devices after cleanup: %v", afterDelete)
	for _, name := range afterDelete {
		if strings.Contains(name, "simurgh-clone[") {
			t.Errorf("clone still present after cleanup: %s", name)
		}
	}
}

func listSimurghNames(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("xcrun", "simctl", "list", "devices").CombinedOutput()
	if err != nil {
		t.Fatalf("simctl list devices: %v\n%s", err, out)
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "simurgh") {
			names = append(names, strings.TrimSpace(line))
		}
	}
	return names
}
