package broker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testSpec() DeviceSpec {
	return DeviceSpec{Platform: "iOS", Model: "iPhone 17 Pro", Runtime: "27.0"}
}

type fakeProvisioner struct {
	mu       sync.Mutex
	n        atomic.Int64
	delay    time.Duration
	errAfter int // fail on call number N (1-based); 0 = never
	calls    int
	onCall   func(ProvisionRequest)
}

func (f *fakeProvisioner) Provision(ctx context.Context, req ProvisionRequest) (Device, error) {
	f.mu.Lock()
	f.calls++
	calls := f.calls
	errAfter := f.errAfter
	delay := f.delay
	onCall := f.onCall
	f.mu.Unlock()

	if onCall != nil {
		onCall(req)
	}
	if delay > 0 {
		select {
		case <-ctx.Done():
			return Device{}, ctx.Err()
		case <-time.After(delay):
		}
	}
	if errAfter > 0 && calls == errAfter {
		return Device{}, errors.New("provision boom")
	}
	id := f.n.Add(1)
	return Device{
		UDID:    fmt.Sprintf("UDID-%d", id),
		Name:    fmt.Sprintf("sim-%d", id),
		Runtime: req.Spec.Runtime,
		Booted:  true,
	}, nil
}

func (f *fakeProvisioner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newTestBroker(t *testing.T, capacity int, p Provisioner) *Broker {
	t.Helper()
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	return New(BrokerOptions{
		Capacity:    capacity,
		Provisioner: p,
		Clock:       func() time.Time { return now },
		PressureOK:  func() (bool, string) { return true, "" },
	})
}

func TestConcurrentSameSpecAcquireDistinctUDIDs(t *testing.T) {
	p := &fakeProvisioner{delay: 20 * time.Millisecond}
	b := newTestBroker(t, 2, p)
	spec := testSpec()

	var wg sync.WaitGroup
	results := make([]Lease, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			lease, err := b.Acquire(context.Background(), AcquireRequest{
				Spec:  spec,
				TTL:   30 * time.Minute,
				Owner: Owner{PID: 1000 + i, Agent: "test"},
			})
			results[i] = lease
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		if results[i].State != LeaseActive {
			t.Fatalf("acquire %d: state=%s want active", i, results[i].State)
		}
		if results[i].Device == nil || results[i].Device.UDID == "" {
			t.Fatalf("acquire %d: missing device", i)
		}
	}
	if results[0].Device.UDID == results[1].Device.UDID {
		t.Fatalf("expected distinct UDIDs, both %s", results[0].Device.UDID)
	}
}

func TestCapacityFullWaitFalseQueues(t *testing.T) {
	p := &fakeProvisioner{}
	b := newTestBroker(t, 1, p)
	spec := testSpec()

	first, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: spec, TTL: time.Minute, Owner: Owner{PID: 1},
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if first.State != LeaseActive {
		t.Fatalf("first state=%s", first.State)
	}

	second, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: spec, TTL: time.Minute, Wait: false, Owner: Owner{PID: 2},
	})
	var cq *CapacityQueuedError
	if !errors.As(err, &cq) {
		t.Fatalf("second err=%v want CapacityQueuedError", err)
	}
	if cq.Position != 1 {
		t.Fatalf("position=%d want 1", cq.Position)
	}
	if second.State != LeaseQueued {
		t.Fatalf("second state=%s want queued", second.State)
	}
	if p.callCount() != 1 {
		t.Fatalf("provisioner calls=%d want 1", p.callCount())
	}
}

