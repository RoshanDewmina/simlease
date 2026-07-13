package watcher

import (
	"context"
	"syscall"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
)

// BrokerLister is the broker surface the watcher needs. Matching
// broker.Broker.List keeps fakes easy without requiring a concrete *Broker.
type BrokerLister interface {
	List(filter broker.ListFilter) []broker.Lease
}

// ReclaimDecision is a pure decision that Tick returns; callers apply it.
type ReclaimDecision struct {
	LeaseID string
	Reason  string
}

// WatcherOptions configures New. Zero-valued seams get sane defaults.
type WatcherOptions struct {
	Broker            BrokerLister
	Now               func() time.Time
	IsProcessAlive    func(pid int) bool
	DefaultInactivity time.Duration
	DefaultHardCap    time.Duration
}

// Watcher scans ACTIVE leases and decides which should be reclaimed.
type Watcher struct {
	broker            BrokerLister
	now               func() time.Time
	isProcessAlive    func(pid int) bool
	defaultInactivity time.Duration
	defaultHardCap    time.Duration
}

// New constructs a Watcher. Broker is required; Now defaults to time.Now;
// IsProcessAlive defaults to syscall.Kill(pid, 0) == nil (Darwin-safe).
func New(opts WatcherOptions) *Watcher {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	alive := opts.IsProcessAlive
	if alive == nil {
		alive = func(pid int) bool {
			return syscall.Kill(pid, 0) == nil
		}
	}
	return &Watcher{
		broker:            opts.Broker,
		now:               now,
		isProcessAlive:    alive,
		defaultInactivity: opts.DefaultInactivity,
		defaultHardCap:    opts.DefaultHardCap,
	}
}

// Tick scans ACTIVE leases and returns reclaim decisions without side effects.
// Priority per lease: PID exit (--rm) → PID not alive → hard → inactivity → TTL.
func (w *Watcher) Tick(ctx context.Context) []ReclaimDecision {
	_ = ctx
	if w == nil || w.broker == nil {
		return nil
	}
	now := w.now()
	leases := w.broker.List(broker.ListFilter{})
	var out []ReclaimDecision
	for _, lease := range leases {
		if lease.State != broker.LeaseActive {
			continue
		}
		if reason, ok := w.decide(lease, now); ok {
			out = append(out, ReclaimDecision{LeaseID: lease.ID, Reason: reason})
		}
	}
	return out
}

func (w *Watcher) decide(lease broker.Lease, now time.Time) (string, bool) {
	alive := w.isProcessAlive(lease.Owner.PID)
	if lease.ReleaseOnPIDExit && !alive {
		return "owner process exited", true
	}
	if !alive {
		return "owner process not alive", true
	}

	hard := lease.Timeouts.Hard
	if hard <= 0 {
		hard = w.defaultHardCap
	}
	if hard > 0 && now.Sub(lease.AcquiredAt) > hard {
		return "hard timeout", true
	}

	inactivity := lease.Timeouts.Inactivity
	if inactivity <= 0 {
		inactivity = w.defaultInactivity
	}
	if inactivity > 0 && now.Sub(lease.HeartbeatAt) > inactivity {
		return "inactivity timeout", true
	}

	if now.After(lease.ExpiresAt) {
		return "ttl expired", true
	}
	return "", false
}

// Run calls Tick on each interval and invokes onDecide with the result until
// ctx is cancelled. Production wiring (later package) passes a callback that
// calls Broker.Reclaim then the reaper.
func (w *Watcher) Run(ctx context.Context, interval time.Duration, onDecide func([]ReclaimDecision)) {
	if w == nil || onDecide == nil {
		return
	}
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			onDecide(w.Tick(ctx))
		}
	}
}
