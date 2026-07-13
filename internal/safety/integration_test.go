//go:build darwin_sim

package safety

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	baseDeviceName = "simurgh-base[iPhone 17 Pro:27.0]"
	cloneNameTag   = "test-safety"
)

// TestSafetyLiveDeletionGuards exercises real simctl + real filesystem paths.
//
// Hard rule: every destructive operation targets ONLY a throwaway artifact
// this test itself creates in the same run. The pre-existing
// simurgh-base[...] device must be refused and left untouched.
func TestSafetyLiveDeletionGuards(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	lister := &liveLister{}
	deleter := &liveDeleter{}

	baseUDID := ensureBaseDevice(t, ctx)
	t.Logf("base device udid=%s name=%s (must remain untouched)", baseUDID, baseDeviceName)

	before := listSimurghLines(t)
	t.Logf("simurgh devices BEFORE:\n%s", strings.Join(before, "\n"))

	// --- (1) delete the test's own throwaway clone ---
	cloneName := fmt.Sprintf("simurgh-clone[%s:%d]", cloneNameTag, time.Now().UnixNano())
	cloneUDID := createThrowawayClone(t, ctx, cloneName)
	t.Logf("created throwaway clone udid=%s name=%s", cloneUDID, cloneName)

	// If DeleteSimulatorClone fails mid-test, still try raw cleanup so we
	// never leave a managed clone behind. Only this exact UDID.
	cleaned := false
	defer func() {
		if cleaned {
			return
		}
		_ = exec.Command("xcrun", "simctl", "shutdown", cloneUDID).Run()
		_ = exec.Command("xcrun", "simctl", "delete", cloneUDID).Run()
	}()

	if err := DeleteSimulatorClone(ctx, lister, deleter, cloneUDID, map[string]bool{cloneUDID: true}); err != nil {
		t.Fatalf("DeleteSimulatorClone throwaway: %v", err)
	}
	cleaned = true

	if deviceExists(t, ctx, cloneUDID) {
		t.Fatalf("throwaway clone %s still present after DeleteSimulatorClone", cloneUDID)
	}
	t.Log("case 1 OK: throwaway clone deleted")

	// --- (2) refuse deletion of real simurgh-base[...] ---
	err := DeleteSimulatorClone(ctx, lister, deleter, baseUDID, map[string]bool{baseUDID: true})
	if err == nil {
		t.Fatal("expected DeviceNotManagedError for simurgh-base[...]")
	}
	var notManaged *DeviceNotManagedError
	if !errors.As(err, &notManaged) {
		t.Fatalf("want DeviceNotManagedError, got %T: %v", err, err)
	}
	if !deviceExists(t, ctx, baseUDID) {
		t.Fatalf("BASE DEVICE WAS DELETED — safety invariant broken; udid=%s", baseUDID)
	}
	// Confirm live name still the base name.
	dev, _, verr := ValidateDeviceDeletion(ctx, lister, baseUDID, nil)
	if !errors.As(verr, &notManaged) {
		// Validate should also refuse; if somehow name changed, fail loudly.
		t.Fatalf("base validation after refused delete: device=%+v err=%v", dev, verr)
	}
	t.Log("case 2 OK: base refused and still present")

	// --- (3) nonexistent UDID → typed not-found, no crash ---
	fakeUDID := "00000000-0000-0000-0000-000000000000"
	err = DeleteSimulatorClone(ctx, lister, deleter, fakeUDID, nil)
	if err == nil {
		t.Fatal("expected DeviceNotFoundError for nonexistent UDID")
	}
	var notFound *DeviceNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("want DeviceNotFoundError, got %T: %v", err, err)
	}
	t.Log("case 3 OK: nonexistent UDID refused")

	// --- paths: delete fake lease under t.TempDir(); refuse symlink escape ---
	root := t.TempDir()
	leaseDir := filepath.Join(root, "lease-1")
	if err := os.Mkdir(leaseDir, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(leaseDir, "file.txt")
	if err := os.WriteFile(marker, []byte("lease-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "untouchable.txt")
	if err := os.WriteFile(sentinel, []byte("keep-me"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := DeleteLeaseDir(root, "lease-1"); err != nil {
		t.Fatalf("DeleteLeaseDir: %v", err)
	}
	if _, err := os.Stat(leaseDir); !os.IsNotExist(err) {
		t.Fatalf("lease dir still present: %v", err)
	}

	// Symlink escape: lease-2 -> outside TempDir; must refuse, sentinel untouched.
	link := filepath.Join(root, "lease-2")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	escapeErr := DeleteLeaseDir(root, "lease-2")
	if escapeErr == nil {
		t.Fatal("DeleteLeaseDir must refuse symlink escape")
	}
	var outsideErr *PathOutsideLeasesRootError
	if !errors.As(escapeErr, &outsideErr) {
		t.Fatalf("want PathOutsideLeasesRootError, got %T: %v", escapeErr, escapeErr)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("sentinel outside leasesRoot was touched: %v", err)
	}
	// Confirm nothing under outside was removed.
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "untouchable.txt" {
		t.Fatalf("outside TempDir contents altered: %v", entries)
	}
	t.Log("path cases OK: lease deleted; symlink escape refused")

	after := listSimurghLines(t)
	t.Logf("simurgh devices AFTER:\n%s", strings.Join(after, "\n"))

	// Final invariant: base still present with same UDID; no throwaway clone left.
	if !deviceExists(t, ctx, baseUDID) {
		t.Fatalf("base UDID %s missing at end of test", baseUDID)
	}
	for _, line := range after {
		if strings.Contains(line, "simurgh-clone[test-safety:") {
			t.Fatalf("throwaway clone still listed: %s", line)
		}
	}
}

// ensureBaseDevice returns the UDID of simurgh-base[iPhone 17 Pro:27.0],
// creating and leaving one if none exists.
func ensureBaseDevice(t *testing.T, ctx context.Context) string {
	t.Helper()
	if udid, ok := findDeviceByName(t, ctx, baseDeviceName); ok {
		return udid
	}
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
	if !IsSimurghManagedName(finalName) {
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

// liveLister / liveDeleter are test-local simctl adapters. They intentionally
// do not import internal/provisioner — this package must stay free of that
// dependency, and the integration test stays self-contained.
type liveLister struct{}

func (l *liveLister) ListDevices(ctx context.Context) ([]Device, error) {
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
	var devices []Device
	for _, group := range raw.Devices {
		for _, d := range group {
			devices = append(devices, Device{UDID: d.UDID, Name: d.Name})
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