func TestAcquireWaitTrueBlocksUntilRelease(t *testing.T) {
	p := &fakeProvisioner{}
	b := newTestBroker(t, 1, p)
	spec := testSpec()

	first, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: spec, TTL: time.Minute, Owner: Owner{PID: 1},
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	started := make(chan struct{})
	done := make(chan Lease, 1)
	errCh := make(chan error, 1)
	go func() {
		close(started)
		lease, err := b.Acquire(context.Background(), AcquireRequest{
			Spec: spec, TTL: time.Minute, Wait: true, Owner: Owner{PID: 2},
		})
		if err != nil {
			errCh <- err
			return
		}
		done <- lease
	}()
	<-started
	time.Sleep(50 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("second acquire returned before release")
	case err := <-errCh:
		t.Fatalf("second acquire early error: %v", err)
	default:
	}

	if _, err := b.Release(context.Background(), first.ID); err != nil {
		t.Fatalf("release: %v", err)
	}

	select {
	case lease := <-done:
		if lease.State != LeaseActive {
			t.Fatalf("second state=%s want active", lease.State)
		}
		if lease.Device == nil || lease.Device.UDID == first.Device.UDID {
			t.Fatalf("second should get a distinct provisioned device")
		}
	case err := <-errCh:
		t.Fatalf("second: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second acquire")
	}
}

func TestReleaseQueuedCancelsWithoutProvisioning(t *testing.T) {
	p := &fakeProvisioner{}
	b := newTestBroker(t, 1, p)
	spec := testSpec()

	first, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: spec, TTL: time.Minute, Owner: Owner{PID: 1},
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	queued, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: spec, TTL: time.Minute, Wait: false, Owner: Owner{PID: 2},
	})
	if !errors.As(err, new(*CapacityQueuedError)) {
		t.Fatalf("want CapacityQueuedError, got %v", err)
	}

	released, err := b.Release(context.Background(), queued.ID)
	if err != nil {
		t.Fatalf("release queued: %v", err)
	}
	if released.State != LeaseCancelled {
		t.Fatalf("state=%s want cancelled", released.State)
	}
	if p.callCount() != 1 {
		t.Fatalf("provisioner calls=%d want 1 (only first)", p.callCount())
	}

	// First still active; releasing it should not revive the cancelled lease.
	if _, err := b.Release(context.Background(), first.ID); err != nil {
		t.Fatalf("release first: %v", err)
	}
	got, ok := b.Get(queued.ID)
	if !ok || got.State != LeaseCancelled {
		t.Fatalf("queued lease should remain cancelled, got %+v ok=%v", got, ok)
	}
}

func TestProvisionFailureReturnsTypedError(t *testing.T) {
	p := &fakeProvisioner{errAfter: 1}
	b := newTestBroker(t, 1, p)

	lease, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: testSpec(), TTL: time.Minute, Owner: Owner{PID: 1},
	})
	var pf *ProvisionFailedError
	if !errors.As(err, &pf) {
		t.Fatalf("err=%v want ProvisionFailedError", err)
	}
	if lease.State != LeaseFailed {
		t.Fatalf("state=%s want failed", lease.State)
	}
	if pf.ErrorEnvelope == nil || pf.ErrorEnvelope.Code == "" || pf.ErrorEnvelope.Fix == "" {
		t.Fatalf("missing error triple: %+v", pf.ErrorEnvelope)
	}
}

func TestRenewActiveExtendsExpiry(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	clock := now
	p := &fakeProvisioner{}
	b := New(BrokerOptions{
		Capacity:    1,
		Provisioner: p,
		Clock:       func() time.Time { return clock },
	})

	lease, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: testSpec(), TTL: time.Minute, Owner: Owner{PID: 1},
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	clock = now.Add(30 * time.Second)
	renewed, err := b.Renew(context.Background(), lease.ID, 2*time.Minute)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if !renewed.HeartbeatAt.Equal(clock) {
		t.Fatalf("heartbeat=%v want %v", renewed.HeartbeatAt, clock)
	}
	wantExp := clock.Add(2 * time.Minute)
	if !renewed.ExpiresAt.Equal(wantExp) {
		t.Fatalf("expires=%v want %v", renewed.ExpiresAt, wantExp)
	}
}

func TestRenewRejectsWrongState(t *testing.T) {
	p := &fakeProvisioner{}
	b := newTestBroker(t, 1, p)
	lease, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: testSpec(), TTL: time.Minute, Owner: Owner{PID: 1},
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if _, err := b.Release(context.Background(), lease.ID); err != nil {
		t.Fatalf("release: %v", err)
	}
	_, err = b.Renew(context.Background(), lease.ID, time.Minute)
	var inv *InvalidStateError
	if !errors.As(err, &inv) {
		t.Fatalf("err=%v want InvalidStateError", err)
	}
}

