package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
	"github.com/RoshanDewmina/simurgh/internal/ipc"
	"github.com/RoshanDewmina/simurgh/internal/reaper"
	"github.com/RoshanDewmina/simurgh/internal/watcher"
)

// Deps holds the runtime collaborators constructed by cmd/simurgh.
// This package wires and translates; it does not construct Broker/Reaper.
type Deps struct {
	Broker     *broker.Broker
	Reaper     *reaper.Reaper
	LeasesRoot string
}

// RegisterLeaseHandlers registers lease.acquire|renew|release|get|list|wait
// and pool.status on server. Call before Serve (or concurrently —
// RegisterHandler is mutex-guarded).
func RegisterLeaseHandlers(server *ipc.Server, deps Deps) {
	h := newHandlers(deps)
	server.RegisterHandler("lease.acquire", h.acquire)
	server.RegisterHandler("lease.renew", h.renew)
	server.RegisterHandler("lease.release", h.release)
	server.RegisterHandler("lease.get", h.get)
	server.RegisterHandler("lease.list", h.list)
	server.RegisterHandler("lease.wait", h.wait)
	server.RegisterHandler("pool.status", h.poolStatus)
}

type handlers struct {
	deps Deps
}

func newHandlers(deps Deps) *handlers {
	return &handlers{deps: deps}
}

// Wire DTOs — explicit so the IPC contract stays decoupled from broker's Go shape.

