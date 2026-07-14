package broker

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/store"
)

const (
	defaultTTL            = 30 * time.Minute
	capacityHardCap       = 4
	capacityRAMPerLeaseGB = 6
)

// BrokerOptions configures New. Zero-valued fields get sane defaults.
type BrokerOptions struct {
	// Capacity is the max concurrent ACTIVE+PROVISIONING leases per DeviceSpec
	// key. Zero means compute via ComputeCapacity using the RAM/CPU seams.
	Capacity int
	// Provisioner is required for Acquire; tests inject a fake. M1.2 supplies
	// the real implementation.
	Provisioner Provisioner
	// Clock defaults to time.Now.
	Clock func() time.Time
	// PressureOK defaults to always-OK. When it returns false, Acquire queues
	// even if capacity remains (reason is included in CAPACITY_QUEUED).
	PressureOK func() (bool, string)
	// TotalRAMBytes is the injected RAM seam for ComputeCapacity (do not shell
	// out to sysctl in this package). Defaults to a conservative 16 GiB.
	TotalRAMBytes func() (uint64, error)
	// NumCPU defaults to runtime.NumCPU.
	NumCPU func() int
	// Store is optional journal persistence (M1.6). Nil keeps the broker
	// in-memory-only; existing tests and callers that omit it are unchanged.
	Store *store.Store
	// Logger defaults to log.Printf. Used for best-effort persist failures
	// and rehydrate normalization messages.
	Logger func(format string, args ...any)
}

// Broker is the in-memory lease lifecycle engine with optional journal
// persistence via internal/store (M1.6). When Store is configured, call
// Rehydrate once after New and before accepting client traffic.
type Broker struct {
	capacity    int
	provisioner Provisioner
	clock       func() time.Time
	pressureOK  func() (bool, string)
	store       *store.Store
	logger      func(format string, args ...any)

	mu         sync.Mutex
	leases     map[string]*Lease
	queues     map[string]*fifoQueue // SpecKey → FIFO of lease IDs
	waiters    map[string][]chan struct{}
	failCauses map[string]error // leaseID → Provisioner error (IDs are never reused)
	idSeq      atomic.Uint64
}

// New constructs a Broker with defaults applied for zero Option fields.
// Persistence is opt-in via BrokerOptions.Store; when set, the caller must
// invoke Rehydrate() before serving requests.
func New(opts BrokerOptions) *Broker {
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	pressureOK := opts.PressureOK
	if pressureOK == nil {
		pressureOK = func() (bool, string) { return true, "" }
	}
	numCPU := opts.NumCPU
	if numCPU == nil {
		numCPU = runtime.NumCPU
	}
	totalRAM := opts.TotalRAMBytes
	if totalRAM == nil {
		totalRAM = func() (uint64, error) { return 16 << 30, nil }
	}
	logger := opts.Logger
	if logger == nil {
		logger = log.Printf
	}

	capacity := opts.Capacity
	if capacity == 0 {
		ram, err := totalRAM()
		if err != nil || ram == 0 {
			ram = 16 << 30
		}
		capacity = ComputeCapacity(numCPU(), ram)
		if capacity < 1 {
			capacity = 1
		}
	}

	return &Broker{
		capacity:    capacity,
		provisioner: opts.Provisioner,
		clock:       clock,
		pressureOK:  pressureOK,
		store:       opts.Store,
		logger:      logger,
		leases:      make(map[string]*Lease),
		queues:      make(map[string]*fifoQueue),
		waiters:     make(map[string][]chan struct{}),
		failCauses:  make(map[string]error),
	}
}

// Capacity returns the effective per-spec capacity.
func (b *Broker) Capacity() int {
	return b.capacity
}

// ComputeCapacity implements ARCHITECTURE §4:
// min(floor(totalRAM_GB/6), physicalCores/2, 4).
func ComputeCapacity(physicalCores int, totalRAMBytes uint64) int {
	ramGB := int(totalRAMBytes / (1 << 30))
	byRAM := ramGB / capacityRAMPerLeaseGB
	byCPU := physicalCores / 2
	return min3(byRAM, byCPU, capacityHardCap)
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	if m < 0 {
		return 0
	}
	return m
}