func TestGetAndListLabelSelectorAND(t *testing.T) {
	p := &fakeProvisioner{}
	b := newTestBroker(t, 4, p)

	a, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: testSpec(), Labels: map[string]string{"task": "a", "agent": "claude"},
		TTL: time.Minute, Owner: Owner{PID: 1},
	})
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	_, err = b.Acquire(context.Background(), AcquireRequest{
		Spec: testSpec(), Labels: map[string]string{"task": "a", "agent": "codex"},
		TTL: time.Minute, Owner: Owner{PID: 2},
	})
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	_, err = b.Acquire(context.Background(), AcquireRequest{
		Spec: testSpec(), Labels: map[string]string{"task": "b", "agent": "claude"},
		TTL: time.Minute, Owner: Owner{PID: 3},
	})
	if err != nil {
		t.Fatalf("c: %v", err)
	}

	got, ok := b.Get(a.ID)
	if !ok || got.ID != a.ID {
		t.Fatalf("Get failed: %+v ok=%v", got, ok)
	}

	matched := b.List(ListFilter{Labels: map[string]string{"task": "a", "agent": "claude"}})
	if len(matched) != 1 || matched[0].ID != a.ID {
		t.Fatalf("AND selector: got %+v", matched)
	}
	taskA := b.List(ListFilter{Labels: map[string]string{"task": "a"}})
	if len(taskA) != 2 {
		t.Fatalf("task=a: got %d want 2", len(taskA))
	}
}

func TestWaitUntilActive(t *testing.T) {
	p := &fakeProvisioner{}
	b := newTestBroker(t, 1, p)
	spec := testSpec()

	first, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: spec, TTL: time.Minute, Owner: Owner{PID: 1},
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	queued, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: spec, TTL: time.Minute, Wait: false, Owner: Owner{PID: 2},
	})
	if !errors.As(err, new(*CapacityQueuedError)) {
		t.Fatalf("want queued, got %v", err)
	}

	done := make(chan Lease, 1)
	errCh := make(chan error, 1)
	go func() {
		lease, err := b.Wait(context.Background(), queued.ID)
		if err != nil {
			errCh <- err
			return
		}
		done <- lease
	}()
	time.Sleep(30 * time.Millisecond)
	if _, err := b.Release(context.Background(), first.ID); err != nil {
		t.Fatalf("release: %v", err)
	}
	select {
	case lease := <-done:
		if lease.State != LeaseActive {
			t.Fatalf("wait state=%s", lease.State)
		}
	case err := <-errCh:
		t.Fatalf("wait: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("Wait timed out")
	}
}

func TestPressureQueuesEvenWithCapacity(t *testing.T) {
	p := &fakeProvisioner{}
	b := New(BrokerOptions{
		Capacity:    2,
		Provisioner: p,
		PressureOK:  func() (bool, string) { return false, "thermal elevated" },
	})
	lease, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: testSpec(), TTL: time.Minute, Wait: false, Owner: Owner{PID: 1},
	})
	var cq *CapacityQueuedError
	if !errors.As(err, &cq) {
		t.Fatalf("err=%v want CapacityQueuedError", err)
	}
	if lease.State != LeaseQueued {
		t.Fatalf("state=%s", lease.State)
	}
	if p.callCount() != 0 {
		t.Fatalf("provisioner should not run under pressure")
	}
}