type wireOwner struct {
	PID       int    `json:"pid"`
	PPID      int    `json:"ppid,omitempty"`
	Agent     string `json:"agent,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
}

type wireSpec struct {
	Platform string `json:"platform"`
	Model    string `json:"model"`
	Runtime  string `json:"runtime,omitempty"`
}

type wireTimeouts struct {
	Inactivity string `json:"inactivity,omitempty"`
	Hard       string `json:"hard,omitempty"`
}

type acquireParams struct {
	Spec             wireSpec          `json:"spec"`
	Labels           map[string]string `json:"labels,omitempty"`
	TTL              string            `json:"ttl,omitempty"`
	ReuseIfExists    bool              `json:"reuseIfExists"`
	Wait             bool              `json:"wait"`
	ReleaseOnPIDExit bool              `json:"releaseOnPIDExit"`
	Owner            wireOwner         `json:"owner"`
	Timeouts         wireTimeouts      `json:"timeouts"`
}

type idParams struct {
	ID  string `json:"id"`
	TTL string `json:"ttl,omitempty"` // renew only
}

type listParams struct {
	Labels map[string]string `json:"labels,omitempty"`
}

func (h *handlers) acquire(ctx context.Context, req ipc.Request) (json.RawMessage, *ipc.Error) {
	var p acquireParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, badRequest(err)
	}
	ttl, err := parseOptionalDuration(p.TTL)
	if err != nil {
		return nil, badRequest(fmt.Errorf("ttl: %w", err))
	}
	inact, err := parseOptionalDuration(p.Timeouts.Inactivity)
	if err != nil {
		return nil, badRequest(fmt.Errorf("timeouts.inactivity: %w", err))
	}
	hard, err := parseOptionalDuration(p.Timeouts.Hard)
	if err != nil {
		return nil, badRequest(fmt.Errorf("timeouts.hard: %w", err))
	}

	lease, err := h.deps.Broker.Acquire(ctx, broker.AcquireRequest{
		Spec: broker.DeviceSpec{
			Platform: p.Spec.Platform,
			Model:    p.Spec.Model,
			Runtime:  p.Spec.Runtime,
		},
		Labels:           p.Labels,
		TTL:              ttl,
		ReuseIfExists:    p.ReuseIfExists,
		Wait:             p.Wait,
		ReleaseOnPIDExit: p.ReleaseOnPIDExit,
		Owner: broker.Owner{
			PID:       p.Owner.PID,
			PPID:      p.Owner.PPID,
			Agent:     p.Owner.Agent,
			SessionID: p.Owner.SessionID,
		},
		Timeouts: broker.Timeouts{Inactivity: inact, Hard: hard},
	})
	if err != nil {
		return nil, brokerErrorToIPC(err)
	}
	return marshalLeaseResult(lease)
}

func (h *handlers) renew(ctx context.Context, req ipc.Request) (json.RawMessage, *ipc.Error) {
	var p idParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, badRequest(err)
	}
	if p.ID == "" {
		return nil, badRequest(errors.New("id is required"))
	}
	ttl, err := parseOptionalDuration(p.TTL)
	if err != nil {
		return nil, badRequest(fmt.Errorf("ttl: %w", err))
	}
	lease, err := h.deps.Broker.Renew(ctx, p.ID, ttl)
	if err != nil {
		return nil, brokerErrorToIPC(err)
	}
	return marshalLeaseResult(lease)
}

func (h *handlers) release(ctx context.Context, req ipc.Request) (json.RawMessage, *ipc.Error) {
	var p idParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, badRequest(err)
	}
	if p.ID == "" {
		return nil, badRequest(errors.New("id is required"))
	}
	lease, err := h.deps.Broker.Release(ctx, p.ID)
	if err != nil {
		return nil, brokerErrorToIPC(err)
	}
	return marshalLeaseResult(lease)
}

func (h *handlers) get(_ context.Context, req ipc.Request) (json.RawMessage, *ipc.Error) {
	var p idParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, badRequest(err)
	}
	if p.ID == "" {
		return nil, badRequest(errors.New("id is required"))
	}
	lease, ok := h.deps.Broker.Get(p.ID)
	if !ok {
		return nil, brokerErrorToIPC(leaseNotFoundErr(p.ID))
	}
	return marshalLeaseResult(lease)
}

func (h *handlers) list(_ context.Context, req ipc.Request) (json.RawMessage, *ipc.Error) {
	var p listParams
	if len(req.Params) > 0 {
		if err := decodeParams(req.Params, &p); err != nil {
			return nil, badRequest(err)
		}
	}
	leases := h.deps.Broker.List(broker.ListFilter{Labels: p.Labels})
	raw, err := json.Marshal(map[string]any{"leases": leases})
	if err != nil {
		return nil, internalErr(err)
	}
	return raw, nil
}

func (h *handlers) wait(ctx context.Context, req ipc.Request) (json.RawMessage, *ipc.Error) {
	var p idParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, badRequest(err)
	}
	if p.ID == "" {
		return nil, badRequest(errors.New("id is required"))
	}
	lease, err := h.deps.Broker.Wait(ctx, p.ID)
	if err != nil {
		return nil, brokerErrorToIPC(err)
	}
	return marshalLeaseResult(lease)
}

// poolStatus returns capacity plus lease counts grouped by State.
func (h *handlers) poolStatus(_ context.Context, _ ipc.Request) (json.RawMessage, *ipc.Error) {
	leases := h.deps.Broker.List(broker.ListFilter{})
	counts := map[string]int{
		"queued":       0,
		"provisioning": 0,
		"active":       0,
		"released":     0,
		"cancelled":    0,
		"failed":       0,
		"reclaimed":    0,
	}
	for _, l := range leases {
		switch l.State {
		case broker.LeaseQueued:
			counts["queued"]++
		case broker.LeaseProvisioning:
			counts["provisioning"]++
		case broker.LeaseActive, broker.LeaseExpiring:
			// LeaseExpiring is reserved/future; fold into active for overview.
			counts["active"]++
		case broker.LeaseReleased:
			counts["released"]++
		case broker.LeaseCancelled:
			counts["cancelled"]++
		case broker.LeaseFailed:
			counts["failed"]++
		case broker.LeaseReclaimed:
			counts["reclaimed"]++
		}
	}
	raw, err := json.Marshal(map[string]any{
		"capacity": h.deps.Broker.Capacity(),
		"leases":   counts,
	})
	if err != nil {
		return nil, internalErr(err)
	}
	return raw, nil
}

func marshalLeaseResult(lease broker.Lease) (json.RawMessage, *ipc.Error) {
	raw, err := json.Marshal(map[string]any{"lease": lease})
	if err != nil {
		return nil, internalErr(err)
	}
	return raw, nil
}

func decodeParams(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		return errors.New("params required")
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("invalid params: %w", err)
	}
	return nil
}

func parseOptionalDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}

func leaseNotFoundErr(id string) error {
	// Reconstruct the same typed error broker uses so mapping stays uniform.
	msg := fmt.Sprintf("lease %q not found", id)
	env, err := broker.NewError(broker.CodeLeaseNotFound, msg, broker.FixLeaseNotFound)
	if err != nil {
		return fmt.Errorf("lease %q not found", id)
	}
	return &broker.LeaseNotFoundError{LeaseID: id, ErrorEnvelope: env}
}

func badRequest(err error) *ipc.Error {
	env, e := ipc.NewError("BAD_REQUEST", err.Error(), "simurgh doctor")
	if e != nil {
		return &ipc.Error{Code: "BAD_REQUEST", Message: "invalid request", Fix: "simurgh doctor"}
	}
	return env
}

func internalErr(err error) *ipc.Error {
	env, e := ipc.NewError("INTERNAL", err.Error(), "simurgh doctor")
	if e != nil {
		return &ipc.Error{Code: "INTERNAL", Message: "internal error", Fix: "simurgh doctor"}
	}
	return env
}

// brokerErrorToIPC maps every exported broker error type to an ipc.Error wire triple.
func brokerErrorToIPC(err error) *ipc.Error {
	if err == nil {
		return nil
	}

	var nf *broker.LeaseNotFoundError
	if errors.As(err, &nf) {
		return copyBrokerEnvelope(nf.ErrorEnvelope)
	}
	var inv *broker.InvalidStateError
	if errors.As(err, &inv) {
		return copyBrokerEnvelope(inv.ErrorEnvelope)
	}
	var cq *broker.CapacityQueuedError
	if errors.As(err, &cq) {
		return copyBrokerEnvelope(cq.ErrorEnvelope)
	}
	var pf *broker.ProvisionFailedError
	if errors.As(err, &pf) {
		return copyBrokerEnvelope(pf.ErrorEnvelope)
	}

	env, e := ipc.NewError("INTERNAL", err.Error(), "simurgh doctor")
	if e != nil {
		return &ipc.Error{Code: "INTERNAL", Message: "internal error", Fix: "simurgh doctor"}
	}
	return env
}

func copyBrokerEnvelope(env *broker.Error) *ipc.Error {
	if env == nil {
		out, _ := ipc.NewError("INTERNAL", "broker error missing envelope", "simurgh doctor")
		return out
	}
	out := &ipc.Error{
		Code:    env.Code,
		Message: env.Message,
		Fix:     env.Fix,
	}
	if env.QueuePosition != nil {
		pos := *env.QueuePosition
		out.QueuePosition = &pos
	}
	return out
}

// ReclaimLoop applies watcher decisions and periodically scans for orphans.
//
// Orphan policy (M1.5a judgment call): log-and-report only. Auto-reclaim of
// ScanOrphans results is deferred to a future `prune --apply` path (roadmap
// M2.3). Watcher-driven reclaim of known ACTIVE leases still deletes via
// Reaper.Reclaim → safety.DeleteSimulatorClone.
type ReclaimLoop struct {
	Deps Deps
	// Logger defaults to log.Printf.
	Logger func(format string, args ...any)
	// IsProcessAlive overrides the watcher's PID check (tests inject false).
	IsProcessAlive func(pid int) bool
	// Now overrides wall clock for the watcher (optional).
	Now func() time.Time
}

// Run watches on watchInterval and scans orphans on orphanScanInterval until
// ctx is cancelled. One failed reclaim does not stop later decisions or ticks.
func (l *ReclaimLoop) Run(ctx context.Context, watchInterval, orphanScanInterval time.Duration) {
	if l == nil || l.Deps.Broker == nil || l.Deps.Reaper == nil {
		return
	}
	logger := l.Logger
	if logger == nil {
		logger = log.Printf
	}
	if watchInterval <= 0 {
		watchInterval = 5 * time.Second
	}
	if orphanScanInterval <= 0 {
		orphanScanInterval = 5 * time.Minute
	}

	wopts := watcher.WatcherOptions{Broker: l.Deps.Broker}
	if l.IsProcessAlive != nil {
		wopts.IsProcessAlive = l.IsProcessAlive
	}
	if l.Now != nil {
		wopts.Now = l.Now
	}
	w := watcher.New(wopts)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		w.Run(ctx, watchInterval, func(decisions []watcher.ReclaimDecision) {
			l.applyDecisions(ctx, decisions, logger)
		})
	}()
	go func() {
		defer wg.Done()
		l.runOrphanScan(ctx, orphanScanInterval, logger)
	}()
	wg.Wait()
}

func (l *ReclaimLoop) applyDecisions(ctx context.Context, decisions []watcher.ReclaimDecision, logger func(string, ...any)) {
	for _, d := range decisions {
		lease, err := l.Deps.Broker.Reclaim(ctx, d.LeaseID)
		if err != nil {
			logger("simurgh reclaim: broker.Reclaim(%s) reason=%q: %v", d.LeaseID, d.Reason, err)
			continue
		}
		if err := l.Deps.Reaper.Reclaim(ctx, lease); err != nil {
			logger("simurgh reclaim: reaper.Reclaim(%s) reason=%q: %v", d.LeaseID, d.Reason, err)
			// Continue — next decision / next tick still runs.
		}
	}
}

func (l *ReclaimLoop) runOrphanScan(ctx context.Context, interval time.Duration, logger func(string, ...any)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			orphans, err := l.Deps.Reaper.ScanOrphans(ctx, knownNonTerminalUDIDs(l.Deps.Broker))
			if err != nil {
				logger("simurgh orphan scan: %v", err)
				continue
			}
			// Empty during the reaper's post-start reconciliation window is
			// expected — do not treat it as proof that no orphans exist.
			for _, udid := range orphans {
				logger("simurgh orphan scan: found unmanaged simurgh device udid=%s (log-only; auto-reclaim deferred to prune)", udid)
			}
		}
	}
}

func knownNonTerminalUDIDs(b *broker.Broker) map[string]bool {
	known := make(map[string]bool)
	if b == nil {
		return known
	}
	for _, lease := range b.List(broker.ListFilter{}) {
		switch lease.State {
		case broker.LeaseReleased, broker.LeaseCancelled, broker.LeaseFailed, broker.LeaseReclaimed:
			continue
		}
		if lease.Device != nil && lease.Device.UDID != "" {
			known[lease.Device.UDID] = true
		}
	}
	return known
}