// Acquire creates a lease and either provisions immediately, queues with
// CAPACITY_QUEUED, or (Wait=true) blocks until the lease reaches ACTIVE/FAILED/
// CANCELLED/RELEASED or ctx is done.
//
// When ReuseIfExists is true, a compatible ACTIVE lease is renewed in place
// (same ID/UDID/paths); a compatible in-flight PROVISIONING lease is waited
// on so concurrent duplicates share one resource. See reuse.go for the match key.
func (b *Broker) Acquire(ctx context.Context, req AcquireRequest) (Lease, error) {
	if b.provisioner == nil {
		return Lease{}, fmt.Errorf("broker: Provisioner is required")
	}

	ttl := req.TTL
	if ttl <= 0 {
		ttl = defaultTTL
	}

	if req.ReuseIfExists {
		b.mu.Lock()
		if existing := b.findReusableLocked(req); existing != nil {
			switch existing.State {
			case LeaseActive:
				b.renewInPlaceLocked(existing, req, ttl)
				out := cloneLease(existing)
				out.Reused = true
				b.persistLocked()
				b.mu.Unlock()
				return out, nil
			case LeaseProvisioning:
				ch := b.addWaiterLocked(existing.ID)
				id := existing.ID
				b.persistLocked()
				b.mu.Unlock()
				return b.waitForReusable(ctx, id, ch)
			}
		}
		b.mu.Unlock()
	}

	now := b.clock()
	id := b.newID()

	lease := &Lease{
		ID:               id,
		State:            LeaseQueued,
		Owner:            req.Owner,
		Labels:           copyLabels(req.Labels),
		Spec:             req.Spec,
		Connection:       Connection{Transport: "local"},
		TTL:              ttl,
		Timeouts:         req.Timeouts,
		ReleaseOnPIDExit: req.ReleaseOnPIDExit,
		AcquiredAt:       now,
	}

	b.mu.Lock()
	// Re-check reuse under the create lock so a concurrent duplicate that
	// began provisioning between the unlocked scan and here is attached
	// instead of allocating a second resource.
	if req.ReuseIfExists {
		if existing := b.findReusableLocked(req); existing != nil {
			switch existing.State {
			case LeaseActive:
				b.renewInPlaceLocked(existing, req, ttl)
				out := cloneLease(existing)
				out.Reused = true
				b.persistLocked()
				b.mu.Unlock()
				return out, nil
			case LeaseProvisioning:
				ch := b.addWaiterLocked(existing.ID)
				id := existing.ID
				b.persistLocked()
				b.mu.Unlock()
				return b.waitForReusable(ctx, id, ch)
			}
		}
	}

	b.leases[id] = lease

	key := SpecKey(req.Spec)
	if b.canGrantLocked(key) {
		b.beginProvisionLocked(lease)
		b.persistLocked()
		b.mu.Unlock()
		return b.finishProvision(ctx, lease)
	}

	q := b.queueForLocked(key)
	q.enqueue(id)
	pos := q.position(id)
	active := b.slotCountLocked(key)
	ok, reason := b.pressureOK()
	if ok {
		reason = ""
	}

	if !req.Wait {
		out := cloneLease(lease)
		b.persistLocked()
		b.mu.Unlock()
		return out, capacityQueued(pos, b.capacity, active, reason)
	}

	ch := b.addWaiterLocked(id)
	b.persistLocked()
	b.mu.Unlock()

	return b.waitForLease(ctx, id, ch, true)
}