func TestConcurrentAcquireReleaseRace(t *testing.T) {
	p := &fakeProvisioner{}
	b := newTestBroker(t, 4, p)
	spec := testSpec()

	const goroutines = 32
	const iters = 20
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				lease, err := b.Acquire(ctx, AcquireRequest{
					Spec:  spec,
					TTL:   time.Minute,
					Wait:  true,
					Owner: Owner{PID: 10000 + g},
				})
				cancel()
				if err != nil {
					t.Errorf("acquire g=%d i=%d: %v", g, i, err)
					return
				}
				if _, err := b.Release(context.Background(), lease.ID); err != nil {
					t.Errorf("release g=%d i=%d: %v", g, i, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}

func TestCapacityFormulaAndOverride(t *testing.T) {
	// RAM 24GB → 4, CPU 8 → 4, cap 4 → 4
	got := ComputeCapacity(8, 24<<30)
	if got != 4 {
		t.Fatalf("ComputeCapacity(8, 24GiB)=%d want 4", got)
	}
	// RAM 12GB → 2, CPU 8 → 4 → 2
	got = ComputeCapacity(8, 12<<30)
	if got != 2 {
		t.Fatalf("ComputeCapacity(8, 12GiB)=%d want 2", got)
	}
	// RAM 48GB → 8, CPU 2 → 1 → 1
	got = ComputeCapacity(2, 48<<30)
	if got != 1 {
		t.Fatalf("ComputeCapacity(2, 48GiB)=%d want 1", got)
	}

	b := New(BrokerOptions{
		Capacity:      7, // config override
		Provisioner:   &fakeProvisioner{},
		TotalRAMBytes: func() (uint64, error) { return 12 << 30, nil },
		NumCPU:        func() int { return 2 },
	})
	if b.Capacity() != 7 {
		t.Fatalf("override capacity=%d want 7", b.Capacity())
	}

	b2 := New(BrokerOptions{
		Provisioner:   &fakeProvisioner{},
		TotalRAMBytes: func() (uint64, error) { return 12 << 30, nil },
		NumCPU:        func() int { return 8 },
	})
	if b2.Capacity() != 2 {
		t.Fatalf("formula capacity=%d want 2", b2.Capacity())
	}
}

func TestLeaseNotFound(t *testing.T) {
	b := newTestBroker(t, 1, &fakeProvisioner{})
	_, err := b.Renew(context.Background(), "lease-missing", time.Minute)
	var nf *LeaseNotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("err=%v want LeaseNotFoundError", err)
	}
	_, err = b.Release(context.Background(), "lease-missing")
	if !errors.As(err, &nf) {
		t.Fatalf("release err=%v want LeaseNotFoundError", err)
	}
	_, err = b.Wait(context.Background(), "lease-missing")
	if !errors.As(err, &nf) {
		t.Fatalf("wait err=%v want LeaseNotFoundError", err)
	}
}

func TestWaitCancelRemovesWaiter(t *testing.T) {
	p := &fakeProvisioner{}
	b := newTestBroker(t, 1, p)
	spec := testSpec()

	first, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: spec, TTL: time.Minute, Owner: Owner{PID: 1},
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	queued, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: spec, TTL: time.Minute, Wait: false, Owner: Owner{PID: 2},
	})
	if !errors.As(err, new(*CapacityQueuedError)) {
		t.Fatalf("want queued, got %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := b.Wait(ctx, queued.ID)
		errCh <- err
	}()

	// Let Wait register its waiter before cancelling.
	deadline := time.Now().Add(time.Second)
	for b.waiterCount(queued.ID) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("waiter never registered")
		}
		time.Sleep(time.Millisecond)
	}

	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("wait err=%v want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after cancel")
	}

	if n := b.waiterCount(queued.ID); n != 0 {
		t.Fatalf("waiterCount(%s)=%d want 0 after cancel", queued.ID, n)
	}

	// Keep first active so the queued lease never reaches a terminal state
	// (notifyWaitersLocked must not be what cleared the map).
	_ = first
}

