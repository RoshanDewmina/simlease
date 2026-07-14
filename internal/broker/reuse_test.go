package broker

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/store"
)

func reuseOwner(pid int) Owner {
	return Owner{PID: pid, Agent: "agent-a", SessionID: "session-1"}
}

func reuseLabels() map[string]string {
	return map[string]string{"project": "lancer", "task": "inbox"}
}

func TestReuseIfExists_CompatibleSameLeaseUDIDPaths(t *testing.T) {
	p := &fakeProvisioner{}
	b := newTestBroker(t, 2, p)
	spec := testSpec()
	labels := reuseLabels()

	first, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: spec, Labels: labels, TTL: time.Minute,
		Owner: reuseOwner(1),
	})
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	if first.Device == nil {
		t.Fatal("first lease missing device")
	}

	second, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: spec, Labels: labels, TTL: 2 * time.Minute,
		ReuseIfExists: true,
		Owner:         reuseOwner(99), // new PID; same Agent/SessionID
	})
	if err != nil {
		t.Fatalf("reuse Acquire: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("lease ID=%s want %s", second.ID, first.ID)
	}
	if !second.Reused {
		t.Fatal("compatible ACTIVE renewal must report Reused=true")
	}
	if second.Device == nil || second.Device.UDID != first.Device.UDID {
		t.Fatalf("UDID=%v want %s", second.Device, first.Device.UDID)
	}
	if p.callCount() != 1 {
		t.Fatalf("provision calls=%d want 1 (no reprovision)", p.callCount())
	}
	if second.Owner.PID != 99 {
		t.Fatalf("owner PID=%d want 99 after renew", second.Owner.PID)
	}
	if second.TTL != 2*time.Minute {
		t.Fatalf("TTL=%v want 2m after renew", second.TTL)
	}
}

func TestReuseIfExists_IncompatibleAllocatesNewResource(t *testing.T) {
	p := &fakeProvisioner{}
	b := newTestBroker(t, 4, p)
	spec := testSpec()

	first, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: spec, Labels: reuseLabels(), TTL: time.Minute,
		Owner: reuseOwner(1),
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	cases := []struct {
		name string
		req  AcquireRequest
	}{
		{
			name: "different labels",
			req: AcquireRequest{
				Spec: spec, Labels: map[string]string{"project": "other"},
				TTL: time.Minute, ReuseIfExists: true, Owner: reuseOwner(2),
			},
		},
		{
			name: "different agent",
			req: AcquireRequest{
				Spec: spec, Labels: reuseLabels(), TTL: time.Minute,
				ReuseIfExists: true,
				Owner:         Owner{PID: 3, Agent: "agent-b", SessionID: "session-1"},
			},
		},
		{
			name: "different session",
			req: AcquireRequest{
				Spec: spec, Labels: reuseLabels(), TTL: time.Minute,
				ReuseIfExists: true,
				Owner:         Owner{PID: 4, Agent: "agent-a", SessionID: "session-2"},
			},
		},
		{
			name: "different model",
			req: AcquireRequest{
				Spec:   DeviceSpec{Platform: "iOS", Model: "iPhone 16", Runtime: "27.0"},
				Labels: reuseLabels(), TTL: time.Minute,
				ReuseIfExists: true, Owner: reuseOwner(5),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := b.Acquire(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("Acquire: %v", err)
			}
			if got.ID == first.ID {
				t.Fatalf("reused lease %s; want new resource", got.ID)
			}
			if got.Device == nil || got.Device.UDID == first.Device.UDID {
				t.Fatalf("want distinct UDID, got %+v", got.Device)
			}
		})
	}
}

func TestReuseIfExists_MissingIdentityNeverReuses(t *testing.T) {
	cases := []struct {
		name  string
		owner Owner
	}{
		{name: "both missing", owner: Owner{PID: 1}},
		{name: "agent missing", owner: Owner{PID: 1, SessionID: "session-1"}},
		{name: "session missing", owner: Owner{PID: 1, Agent: "agent-a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &fakeProvisioner{}
			b := newTestBroker(t, 2, p)
			req := AcquireRequest{
				Spec: testSpec(), Labels: reuseLabels(), TTL: time.Minute,
				ReuseIfExists: true, Owner: tc.owner,
			}
			first, err := b.Acquire(context.Background(), req)
			if err != nil {
				t.Fatalf("first Acquire: %v", err)
			}
			req.Owner.PID++
			second, err := b.Acquire(context.Background(), req)
			if err != nil {
				t.Fatalf("second Acquire: %v", err)
			}
			if second.ID == first.ID || second.Reused {
				t.Fatalf("missing identity reused: first=%s second=%s reused=%v",
					first.ID, second.ID, second.Reused)
			}
			if p.callCount() != 2 {
				t.Fatalf("provision calls=%d want 2", p.callCount())
			}
		})
	}
}