// Renew extends an ACTIVE lease's expiry and bumps HeartbeatAt.
// Timeout-driven reclaim is M1.3 — this method only updates timestamps.
func (b *Broker) Renew(ctx context.Context, leaseID string, ttl time.Duration) (Lease, error) {
	_ = ctx
	if ttl <= 0 {
		ttl = defaultTTL
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	lease, ok := b.leases[leaseID]
	if !ok {
		return Lease{}, leaseNotFound(leaseID)
	}
	if lease.State != LeaseActive {
		return Lease{}, invalidState(leaseID, lease.State, "renew")
	}
	now := b.clock()
	lease.HeartbeatAt = now
	lease.TTL = ttl
	lease.ExpiresAt = now.Add(ttl)
	b.persistLocked()
	return cloneLease(lease), nil
}

// Release transitions ACTIVE → RELEASED or QUEUED → CANCELLED, frees capacity,
// and advances the per-spec FIFO queue.
//
// M1.3 hook: the reaper (via M1.4 safety) is responsible for actual device
// teardown / clone deletion after a lease leaves ACTIVE. This method only
// mutates broker state and starts provisioning the next queued lease.
func (b *Broker) Release(ctx context.Context, leaseID string) (Lease, error) {
	return b.endActiveOrQueued(ctx, leaseID, LeaseReleased, "release")
}

// CancelQueued atomically transitions QUEUED → CANCELLED. It is used by the
// watcher for a "queued owner process exited" decision so a stale decision
// cannot reclaim a lease that was promoted after the watcher snapshot.
func (b *Broker) CancelQueued(leaseID string) (Lease, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	lease, ok := b.leases[leaseID]
	if !ok {
		return Lease{}, leaseNotFound(leaseID)
	}
	if lease.State != LeaseQueued {
		return Lease{}, invalidState(leaseID, lease.State, "cancel queued")
	}
	b.queueForLocked(SpecKey(lease.Spec)).remove(leaseID)
	lease.State = LeaseCancelled
	b.notifyWaitersLocked(leaseID)
	b.persistLocked()
	return cloneLease(lease), nil
}

// Reclaim transitions ACTIVE → RECLAIMED, frees capacity, and advances the
// per-spec FIFO queue. Unlike Release, QUEUED leases cannot be reclaimed
// (use Release to cancel). Non-ACTIVE leases return InvalidStateError.
//
// This only mutates broker state. Actual device/lease-dir teardown is the
// reaper's job via internal/safety.
func (b *Broker) Reclaim(ctx context.Context, leaseID string) (Lease, error) {
	b.mu.Lock()
	lease, ok := b.leases[leaseID]
	if !ok {
		b.mu.Unlock()
		return Lease{}, leaseNotFound(leaseID)
	}
	if lease.State != LeaseActive {
		state := lease.State
		b.mu.Unlock()
		return Lease{}, invalidState(leaseID, state, "reclaim")
	}
	lease.State = LeaseReclaimed
	b.notifyWaitersLocked(leaseID)
	next := b.tryBeginNextLocked(SpecKey(lease.Spec))
	out := cloneLease(lease)
	b.persistLocked()
	b.mu.Unlock()

	if next != nil {
		_, _ = b.finishProvision(ctx, next)
	}
	return out, nil
}

// endActiveOrQueued is the shared ACTIVE/QUEUED termination path used by
// Release. terminal must be LeaseReleased (ACTIVE) — QUEUED always becomes
// CANCELLED. Reclaim has its own path because it rejects non-ACTIVE leases.
func (b *Broker) endActiveOrQueued(ctx context.Context, leaseID string, activeTerminal LeaseState, op string) (Lease, error) {
	b.mu.Lock()
	lease, ok := b.leases[leaseID]
	if !ok {
		b.mu.Unlock()
		return Lease{}, leaseNotFound(leaseID)
	}

	var next *Lease
	switch lease.State {
	case LeaseQueued:
		b.queueForLocked(SpecKey(lease.Spec)).remove(leaseID)
		lease.State = LeaseCancelled
		b.notifyWaitersLocked(leaseID)
	case LeaseActive:
		lease.State = activeTerminal
		b.notifyWaitersLocked(leaseID)
		next = b.tryBeginNextLocked(SpecKey(lease.Spec))
	case LeaseProvisioning:
		b.mu.Unlock()
		return Lease{}, invalidState(leaseID, lease.State, op)
	default:
		b.mu.Unlock()
		return Lease{}, invalidState(leaseID, lease.State, op)
	}
	out := cloneLease(lease)
	b.persistLocked()
	b.mu.Unlock()

	if next != nil {
		_, _ = b.finishProvision(ctx, next)
	}
	return out, nil
}

// Get returns a snapshot of the lease, if present.
func (b *Broker) Get(leaseID string) (Lease, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	lease, ok := b.leases[leaseID]
	if !ok {
		return Lease{}, false
	}
	return cloneLease(lease), true
}

// List returns leases matching the label selector (AND across keys). An empty
// filter matches all leases.
func (b *Broker) List(filter ListFilter) []Lease {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Lease, 0, len(b.leases))
	for _, lease := range b.leases {
		if labelsMatch(lease.Labels, filter.Labels) {
			out = append(out, cloneLease(lease))
		}
	}
	return out
}

