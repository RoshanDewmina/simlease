package broker

import (
	"context"
	"errors"
	"testing"
	"time"
)

func specA() DeviceSpec {
	return DeviceSpec{Platform: "iOS", Model: "iPhone 17 Pro", Runtime: "27.0"}
}

func specB() DeviceSpec {
	return DeviceSpec{Platform: "iOS", Model: "iPhone 16", Runtime: "27.0"}
}

func TestGlobalCapacityAcrossDifferentSpecs(t *testing.T) {
	p := &fakeProvisioner{}
	b := newTestBroker(t, 1, p)

	first, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: specA(), TTL: time.Minute, Owner: Owner{PID: 1},
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if first.State != LeaseActive {
		t.Fatalf("first state=%s", first.State)
	}

	second, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: specB(), TTL: time.Minute, Wait: false, Owner: Owner{PID: 2},
	})
	var cq *CapacityQueuedError
	if !errors.As(err, &cq) {
		t.Fatalf("second err=%v want CapacityQueuedError (global cap must not multiply across specs)", err)
	}
	if second.State != LeaseQueued {
		t.Fatalf("second state=%s want queued", second.State)
	}
	if cq.Reason != QueueReasonSimulatorSlot {
		t.Fatalf("reason=%q want %q", cq.Reason, QueueReasonSimulatorSlot)
	}
	if cq.ResourceClass != QueueReasonSimulatorSlot {
		t.Fatalf("resourceClass=%q want %q", cq.ResourceClass, QueueReasonSimulatorSlot)
	}
	if p.callCount() != 1 {
		t.Fatalf("provisioner calls=%d want 1", p.callCount())
	}
}

func TestPerSpecCapEnforced(t *testing.T) {
	p := &fakeProvisioner{}
	b := New(BrokerOptions{
		Capacity:        4,
		PerSpecCapacity: 1,
		Provisioner:     p,
		Clock:           func() time.Time { return time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC) },
		PressureOK:      func() (bool, string) { return true, "" },
	})

	first, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: specA(), TTL: time.Minute, Owner: Owner{PID: 1},
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if first.State != LeaseActive {
		t.Fatalf("first state=%s", first.State)
	}

	// Different spec still fits under global capacity.
	other, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: specB(), TTL: time.Minute, Owner: Owner{PID: 2},
	})
	if err != nil {
		t.Fatalf("other spec: %v", err)
	}
	if other.State != LeaseActive {
		t.Fatalf("other state=%s", other.State)
	}

	// Same spec hits per-spec cap despite global headroom.
	queued, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: specA(), TTL: time.Minute, Wait: false, Owner: Owner{PID: 3},
	})
	var cq *CapacityQueuedError
	if !errors.As(err, &cq) {
		t.Fatalf("same-spec err=%v want CapacityQueuedError", err)
	}
	if queued.State != LeaseQueued {
		t.Fatalf("state=%s want queued", queued.State)
	}
	if cq.Reason != QueueReasonPerSpecSlot {
		t.Fatalf("reason=%q want %q", cq.Reason, QueueReasonPerSpecSlot)
	}
	if cq.ResourceClass != QueueReasonPerSpecSlot {
		t.Fatalf("resourceClass=%q want %q", cq.ResourceClass, QueueReasonPerSpecSlot)
	}
}

func TestProvisioningOpLimit(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	p := &fakeProvisioner{
		onCall: func(ProvisionRequest) {
			started <- struct{}{}
			<-release
		},
	}
	b := New(BrokerOptions{
		Capacity:                  4,
		MaxProvisioningOperations: 1,
		Provisioner:               p,
		Clock:                     func() time.Time { return time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC) },
		PressureOK:                func() (bool, string) { return true, "" },
	})

	errCh := make(chan error, 1)
	go func() {
		_, err := b.Acquire(context.Background(), AcquireRequest{
			Spec: specA(), TTL: time.Minute, Wait: true, Owner: Owner{PID: 1},
		})
		errCh <- err
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first provision did not start")
	}

	queued, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: specB(), TTL: time.Minute, Wait: false, Owner: Owner{PID: 2},
	})
	var cq *CapacityQueuedError
	if !errors.As(err, &cq) {
		t.Fatalf("second err=%v want CapacityQueuedError", err)
	}
	if queued.State != LeaseQueued {
		t.Fatalf("state=%s want queued", queued.State)
	}
	if cq.Reason != QueueReasonProvisioningSlot {
		t.Fatalf("reason=%q want %q", cq.Reason, QueueReasonProvisioningSlot)
	}
	if cq.ResourceClass != QueueReasonProvisioningSlot {
		t.Fatalf("resourceClass=%q want %q", cq.ResourceClass, QueueReasonProvisioningSlot)
	}

	close(release)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("first acquire: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first acquire did not finish")
	}
}