func TestReuseIfExists_SameExplicitSessionReuses(t *testing.T) {
	p := &fakeProvisioner{}
	b := newTestBroker(t, 2, p)
	req := AcquireRequest{
		Spec: testSpec(), Labels: reuseLabels(), TTL: time.Minute,
		ReuseIfExists: true,
		Owner:         Owner{PID: 10, Agent: "agent-a", SessionID: "explicit-session"},
	}
	first, err := b.Acquire(context.Background(), req)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	req.Owner.PID = 11
	second, err := b.Acquire(context.Background(), req)
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if second.ID != first.ID || !second.Reused {
		t.Fatalf("same explicit session did not reuse: first=%s second=%s reused=%v",
			first.ID, second.ID, second.Reused)
	}
}

func TestReuseIfExists_ConcurrentDuplicateOneResource(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	p := &fakeProvisioner{
		delay: 80 * time.Millisecond,
		onCall: func(ProvisionRequest) {
			once.Do(func() { close(started) })
		},
	}
	b := newTestBroker(t, 4, p)
	spec := testSpec()
	labels := reuseLabels()

	var wg sync.WaitGroup
	results := make([]Lease, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			lease, err := b.Acquire(context.Background(), AcquireRequest{
				Spec: spec, Labels: labels, TTL: time.Minute,
				ReuseIfExists: true,
				Owner:         reuseOwner(1000 + i),
			})
			results[i] = lease
			errs[i] = err
		}(i)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("provisioner never started")
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
	}
	if results[0].ID != results[1].ID {
		t.Fatalf("concurrent reuse got distinct IDs %s and %s", results[0].ID, results[1].ID)
	}
	if results[0].Reused == results[1].Reused {
		t.Fatalf("want one new and one reused response, got reused=%v/%v",
			results[0].Reused, results[1].Reused)
	}
	if results[0].Device == nil || results[1].Device == nil {
		t.Fatal("missing device on concurrent reuse")
	}
	if results[0].Device.UDID != results[1].Device.UDID {
		t.Fatalf("UDIDs differ: %s vs %s", results[0].Device.UDID, results[1].Device.UDID)
	}
	if p.callCount() != 1 {
		t.Fatalf("provision calls=%d want 1", p.callCount())
	}
}

func TestReuseIfExists_TerminalStatesNotReused(t *testing.T) {
	spec := testSpec()
	labels := reuseLabels()

	t.Run("released", func(t *testing.T) {
		p := &fakeProvisioner{}
		b := newTestBroker(t, 2, p)
		first, err := b.Acquire(context.Background(), AcquireRequest{
			Spec: spec, Labels: labels, TTL: time.Minute, Owner: reuseOwner(1),
		})
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		if _, err := b.Release(context.Background(), first.ID); err != nil {
			t.Fatalf("Release: %v", err)
		}
		second, err := b.Acquire(context.Background(), AcquireRequest{
			Spec: spec, Labels: labels, TTL: time.Minute,
			ReuseIfExists: true, Owner: reuseOwner(2),
		})
		if err != nil {
			t.Fatalf("reuse after release: %v", err)
		}
		if second.ID == first.ID {
			t.Fatal("RELEASED lease must not be reused")
		}
		if p.callCount() != 2 {
			t.Fatalf("provision calls=%d want 2", p.callCount())
		}
	})

	t.Run("failed", func(t *testing.T) {
		p := &fakeProvisioner{errAfter: 1}
		b := newTestBroker(t, 2, p)
		first, err := b.Acquire(context.Background(), AcquireRequest{
			Spec: spec, Labels: labels, TTL: time.Minute, Owner: reuseOwner(1),
		})
		var pf *ProvisionFailedError
		if !errors.As(err, &pf) {
			t.Fatalf("want ProvisionFailedError, got %v lease=%+v", err, first)
		}
		p.errAfter = 0
		second, err := b.Acquire(context.Background(), AcquireRequest{
			Spec: spec, Labels: labels, TTL: time.Minute,
			ReuseIfExists: true, Owner: reuseOwner(2),
		})
		if err != nil {
			t.Fatalf("reuse after failed: %v", err)
		}
		if second.ID == first.ID {
			t.Fatal("FAILED lease must not be reused")
		}
	})

	t.Run("reclaimed", func(t *testing.T) {
		p := &fakeProvisioner{}
		b := newTestBroker(t, 2, p)
		first, err := b.Acquire(context.Background(), AcquireRequest{
			Spec: spec, Labels: labels, TTL: time.Minute, Owner: reuseOwner(1),
		})
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		if _, err := b.Reclaim(context.Background(), first.ID); err != nil {
			t.Fatalf("Reclaim: %v", err)
		}
		second, err := b.Acquire(context.Background(), AcquireRequest{
			Spec: spec, Labels: labels, TTL: time.Minute,
			ReuseIfExists: true, Owner: reuseOwner(2),
		})
		if err != nil {
			t.Fatalf("reuse after reclaim: %v", err)
		}
		if second.ID == first.ID {
			t.Fatal("RECLAIMED lease must not be reused")
		}
	})
}