// Wait blocks until the lease reaches ACTIVE, FAILED, CANCELLED, or RELEASED,
// or until ctx is done.
func (b *Broker) Wait(ctx context.Context, leaseID string) (Lease, error) {
	b.mu.Lock()
	lease, ok := b.leases[leaseID]
	if !ok {
		b.mu.Unlock()
		return Lease{}, leaseNotFound(leaseID)
	}
	if isWaitTerminal(lease.State) {
		out := cloneLease(lease)
		b.mu.Unlock()
		return out, nil
	}
	ch := b.addWaiterLocked(leaseID)
	b.mu.Unlock()
	return b.waitForLease(ctx, leaseID, ch, false)
}

func (b *Broker) newID() string {
	return fmt.Sprintf("lease-%d", b.idSeq.Add(1))
}

func (b *Broker) queueForLocked(key string) *fifoQueue {
	q, ok := b.queues[key]
	if !ok {
		q = &fifoQueue{}
		b.queues[key] = q
	}
	return q
}

func (b *Broker) slotCountLocked(key string) int {
	n := 0
	for _, lease := range b.leases {
		if SpecKey(lease.Spec) != key {
			continue
		}
		switch lease.State {
		case LeaseActive, LeaseProvisioning:
			n++
		}
	}
	return n
}

func (b *Broker) canGrantLocked(key string) bool {
	if b.slotCountLocked(key) >= b.capacity {
		return false
	}
	ok, _ := b.pressureOK()
	return ok
}

func (b *Broker) beginProvisionLocked(lease *Lease) {
	lease.State = LeaseProvisioning
}

// RetryQueues re-attempts promotion for every spec key with a non-empty
// queue.
//
// tryBeginNextLocked only ever fires from Release/Acquire call sites. A
// promotion attempt that fails solely because pressureOK() was transiently
// false (canGrantLocked) has no other trigger to retry once conditions
// recover — capacity can go fully idle while a queued lease waits forever,
// since nothing re-checks it after the one attempt that happened to land
// during a pressure dip. Found via B2 (BENCHMARK_PLAN.md bench matrix,
// 4-agent scenario): two leases sat QUEUED with 0 ACTIVE and capacity
// free for 20+ minutes. Intended to be called periodically (daemon wires
// it into the same loop cadence as reclaim) so a transient dip gets
// retried instead of stranding the lease permanently.
func (b *Broker) RetryQueues(ctx context.Context) {
	b.mu.Lock()
	var toProvision []*Lease
	for key, q := range b.queues {
		if len(q.ids) == 0 {
			continue
		}
		if next := b.tryBeginNextLocked(key); next != nil {
			toProvision = append(toProvision, next)
		}
	}
	b.mu.Unlock()

	for _, lease := range toProvision {
		_, _ = b.finishProvision(ctx, lease)
	}
}

// tryBeginNextLocked dequeues the next grantable lease for key and marks it
// PROVISIONING. Returns nil if the queue is empty or canGrant is false.
func (b *Broker) tryBeginNextLocked(key string) *Lease {
	if !b.canGrantLocked(key) {
		return nil
	}
	q := b.queueForLocked(key)
	for {
		id, ok := q.dequeue()
		if !ok {
			return nil
		}
		lease, exists := b.leases[id]
		if !exists || lease.State != LeaseQueued {
			continue
		}
		b.beginProvisionLocked(lease)
		return lease
	}
}

func (b *Broker) finishProvision(ctx context.Context, lease *Lease) (Lease, error) {
	device, err := b.provisioner.Provision(ctx, ProvisionRequest{
		Spec:    lease.Spec,
		LeaseID: lease.ID,
	})

	b.mu.Lock()

	// If the lease left PROVISIONING while the call ran (should not happen in
	// M1.1), leave it alone.
	if lease.State != LeaseProvisioning {
		out := cloneLease(lease)
		b.mu.Unlock()
		return out, nil
	}

	if err != nil {
		lease.State = LeaseFailed
		b.failCauses[lease.ID] = err
		b.notifyWaitersLocked(lease.ID)
		next := b.tryBeginNextLocked(SpecKey(lease.Spec))
		out := cloneLease(lease)
		b.persistLocked()
		b.mu.Unlock()
		if next != nil {
			_, _ = b.finishProvision(ctx, next)
		}
		return out, provisionFailed(err)
	}

	now := b.clock()
	d := device
	lease.Device = &d
	lease.State = LeaseActive
	lease.AcquiredAt = now
	lease.HeartbeatAt = now
	lease.ExpiresAt = now.Add(lease.TTL)
	b.notifyWaitersLocked(lease.ID)
	out := cloneLease(lease)
	b.persistLocked()
	b.mu.Unlock()
	return out, nil
}