func TestQueueReasonExplicit(t *testing.T) {
	p := &fakeProvisioner{}
	b := newTestBroker(t, 1, p)
	_, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: specA(), TTL: time.Minute, Owner: Owner{PID: 1},
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err = b.Acquire(context.Background(), AcquireRequest{
		Spec: specA(), TTL: time.Minute, Wait: false, Owner: Owner{PID: 2},
	})
	var cq *CapacityQueuedError
	if !errors.As(err, &cq) {
		t.Fatalf("err=%v", err)
	}
	if cq.ErrorEnvelope == nil {
		t.Fatal("missing error envelope")
	}
	if cq.ErrorEnvelope.Reason != QueueReasonSimulatorSlot {
		t.Fatalf("envelope reason=%q", cq.ErrorEnvelope.Reason)
	}
	if cq.ErrorEnvelope.ResourceClass != QueueReasonSimulatorSlot {
		t.Fatalf("envelope resourceClass=%q", cq.ErrorEnvelope.ResourceClass)
	}
	if cq.Reason == "" || cq.ResourceClass == "" {
		t.Fatalf("typed error missing reason/class: %+v", cq)
	}
}

func TestFairnessCrossSpecReleaseAdvancesWaiter(t *testing.T) {
	p := &fakeProvisioner{}
	b := newTestBroker(t, 1, p)

	holder, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: specA(), TTL: time.Minute, Owner: Owner{PID: 1},
	})
	if err != nil {
		t.Fatalf("holder: %v", err)
	}

	queued, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: specB(), TTL: time.Minute, Wait: false, Owner: Owner{PID: 2},
	})
	if !errors.As(err, new(*CapacityQueuedError)) {
		t.Fatalf("want queued, got %v", err)
	}
	if queued.State != LeaseQueued {
		t.Fatalf("state=%s", queued.State)
	}

	if _, err := b.Release(context.Background(), holder.ID); err != nil {
		t.Fatalf("release: %v", err)
	}

	got, ok := b.Get(queued.ID)
	if !ok {
		t.Fatal("queued lease missing")
	}
	if got.State != LeaseActive {
		t.Fatalf("after cross-spec release: state=%s want active", got.State)
	}
	if got.Device == nil || got.Device.UDID == "" {
		t.Fatal("promoted lease missing device")
	}
	if p.callCount() != 2 {
		t.Fatalf("provisioner calls=%d want 2", p.callCount())
	}
}

func TestFairnessDeterministicOldestWins(t *testing.T) {
	p := &fakeProvisioner{}
	clock := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	b := New(BrokerOptions{
		Capacity:    1,
		Provisioner: p,
		Clock:       func() time.Time { return clock },
		PressureOK:  func() (bool, string) { return true, "" },
	})

	holder, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: specA(), TTL: time.Minute, Owner: Owner{PID: 1},
	})
	if err != nil {
		t.Fatalf("holder: %v", err)
	}

	clock = clock.Add(time.Second)
	firstWaiter, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: specB(), TTL: time.Minute, Wait: false, Owner: Owner{PID: 2},
	})
	if !errors.As(err, new(*CapacityQueuedError)) {
		t.Fatalf("first waiter: %v", err)
	}

	clock = clock.Add(time.Second)
	secondWaiter, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: specA(), TTL: time.Minute, Wait: false, Owner: Owner{PID: 3},
	})
	if !errors.As(err, new(*CapacityQueuedError)) {
		t.Fatalf("second waiter: %v", err)
	}

	if _, err := b.Release(context.Background(), holder.ID); err != nil {
		t.Fatalf("release: %v", err)
	}

	gotFirst, _ := b.Get(firstWaiter.ID)
	gotSecond, _ := b.Get(secondWaiter.ID)
	if gotFirst.State != LeaseActive {
		t.Fatalf("oldest waiter state=%s want active", gotFirst.State)
	}
	if gotSecond.State != LeaseQueued {
		t.Fatalf("newer waiter state=%s want queued", gotSecond.State)
	}
}

func TestPressureReason(t *testing.T) {
	p := &fakeProvisioner{}
	b := New(BrokerOptions{
		Capacity:    2,
		Provisioner: p,
		PressureOK:  func() (bool, string) { return false, "ram below floor" },
	})
	_, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: specA(), TTL: time.Minute, Wait: false, Owner: Owner{PID: 1},
	})
	var cq *CapacityQueuedError
	if !errors.As(err, &cq) {
		t.Fatalf("err=%v want CapacityQueuedError", err)
	}
	if cq.Reason != QueueReasonPressure {
		t.Fatalf("reason=%q want %q", cq.Reason, QueueReasonPressure)
	}
	if cq.ResourceClass != QueueReasonPressure {
		t.Fatalf("resourceClass=%q want %q", cq.ResourceClass, QueueReasonPressure)
	}
	if cq.ErrorEnvelope == nil || cq.ErrorEnvelope.Reason != QueueReasonPressure {
		t.Fatalf("envelope=%+v", cq.ErrorEnvelope)
	}
	if p.callCount() != 0 {
		t.Fatal("provisioner must not run under pressure")
	}
}
