package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/store"
)

func TestPersist_NilStoreIsNoOp(t *testing.T) {
	p := &fakeProvisioner{}
	b := newTestBroker(t, 1, p)
	lease, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: testSpec(), TTL: time.Minute, Owner: Owner{PID: 1},
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if lease.State != LeaseActive {
		t.Fatalf("state=%s want active", lease.State)
	}
}

func TestPersistAndRehydrate_ActiveSurvives(t *testing.T) {
	journal := filepath.Join(t.TempDir(), "state.json")
	st := store.New(journal)
	p := &fakeProvisioner{}
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)

	b1 := New(BrokerOptions{
		Capacity:    2,
		Provisioner: p,
		Clock:       func() time.Time { return now },
		Store:       st,
	})
	lease, err := b1.Acquire(context.Background(), AcquireRequest{
		Spec: testSpec(), TTL: 30 * time.Minute, Owner: Owner{PID: 42, Agent: "t"},
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if lease.State != LeaseActive || lease.Device == nil {
		t.Fatalf("want ACTIVE with device, got %+v", lease)
	}

	raw, err := st.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(raw.Leases) == 0 {
		t.Fatal("journal Leases empty after Acquire")
	}

	b2 := New(BrokerOptions{
		Capacity:    2,
		Provisioner: p,
		Clock:       func() time.Time { return now },
		Store:       st,
	})
	if err := b2.Rehydrate(); err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	got, ok := b2.Get(lease.ID)
	if !ok {
		t.Fatalf("lease %s missing after rehydrate", lease.ID)
	}
	if got.State != LeaseActive {
		t.Fatalf("state=%s want active", got.State)
	}
	if got.Device == nil || got.Device.UDID != lease.Device.UDID {
		t.Fatalf("device UDID=%v want %s", got.Device, lease.Device.UDID)
	}
	known := b2.KnownDeviceUDIDs()
	if !known[lease.Device.UDID] {
		t.Fatalf("KnownDeviceUDIDs missing %s: %v", lease.Device.UDID, known)
	}
}

func TestRehydrate_ProvisioningBecomesFailed(t *testing.T) {
	journal := filepath.Join(t.TempDir(), "state.json")
	st := store.New(journal)

	seed := map[string]*Lease{
		"lease-7": {
			ID:         "lease-7",
			State:      LeaseProvisioning,
			Owner:      Owner{PID: 1},
			Spec:       testSpec(),
			Connection: Connection{Transport: "local"},
			TTL:        time.Minute,
			AcquiredAt: time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC),
		},
	}
	raw, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Save(store.State{SchemaVersion: store.SchemaVersion, Leases: raw}); err != nil {
		t.Fatal(err)
	}

	var logs []string
	b := New(BrokerOptions{
		Capacity:    1,
		Provisioner: &fakeProvisioner{},
		Store:       st,
		Logger: func(format string, args ...any) {
			logs = append(logs, fmt.Sprintf(format, args...))
		},
	})
	if err := b.Rehydrate(); err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	got, ok := b.Get("lease-7")
	if !ok {
		t.Fatal("lease-7 missing")
	}
	if got.State != LeaseFailed {
		t.Fatalf("state=%s want failed", got.State)
	}
	if len(b.KnownDeviceUDIDs()) != 0 {
		t.Fatalf("FAILED lease must not contribute known UDIDs: %v", b.KnownDeviceUDIDs())
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "lease-7") || !strings.Contains(strings.ToLower(joined), "provision") {
		t.Fatalf("expected rehydrate log about provisioning, got %q", joined)
	}
}

func TestRehydrate_QueuedReentersQueue(t *testing.T) {
	journal := filepath.Join(t.TempDir(), "state.json")
	st := store.New(journal)

	seed := map[string]*Lease{
		"lease-1": {
			ID: "lease-1", State: LeaseActive, Owner: Owner{PID: 1},
			Spec: testSpec(), Connection: Connection{Transport: "local"},
			TTL: time.Minute, Device: &Device{UDID: "U1", Name: "simurgh-clone[a]", Booted: true},
			AcquiredAt: time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC),
		},
		"lease-2": {
			ID: "lease-2", State: LeaseQueued, Owner: Owner{PID: 2},
			Spec: testSpec(), Connection: Connection{Transport: "local"},
			TTL: time.Minute, AcquiredAt: time.Date(2026, 7, 13, 12, 1, 0, 0, time.UTC),
		},
	}
	raw, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Save(store.State{SchemaVersion: store.SchemaVersion, Leases: raw}); err != nil {
		t.Fatal(err)
	}

	b := New(BrokerOptions{
		Capacity:    1,
		Provisioner: &fakeProvisioner{},
		Store:       st,
	})
	if err := b.Rehydrate(); err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	q, ok := b.Get("lease-2")
	if !ok || q.State != LeaseQueued {
		t.Fatalf("queued lease: %+v ok=%v", q, ok)
	}
	if _, err := b.Release(context.Background(), "lease-1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	got, ok := b.Get("lease-2")
	if !ok {
		t.Fatal("lease-2 missing after release")
	}
	if got.State != LeaseActive {
		t.Fatalf("after Release of active, queued should promote; state=%s", got.State)
	}
}

func TestPersistFailureDoesNotRollBackMemory(t *testing.T) {
	dir := t.TempDir()
	journal := filepath.Join(dir, "state.json")
	st := store.New(journal)
	p := &fakeProvisioner{}
	var logs []string
	b := New(BrokerOptions{
		Capacity:    1,
		Provisioner: p,
		Clock:       func() time.Time { return time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC) },
		Store:       st,
		Logger: func(format string, args ...any) {
			logs = append(logs, fmt.Sprintf(format, args...))
		},
	})
	lease, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: testSpec(), TTL: time.Minute, Owner: Owner{PID: 1},
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	_, err = b.Renew(context.Background(), lease.ID, time.Minute)
	if err != nil {
		t.Fatalf("Renew must succeed in-memory despite persist failure: %v", err)
	}
	got, ok := b.Get(lease.ID)
	if !ok || got.State != LeaseActive {
		t.Fatalf("in-memory lease broken: %+v", got)
	}
	if len(logs) == 0 {
		t.Fatal("expected persist failure log")
	}
}