func (b *Broker) addWaiterLocked(leaseID string) chan struct{} {
	ch := make(chan struct{})
	b.waiters[leaseID] = append(b.waiters[leaseID], ch)
	return ch
}

func (b *Broker) removeWaiterLocked(leaseID string, ch chan struct{}) {
	waiters := b.waiters[leaseID]
	for i, w := range waiters {
		if w == ch {
			last := len(waiters) - 1
			waiters[i] = waiters[last]
			waiters[last] = nil
			waiters = waiters[:last]
			if len(waiters) == 0 {
				delete(b.waiters, leaseID)
			} else {
				b.waiters[leaseID] = waiters
			}
			return
		}
	}
}

func (b *Broker) notifyWaitersLocked(leaseID string) {
	for _, ch := range b.waiters[leaseID] {
		close(ch)
	}
	delete(b.waiters, leaseID)
}

// waiterCount reports how many waiters are registered for leaseID.
// Test-only observability for waiter-leak assertions.
func (b *Broker) waiterCount(leaseID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.waiters[leaseID])
}

func (b *Broker) waitForLease(ctx context.Context, leaseID string, ch chan struct{}, cancelQueuedOnDone bool) (Lease, error) {
	for {
		select {
		case <-ctx.Done():
			return b.cancelWaiter(leaseID, ch, ctx.Err(), cancelQueuedOnDone)
		case <-ch:
			b.mu.Lock()
			lease, ok := b.leases[leaseID]
			if !ok {
				b.mu.Unlock()
				return Lease{}, leaseNotFound(leaseID)
			}
			if !isWaitTerminal(lease.State) {
				// Spurious or intermediate wake (e.g. still provisioning); re-arm.
				ch = b.addWaiterLocked(leaseID)
				b.mu.Unlock()
				continue
			}
			out := cloneLease(lease)
			state := lease.State
			cause := b.failCauses[leaseID]
			b.mu.Unlock()
			if state == LeaseFailed {
				return out, provisionFailed(cause)
			}
			return out, nil
		}
	}
}

func (b *Broker) waitForReusable(ctx context.Context, leaseID string, ch chan struct{}) (Lease, error) {
	lease, err := b.waitForLease(ctx, leaseID, ch, false)
	if err == nil {
		lease.Reused = true
	}
	return lease, err
}

// cancelWaiter drops the waiter registration. When cancelQueuedOnDone is true
// and the lease is still QUEUED (Acquire --wait owner), it is CANCELLED so
// capacity is not stranded. Wait RPC / reuse-attach paths pass false.
func (b *Broker) cancelWaiter(leaseID string, ch chan struct{}, cause error, cancelQueuedOnDone bool) (Lease, error) {
	b.mu.Lock()
	b.removeWaiterLocked(leaseID, ch)
	lease, ok := b.leases[leaseID]
	freedQueued := false
	var out Lease
	if ok {
		if cancelQueuedOnDone && lease.State == LeaseQueued {
			b.queueForLocked(SpecKey(lease.Spec)).remove(leaseID)
			lease.State = LeaseCancelled
			b.notifyWaitersLocked(leaseID)
			b.persistLocked()
			freedQueued = true
		}
		out = cloneLease(lease)
	}
	b.mu.Unlock()
	return out, acquireCancelled(leaseID, cause, freedQueued)
}

func isWaitTerminal(state LeaseState) bool {
	switch state {
	case LeaseActive, LeaseFailed, LeaseCancelled, LeaseReleased, LeaseReclaimed:
		return true
	default:
		return false
	}
}

func labelsMatch(have, want map[string]string) bool {
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}
	return true
}

func copyLabels(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneLease(l *Lease) Lease {
	if l == nil {
		return Lease{}
	}
	out := *l
	out.Labels = copyLabels(l.Labels)
	if l.Device != nil {
		d := *l.Device
		out.Device = &d
	}
	if l.Env.Env != nil {
		out.Env.Env = copyLabels(l.Env.Env)
	}
	if l.Env.XcodebuildFlags != nil {
		out.Env.XcodebuildFlags = append([]string(nil), l.Env.XcodebuildFlags...)
	}
	return out
}
