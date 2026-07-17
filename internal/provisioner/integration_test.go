//go:build darwin_sim

package provisioner

import (
	"context"
	"fmt"
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
//
// Negative: a foreign simurgh-clone[...] seeded by this test must remain after
// owned-UDID cleanup and must not cause cleanup to fail.
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

	// Foreign fixture owned by this test: proves cleanup ignores other clones.
	foreignName := fmt.Sprintf("simurgh-clone[foreign-cleanup-flake:%d]", time.Now().UnixNano())
	foreignUDID := seedForeignClone(t, ctx, client, foreignName)
	defer cleanupOwnedUDID(t, foreignUDID)

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
	ownedCleaned := false
	defer func() {
		if !ownedCleaned {
			cleanupClone(t, dev.UDID)
		}
	}()

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
	if dev.UDID == foreignUDID {
		t.Fatalf("provisioned UDID collided with foreign fixture: %s", dev.UDID)
	}

	afterCreate := listSimurghNames(t)
	t.Logf("simurgh devices after create: %v", afterCreate)
	if !simurghLineHasUDID(afterCreate, dev.UDID) {
		t.Fatalf("owned UDID %q not listed after create; before=%v after=%v", dev.UDID, before, afterCreate)
	}
	if !deviceExistsByUDID(t, ctx, client, foreignUDID) {
		t.Fatalf("foreign fixture UDID %s disappeared before owned cleanup", foreignUDID)
	}

	cleanupClone(t, dev.UDID)
	ownedCleaned = true

	if deviceExistsByUDID(t, ctx, client, dev.UDID) {
		t.Fatalf("owned UDID %s still present after cleanup", dev.UDID)
	}
	if !deviceExistsByUDID(t, ctx, client, foreignUDID) {
		t.Fatalf("foreign Simurgh clone UDID %s was deleted or lost during owned cleanup", foreignUDID)
	}
	t.Logf("foreign clone still present after owned cleanup: udid=%s name=%s", foreignUDID, foreignName)
}

func cleanupClone(t *testing.T, udid string) {
	t.Helper()
	if udid == "" {
		return
	}
	shutdown := exec.Command("xcrun", "simctl", "shutdown", udid)
	_ = shutdown.Run() // may already be shutting down; ignore
	del := exec.Command("xcrun", "simctl", "delete", udid)
	if out, err := del.CombinedOutput(); err != nil {
		t.Errorf("cleanup delete %s: %v\n%s", udid, err, out)
		return
	}
	afterDelete := listSimurghNames(t)
	t.Logf("simurgh devices after cleanup: %v", afterDelete)
	if simurghLineHasUDID(afterDelete, udid) {
		t.Errorf("owned clone UDID %s still present after cleanup", udid)
	}
}

func cleanupOwnedUDID(t *testing.T, udid string) {
	t.Helper()
	if udid == "" {
		return
	}
	_ = exec.Command("xcrun", "simctl", "shutdown", udid).Run()
	if out, err := exec.Command("xcrun", "simctl", "delete", udid).CombinedOutput(); err != nil {
		if simurghLineHasUDID(listSimurghNames(t), udid) {
			t.Errorf("foreign fixture cleanup delete %s: %v\n%s", udid, err, out)
		}
	}
}

func seedForeignClone(t *testing.T, ctx context.Context, client SimctlClient, name string) string {
	t.Helper()
	baseUDID := findBaseUDID(t, ctx, client)
	out, err := exec.CommandContext(ctx, "xcrun", "simctl", "clone", baseUDID, name).CombinedOutput()
	if err != nil {
		t.Fatalf("seed foreign clone: %v\n%s", err, out)
	}
	udid := strings.TrimSpace(string(out))
	if udid == "" {
		t.Fatal("seed foreign clone: empty udid")
	}
	t.Logf("seeded foreign clone udid=%s name=%s", udid, name)
	return udid
}

func findBaseUDID(t *testing.T, ctx context.Context, client SimctlClient) string {
	t.Helper()
	devices, err := client.ListDevices(ctx)
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	for _, d := range devices {
		if strings.HasPrefix(d.Name, "simurgh-base[") {
			return d.UDID
		}
	}
	t.Fatal("no simurgh-base[...] device to clone for foreign fixture")
	return ""
}

func deviceExistsByUDID(t *testing.T, ctx context.Context, client SimctlClient, udid string) bool {
	t.Helper()
	devices, err := client.ListDevices(ctx)
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	for _, d := range devices {
		if d.UDID == udid {
			return true
		}
	}
	return false
}

func simurghLineHasUDID(lines []string, udid string) bool {
	for _, line := range lines {
		if strings.Contains(line, udid) {
			return true
		}
	}
	return false
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