func TestWaitOnProvisionFailurePreservesCause(t *testing.T) {
	// Capacity 1: first acquire holds the slot; second queues. Releasing first
	// triggers provision of the queued lease, which fails. A concurrent Wait
	// must surface the real provisioner cause, not nil.
	failCause := errors.New("provision boom")
	b := newTestBroker(t, 1, &failCauseProvisioner{failOn: 2, cause: failCause})
	spec := testSpec()

	first, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: spec, TTL: time.Minute, Owner: Owner{PID: 1},
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	queued, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: spec, TTL: time.Minute, Wait: false, Owner: Owner{PID: 2},
	})
	if !errors.As(err, new(*CapacityQueuedError)) {
		t.Fatalf("want queued, got %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := b.Wait(context.Background(), queued.ID)
		errCh <- err
	}()

	deadline := time.Now().Add(time.Second)
	for b.waiterCount(queued.ID) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("waiter never registered")
		}
		time.Sleep(time.Millisecond)
	}

	if _, err := b.Release(context.Background(), first.ID); err != nil {
		t.Fatalf("release: %v", err)
	}

	select {
	case err := <-errCh:
		var pf *ProvisionFailedError
		if !errors.As(err, &pf) {
			t.Fatalf("err=%v want ProvisionFailedError", err)
		}
		if pf.Cause == nil {
			t.Fatal("ProvisionFailedError.Cause is nil; want real provisioner error")
		}
		if !errors.Is(pf.Cause, failCause) {
			t.Fatalf("Cause=%v want %v", pf.Cause, failCause)
		}
		if pf.ErrorEnvelope == nil || !strings.Contains(pf.ErrorEnvelope.Message, "provision boom") {
			t.Fatalf("envelope message missing cause: %+v", pf.ErrorEnvelope)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait timed out")
	}
}

// failCauseProvisioner succeeds until call failOn, then returns cause.
type failCauseProvisioner struct {
	mu     sync.Mutex
	calls  int
	failOn int
	cause  error
	n      atomic.Int64
}

func (p *failCauseProvisioner) Provision(ctx context.Context, req ProvisionRequest) (Device, error) {
	_ = ctx
	p.mu.Lock()
	p.calls++
	calls := p.calls
	p.mu.Unlock()
	if calls == p.failOn {
		return Device{}, p.cause
	}
	id := p.n.Add(1)
	return Device{
		UDID:    fmt.Sprintf("UDID-%d", id),
		Name:    fmt.Sprintf("sim-%d", id),
		Runtime: req.Spec.Runtime,
		Booted:  true,
	}, nil
}

func TestReclaimActiveSetsReclaimedAndAdvancesQueue(t *testing.T) {
	p := &fakeProvisioner{}
	b := newTestBroker(t, 1, p)
	spec := testSpec()

	first, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: spec, TTL: time.Minute, Owner: Owner{PID: 1},
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	queued, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: spec, TTL: time.Minute, Wait: false, Owner: Owner{PID: 2},
	})
	if !errors.As(err, new(*CapacityQueuedError)) {
		t.Fatalf("want CapacityQueuedError, got %v", err)
	}
	if queued.State != LeaseQueued {
		t.Fatalf("queued state=%s", queued.State)
	}

	reclaimed, err := b.Reclaim(context.Background(), first.ID)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if reclaimed.State != LeaseReclaimed {
		t.Fatalf("state=%s want reclaimed", reclaimed.State)
	}

	got, ok := b.Get(first.ID)
	if !ok || got.State != LeaseReclaimed {
		t.Fatalf("stored state=%+v ok=%v", got, ok)
	}

	// Capacity freed: queued lease should have been advanced into ACTIVE.
	deadline := time.Now().Add(2 * time.Second)
	var next Lease
	for {
		next, ok = b.Get(queued.ID)
		if ok && next.State == LeaseActive {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queued lease never became ACTIVE after reclaim; got %+v", next)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if next.Device == nil || next.Device.UDID == first.Device.UDID {
		t.Fatalf("next should be a distinct provisioned device; got %+v", next.Device)
	}
}

func TestReclaimNonActiveIsInvalidState(t *testing.T) {
	p := &fakeProvisioner{}
	b := newTestBroker(t, 1, p)

	active, err := b.Acquire(context.Background(), AcquireRequest{
		Spec: testSpec(), TTL: time.Minute, Owner: Owner{PID: 1},
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if _, err := b.Release(context.Background(), active.ID); err != nil {
		t.Fatalf("release: %v", err)
	}

	_, err = b.Reclaim(context.Background(), active.ID)
	var inv *InvalidStateError
	if !errors.As(err, &inv) {
		t.Fatalf("err=%v want InvalidStateError", err)
	}

	_, err = b.Reclaim(context.Background(), "lease-missing")
	var nf *LeaseNotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("err=%v want LeaseNotFoundError", err)
	}
}