func TestReuseIfExists_PersistRehydrateMatching(t *testing.T) {
	journal := filepath.Join(t.TempDir(), "state.json")
	st := store.New(journal)
	p := &fakeProvisioner{}
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	spec := testSpec()
	labels := reuseLabels()

	b1 := New(BrokerOptions{
		Capacity: 2, Provisioner: p, Clock: func() time.Time { return now }, Store: st,
	})
	first, err := b1.Acquire(context.Background(), AcquireRequest{
		Spec: spec, Labels: labels, TTL: 30 * time.Minute, Owner: reuseOwner(1),
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	b2 := New(BrokerOptions{
		Capacity: 2, Provisioner: p, Clock: func() time.Time { return now }, Store: st,
	})
	if err := b2.Rehydrate(); err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	second, err := b2.Acquire(context.Background(), AcquireRequest{
		Spec: spec, Labels: labels, TTL: time.Minute,
		ReuseIfExists: true, Owner: reuseOwner(50),
	})
	if err != nil {
		t.Fatalf("reuse after rehydrate: %v", err)
	}
	if second.ID != first.ID || second.Device == nil || second.Device.UDID != first.Device.UDID {
		t.Fatalf("want same lease/UDID after rehydrate; got %+v want id=%s udid=%s",
			second, first.ID, first.Device.UDID)
	}
	if p.callCount() != 1 {
		t.Fatalf("provision calls=%d want 1", p.callCount())
	}
}

func TestWaitCancel_QueuedLeaseNotStranded(t *testing.T) {
	p := &fakeProvisioner{}
	b := newTestBroker(t, 1, p)
	spec := testSpec()

	first, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: spec, TTL: time.Minute, Owner: Owner{PID: 1},
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	_ = first

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	var queuedID string
	go func() {
		lease, err := b.Acquire(ctx, AcquireRequest{
			Spec: spec, TTL: time.Minute, Wait: true, Owner: Owner{PID: 2},
		})
		queuedID = lease.ID
		errCh <- err
	}()

	// Let the waiter register and leave the lease QUEUED.
	deadline := time.Now().Add(2 * time.Second)
	for {
		listed := b.List(ListFilter{})
		queued := 0
		for _, l := range listed {
			if l.State == LeaseQueued {
				queued++
				queuedID = l.ID
			}
		}
		if queued == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("queued lease never appeared")
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-errCh:
		var ac *AcquireCancelledError
		if !errors.As(err, &ac) {
			t.Fatalf("want AcquireCancelledError, got %v", err)
		}
		if ac.ErrorEnvelope == nil || ac.ErrorEnvelope.Code != CodeAcquireCancelled {
			t.Fatalf("envelope=%+v", ac.ErrorEnvelope)
		}
		if ac.ErrorEnvelope.Fix == "" || ac.ErrorEnvelope.Message == "" {
			t.Fatalf("actionable error missing message/fix: %+v", ac.ErrorEnvelope)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Acquire did not return after cancel")
	}

	got, ok := b.Get(queuedID)
	if !ok {
		t.Fatalf("lease %s missing", queuedID)
	}
	if got.State != LeaseCancelled {
		t.Fatalf("state=%s want cancelled (not stranded QUEUED)", got.State)
	}
	if b.waiterCount(queuedID) != 0 {
		t.Fatalf("waiters leaked: %d", b.waiterCount(queuedID))
	}
}

func TestReuseMatchKey_DocumentedDimensions(t *testing.T) {
	// Conservative key uses only Spec + exact Labels + Owner.Agent/SessionID.
	// PID is intentionally excluded; project/Xcode/scheme are Labels if needed.
	req := AcquireRequest{
		Spec:   testSpec(),
		Labels: map[string]string{"project": "a", "xcode": "27.0"},
		Owner:  Owner{PID: 1, Agent: "claude", SessionID: "s1"},
	}
	active := &Lease{
		ID:     "lease-1",
		State:  LeaseActive,
		Spec:   testSpec(),
		Labels: map[string]string{"project": "a", "xcode": "27.0"},
		Owner:  Owner{PID: 999, Agent: "claude", SessionID: "s1"},
	}
	if !reuseCompatible(req, active) {
		t.Fatal("expected compatible despite different PID")
	}
	active.Labels = map[string]string{"project": "a"} // missing xcode
	if reuseCompatible(req, active) {
		t.Fatal("exact Labels mismatch must not match")
	}
}
