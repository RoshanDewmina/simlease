//go:build darwin_sim

package reaper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
	"github.com/RoshanDewmina/simurgh/internal/safety"
)

const (
	baseDeviceName = "simurgh-base[iPhone 17 Pro:27.0]"
	cloneNameTag   = "test-reaper"
)

// TestReaperLiveReclaimAndOrphanScan exercises real simctl through
// internal/safety. Every destructive action targets ONLY throwaway
// artifacts this test creates. The pre-existing simurgh-base[...] must
// remain untouched.
func TestReaperLiveReclaimAndOrphanScan(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	lister := &liveLister{}
	deleter := &liveDeleter{}

	baseUDID := findOrSkipBase(t, ctx)
	t.Logf("base device udid=%s name=%s (must remain untouched)", baseUDID, baseDeviceName)

	before := listSimurghLines(t)
	t.Logf("simurgh devices BEFORE:\n%s", strings.Join(before, "\n"))

	root := t.TempDir()
	start := time.Now().Add(-30 * time.Second) // clear reconciliation window
	r := New(ReaperOptions{
		LeasesRoot:           root,
		Lister:               lister,
		Deleter:              deleter,
		StartedAt:            func() time.Time { return start },
		Now:                  time.Now,
		ReconciliationWindow: 10 * time.Second,
	})

	// --- (1) Reclaim deletes a throwaway clone via real safety path ---
	reclaimName := fmt.Sprintf("simurgh-clone[%s-reclaim:%d]", cloneNameTag, time.Now().UnixNano())
	reclaimUDID := createThrowawayClone(t, ctx, reclaimName)
	t.Logf("created reclaim clone udid=%s name=%s", reclaimUDID, reclaimName)

	leaseID := "lease-1"
	leaseDir := filepath.Join(root, leaseID)
	if err := os.Mkdir(leaseDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaseDir, "marker.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	reclaimCleaned := false
	defer func() {
		if reclaimCleaned {
			return
		}
		_ = exec.Command("xcrun", "simctl", "shutdown", reclaimUDID).Run()
		_ = exec.Command("xcrun", "simctl", "delete", reclaimUDID).Run()
	}()

	err := r.Reclaim(ctx, broker.Lease{
		ID:     leaseID,
		State:  broker.LeaseReclaimed,
		Device: &broker.Device{UDID: reclaimUDID, Name: reclaimName},
	})
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	reclaimCleaned = true

	if deviceExists(t, ctx, reclaimUDID) {
		t.Fatalf("reclaim clone %s still present after Reaper.Reclaim", reclaimUDID)
	}
	if _, err := os.Stat(leaseDir); !os.IsNotExist(err) {
		t.Fatalf("lease dir still present: %v", err)
	}
	t.Log("case 1 OK: Reclaim deleted throwaway clone + lease dir")

	// --- (2) ScanOrphans finds a true orphan (created via simctl, no lease) ---
	orphanName := fmt.Sprintf("simurgh-clone[%s-orphan:%d]", cloneNameTag, time.Now().UnixNano())
	orphanUDID := createThrowawayClone(t, ctx, orphanName)
	t.Logf("created orphan clone udid=%s name=%s", orphanUDID, orphanName)

	orphanCleaned := false
	defer func() {
		if orphanCleaned {
			return
		}
		_ = exec.Command("xcrun", "simctl", "shutdown", orphanUDID).Run()
		_ = exec.Command("xcrun", "simctl", "delete", orphanUDID).Run()
	}()

	orphans, err := r.ScanOrphans(ctx, map[string]bool{
		// No entry for orphanUDID — simulates bookkeeping loss.
		// Include base so even if it somehow matched (it shouldn't),
		// we still assert managed-name filtering separately below.
	})
	if err != nil {
		t.Fatalf("ScanOrphans: %v", err)
	}

	foundOrphan := false
	for _, udid := range orphans {
		if udid == orphanUDID {
			foundOrphan = true
		}
		if udid == baseUDID {
			t.Fatalf("ScanOrphans listed base device %s — IsSimurghManagedName must exclude bases", baseUDID)
		}
	}
	if !foundOrphan {
		t.Fatalf("ScanOrphans did not find orphan %s; got %v", orphanUDID, orphans)
	}
	t.Log("case 2 OK: ScanOrphans found throwaway orphan; base not listed")

	// Clean the orphan through safety (same path production would use).
	if err := safety.DeleteSimulatorClone(ctx, lister, deleter, orphanUDID, nil); err != nil {
		t.Fatalf("cleanup orphan via safety: %v", err)
	}
	orphanCleaned = true
	if deviceExists(t, ctx, orphanUDID) {
		t.Fatalf("orphan %s still present after cleanup", orphanUDID)
	}

	after := listSimurghLines(t)
	t.Logf("simurgh devices AFTER:\n%s", strings.Join(after, "\n"))

	if !deviceExists(t, ctx, baseUDID) {
		t.Fatalf("BASE DEVICE WAS DELETED — udid=%s", baseUDID)
	}
	for _, line := range after {
		if strings.Contains(line, "simurgh-clone["+cloneNameTag) {
			t.Fatalf("throwaway clone still listed: %s", line)
		}
	}
}

func findOrSkipBase(t *testing.T, ctx context.Context) string {
	t.Helper()
	if udid, ok := findDeviceByName(t, ctx, baseDeviceName); ok {
		return udid
	}
	// Create and leave a base so the "untouched" invariant is meaningful
	// on a clean machine — same pattern as M1.4 safety integration.
	dt, rt := pickIOSDeviceTypeAndRuntime(t, ctx)
	udid, err := simctlCreate(ctx, baseDeviceName, dt, rt)
	if err != nil {
		t.Fatalf("create base device: %v", err)
	}
	t.Logf("created fresh base device udid=%s (intentionally left in place)", udid)
	return udid
}

func createThrowawayClone(t *testing.T, ctx context.Context, finalName string) string {
	t.Helper()
	dt, rt := pickIOSDeviceTypeAndRuntime(t, ctx)
	tempName := fmt.Sprintf("simurgh-provisioning-%s-%d", cloneNameTag, time.Now().UnixNano())
	udid, err := simctlCreate(ctx, tempName, dt, rt)
	if err != nil {
		t.Fatalf("simctl create: %v", err)
	}
	if err := simctlRename(ctx, udid, finalName); err != nil {
		_ = exec.Command("xcrun", "simctl", "delete", udid).Run()
		t.Fatalf("simctl rename: %v", err)
	}
	if !safety.IsSimurghManagedName(finalName) {
		t.Fatalf("test bug: finalName %q not managed", finalName)
	}
	return udid
}

func pickIOSDeviceTypeAndRuntime(t *testing.T, ctx context.Context) (deviceTypeID, runtimeID string) {
	t.Helper()
	out, err := simctl(ctx, "list", "devicetypes", "-j")
	if err != nil {
		t.Fatalf("list devicetypes: %v", err)
	}
	var typesRaw struct {
		DeviceTypes []struct {
			Name       string `json:"name"`
			Identifier string `json:"identifier"`
		} `json:"devicetypes"`
	}
	if err := json.Unmarshal(out, &typesRaw); err != nil {
		t.Fatalf("parse devicetypes: %v", err)
	}
	for _, dt := range typesRaw.DeviceTypes {
		if dt.Name == "iPhone 17 Pro" {
			deviceTypeID = dt.Identifier
			break
		}
	}
	if deviceTypeID == "" {
		t.Fatal("iPhone 17 Pro device type not found")
	}

	out, err = simctl(ctx, "list", "runtimes", "-j")
	if err != nil {
		t.Fatalf("list runtimes: %v", err)
	}
	var rtRaw struct {
		Runtimes []struct {
			Name        string `json:"name"`
			Identifier  string `json:"identifier"`
			IsAvailable bool   `json:"isAvailable"`
		} `json:"runtimes"`
	}
	if err := json.Unmarshal(out, &rtRaw); err != nil {
		t.Fatalf("parse runtimes: %v", err)
	}
	for _, rt := range rtRaw.Runtimes {
		if rt.IsAvailable && strings.HasPrefix(rt.Name, "iOS") {
			runtimeID = rt.Identifier
			break
		}
	}
	if runtimeID == "" {
		t.Skip("no available iOS runtime")
	}
	return deviceTypeID, runtimeID
}

func simctlCreate(ctx context.Context, name, deviceTypeID, runtimeID string) (string, error) {
	out, err := simctl(ctx, "create", name, deviceTypeID, runtimeID)
	if err != nil {
		return "", err
	}
	udid := strings.TrimSpace(string(out))
	if udid == "" {
		return "", fmt.Errorf("simctl create returned empty udid")
	}
	return udid, nil
}

func simctlRename(ctx context.Context, udid, newName string) error {
	_, err := simctl(ctx, "rename", udid, newName)
	return err
}

func simctl(ctx context.Context, args ...string) ([]byte, error) {
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

func findDeviceByName(t *testing.T, ctx context.Context, name string) (string, bool) {
	t.Helper()
	devices, err := (&liveLister{}).ListDevices(ctx)
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	for _, d := range devices {
		if d.Name == name {
			return d.UDID, true
		}
	}
	return "", false
}

func deviceExists(t *testing.T, ctx context.Context, udid string) bool {
	t.Helper()
	devices, err := (&liveLister{}).ListDevices(ctx)
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

func listSimurghLines(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("xcrun", "simctl", "list", "devices").CombinedOutput()
	if err != nil {
		t.Fatalf("simctl list devices: %v\n%s", err, out)
	}
	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(strings.ToLower(line), "simurgh") {
			lines = append(lines, strings.TrimSpace(line))
		}
	}
	return lines
}

type liveLister struct{}

func (l *liveLister) ListDevices(ctx context.Context) ([]safety.Device, error) {
	out, err := simctl(ctx, "list", "devices", "-j")
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
		return nil, fmt.Errorf("parse devices: %w", err)
	}
	var devices []safety.Device
	for _, group := range raw.Devices {
		for _, d := range group {
			devices = append(devices, safety.Device{UDID: d.UDID, Name: d.Name})
		}
	}
	return devices, nil
}

type liveDeleter struct{}

func (d *liveDeleter) Shutdown(ctx context.Context, udid string) error {
	_, err := simctl(ctx, "shutdown", udid)
	return err
}

func (d *liveDeleter) Delete(ctx context.Context, udid string) error {
	_, err := simctl(ctx, "delete", udid)
	return err
}
