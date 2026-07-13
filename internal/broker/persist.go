package broker

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/RoshanDewmina/simurgh/internal/store"
)

// persistLocked writes the full lease map to the journal while holding b.mu.
// Nil store is a no-op. Save errors are logged and do not roll back in-memory
// state — the running process remains authoritative; the journal is best-effort
// durability for the next process.
//
// Caller must hold b.mu.
func (b *Broker) persistLocked() {
	if b.store == nil {
		return
	}
	raw, err := json.Marshal(b.leases)
	if err != nil {
		b.logger("simurgh broker: persist marshal: %v", err)
		return
	}
	if err := b.store.Save(store.State{
		SchemaVersion: store.SchemaVersion,
		Leases:        raw,
	}); err != nil {
		b.logger("simurgh broker: persist save: %v", err)
	}
}

// Rehydrate loads the journal into the in-memory lease map. Call once after
// New when Store is configured, before accepting client traffic.
//
// Startup normalization (a crash can only interrupt mid-flight work, never
// erase that a lease existed):
//   - QUEUED → keep QUEUED; re-enter the per-spec FIFO queue
//   - PROVISIONING → FAILED (no finishProvision goroutine remains)
//   - ACTIVE → keep ACTIVE (including Device) so restart does not orphan it
//   - RELEASED/CANCELLED/FAILED/RECLAIMED → keep as-is (terminal)
//
// Queue-order across restart (v0 design decision): Leases are persisted as a
// JSON object (map), reusing the existing lease JSON shape — not an ordered
// slice. Exact pre-crash FIFO order is therefore NOT guaranteed across a
// restart. On rehydrate, QUEUED leases are enqueued sorted by AcquiredAt then
// ID as a deterministic best-effort proxy for FIFO. Preferring map reuse over
// a second serialization format keeps the journal compatible with
// json.Marshal(b.leases) and avoids a schema bump.
func (b *Broker) Rehydrate() error {
	if b.store == nil {
		return nil
	}
	st, err := b.store.Load()
	if err != nil {
		return fmt.Errorf("broker: rehydrate load: %w", err)
	}
	if len(st.Leases) == 0 {
		return nil
	}

	var leases map[string]*Lease
	if err := json.Unmarshal(st.Leases, &leases); err != nil {
		return fmt.Errorf("broker: rehydrate unmarshal leases: %w", err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	var queued []*Lease
	for id, lease := range leases {
		if lease == nil {
			continue
		}
		if lease.ID == "" {
			lease.ID = id
		}
		switch lease.State {
		case LeaseProvisioning:
			lease.State = LeaseFailed
			b.logger("simurgh broker: rehydrate: lease %s was PROVISIONING at crash; marking FAILED (provision cannot resume)", lease.ID)
		case LeaseQueued:
			queued = append(queued, lease)
		}
		b.leases[lease.ID] = lease
		b.bumpIDSeqLocked(lease.ID)
	}

	sort.Slice(queued, func(i, j int) bool {
		if !queued[i].AcquiredAt.Equal(queued[j].AcquiredAt) {
			return queued[i].AcquiredAt.Before(queued[j].AcquiredAt)
		}
		return queued[i].ID < queued[j].ID
	})
	for _, lease := range queued {
		b.queueForLocked(SpecKey(lease.Spec)).enqueue(lease.ID)
	}
	return nil
}

// KnownDeviceUDIDs returns UDIDs of devices attached to non-terminal leases
// (queued/provisioning/active/expiring). Terminal leases do not contribute.
func (b *Broker) KnownDeviceUDIDs() map[string]bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	known := make(map[string]bool)
	for _, lease := range b.leases {
		switch lease.State {
		case LeaseReleased, LeaseCancelled, LeaseFailed, LeaseReclaimed:
			continue
		}
		if lease.Device != nil && lease.Device.UDID != "" {
			known[lease.Device.UDID] = true
		}
	}
	return known
}

func (b *Broker) bumpIDSeqLocked(leaseID string) {
	const prefix = "lease-"
	if !strings.HasPrefix(leaseID, prefix) {
		return
	}
	n, err := strconv.ParseUint(leaseID[len(prefix):], 10, 64)
	if err != nil {
		return
	}
	for {
		cur := b.idSeq.Load()
		if n <= cur {
			return
		}
		if b.idSeq.CompareAndSwap(cur, n) {
			return
		}
	}
}
