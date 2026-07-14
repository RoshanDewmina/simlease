package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
	"github.com/RoshanDewmina/simurgh/internal/controlplane"
	"github.com/RoshanDewmina/simurgh/internal/ipc"
	"github.com/RoshanDewmina/simurgh/internal/isolator"
	"github.com/RoshanDewmina/simurgh/internal/pressure"
	"github.com/RoshanDewmina/simurgh/internal/provisioner"
	"github.com/RoshanDewmina/simurgh/internal/reaper"
	"github.com/RoshanDewmina/simurgh/internal/safety"
	"github.com/RoshanDewmina/simurgh/internal/watcher"
)

// Deps holds the runtime collaborators constructed by cmd/simurgh.
// This package wires and translates; it does not construct Broker/Reaper.
type Deps struct {
	Broker        *broker.Broker
	Reaper        *reaper.Reaper
	Provisioner   *provisioner.Provisioner
	ControlPlane  *controlplane.ControlPlane // optional; surfaces build-slot status
	LeasesRoot    string
	DeviceLister  safety.DeviceLister  // warm.remove → DeleteSimulatorClone
	DeviceDeleter safety.DeviceDeleter // warm.remove → DeleteSimulatorClone
}

// RegisterLeaseHandlers registers lease.acquire|renew|release|get|list|wait,
// pool.status, warm.create|list|remove, and prune.plan|apply on server. Call
// before Serve (or concurrently — RegisterHandler is mutex-guarded).
func RegisterLeaseHandlers(server *ipc.Server, deps Deps) {
	h := newHandlers(deps)
	server.RegisterHandler("lease.acquire", h.acquire)
	server.RegisterHandler("lease.renew", h.renew)
	server.RegisterHandler("lease.release", h.release)
	server.RegisterHandler("lease.get", h.get)
	server.RegisterHandler("lease.list", h.list)
	server.RegisterHandler("lease.wait", h.wait)
	server.RegisterHandler("pool.status", h.poolStatus)
	server.RegisterHandler("warm.create", h.warmCreate)
	server.RegisterHandler("warm.list", h.warmList)
	server.RegisterHandler("warm.remove", h.warmRemove)
	server.RegisterHandler("prune.plan", h.prunePlan)
	server.RegisterHandler("prune.apply", h.pruneApply)
}

// AdvanceRehydratedQueues performs the one immediate post-rehydrate global
// queue retry required at daemon startup. It refuses to provision until the
// broker, reaper, and daemon-facing provisioner dependencies are all ready;
// cmd/simurgh calls it after constructing Deps and registering handlers, before
// starting the periodic reclaim loop or accepting clients.
func AdvanceRehydratedQueues(ctx context.Context, deps Deps) error {
	if deps.Broker == nil {
		return errors.New("daemon: broker not ready for rehydrated queue advance")
	}
	if deps.Reaper == nil {
		return errors.New("daemon: reaper not ready for rehydrated queue advance")
	}
	if deps.Provisioner == nil {
		return errors.New("daemon: provisioner not ready for rehydrated queue advance")
	}
	deps.Broker.RetryQueues(ctx)
	return nil
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
	// Side-effecting isolation setup once per grant (ACTIVE with a device).
	// Env is recomputed fresh on every wire response — not stored on the broker lease.
	if lease.Device != nil && h.deps.LeasesRoot != "" {
		dirs, err := isolator.EnsureLeaseDir(h.deps.LeasesRoot, lease.ID)
		if err != nil {
			return nil, internalErr(err)
		}
		env := isolator.BuildEnv(dirs, lease.ID, lease.Device.UDID)
		if err := isolator.WriteLeaseSnapshot(dirs, lease, env); err != nil {
			return nil, internalErr(err)
		}
	}
	return h.marshalLeaseResult(lease)
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
	return h.marshalLeaseResult(lease)
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
	// Immediate teardown after a successful Release (M2.3). Lease state stays
	// RELEASED/CANCELLED — this is an operational reaper step, not a
	// RECLAIMED transition. Best-effort: teardown failure is logged and does
	// not change the release RPC outcome (mirror ReclaimLoop.applyDecisions).
	if lease.Device != nil && h.deps.Reaper != nil {
		if err := h.deps.Reaper.Reclaim(ctx, lease); err != nil {
			log.Printf("simurgh release: reaper.Reclaim(%s): %v", lease.ID, err)
		}
	}
	return h.marshalLeaseResult(lease)
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
	return h.marshalLeaseResult(lease)
}

func (h *handlers) list(_ context.Context, req ipc.Request) (json.RawMessage, *ipc.Error) {
	var p listParams
	if len(req.Params) > 0 {
		if err := decodeParams(req.Params, &p); err != nil {
			return nil, badRequest(err)
		}
	}
	leases := h.deps.Broker.List(broker.ListFilter{Labels: p.Labels})
	out := make([]broker.Lease, len(leases))
	for i, l := range leases {
		out[i] = h.withIsolationEnv(l)
	}
	raw, err := json.Marshal(map[string]any{"leases": out})
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
	return h.marshalLeaseResult(lease)
}

// poolStatus returns capacity plus lease counts grouped by State.
// Additive R-WP3 fields buildSlots/activeBuilds/queuedBuilds come from the
// control-plane build scheduler when configured; they never alter lease.acquire.
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
	build := controlplane.BuildStatus{BuildSlots: controlplane.DefaultBuildSlots}
	if h.deps.ControlPlane != nil {
		build = h.deps.ControlPlane.BuildStatus()
	}
	raw, err := json.Marshal(map[string]any{
		"capacity":                  h.deps.Broker.Capacity(),
		"perSpecCapacity":           h.deps.Broker.PerSpecCapacity(),
		"maxProvisioningOperations": h.deps.Broker.MaxProvisioningOperations(),
		"buildSlots":                build.BuildSlots,
		"activeBuilds":              build.ActiveBuilds,
		"queuedBuilds":              build.QueuedBuilds,
		"leases":                    counts,
		"pressure":                  pressure.Check(h.deps.LeasesRoot),
	})
	if err != nil {
		return nil, internalErr(err)
	}
	return raw, nil
}

// marshalLeaseResult shapes a single-lease wire response, recomputing
// IsolationEnv when the lease has a Device (pure; does not mutate broker state).
func (h *handlers) marshalLeaseResult(lease broker.Lease) (json.RawMessage, *ipc.Error) {
	raw, err := json.Marshal(map[string]any{
		"lease":  h.withIsolationEnv(lease),
		"reused": lease.Reused,
	})
	if err != nil {
		return nil, internalErr(err)
	}
	return raw, nil
}

// withIsolationEnv returns a copy of lease with Env populated from LeasesRoot
// when Device is set. Paths are derived without mkdir — EnsureLeaseDir runs
// only in acquire.
func (h *handlers) withIsolationEnv(lease broker.Lease) broker.Lease {
	if lease.Device == nil || lease.Device.UDID == "" || h.deps.LeasesRoot == "" {
		return lease
	}
	root := filepath.Join(h.deps.LeasesRoot, lease.ID)
	dirs := isolator.LeaseDirs{
		Root:        root,
		DerivedData: filepath.Join(root, "DerivedData"),
		ModuleCache: filepath.Join(root, "ModuleCache"),
		SwiftPM:     filepath.Join(root, "SwiftPM"),
		Results:     filepath.Join(root, "Results"),
		Tmp:         filepath.Join(root, "tmp"),
	}
	lease.Env = isolator.BuildEnv(dirs, lease.ID, lease.Device.UDID)
	return lease
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
	var ac *broker.AcquireCancelledError
	if errors.As(err, &ac) {
		return copyBrokerEnvelope(ac.ErrorEnvelope)
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
		Code:          env.Code,
		Message:       env.Message,
		Fix:           env.Fix,
		Reason:        env.Reason,
		ResourceClass: env.ResourceClass,
	}
	if env.QueuePosition != nil {
		pos := *env.QueuePosition
		out.QueuePosition = &pos
	}
	return out
}

type warmSpecParams struct {
	Model   string `json:"model"`
	Runtime string `json:"runtime,omitempty"`
}

func (h *handlers) warmCreate(ctx context.Context, req ipc.Request) (json.RawMessage, *ipc.Error) {
	if h.deps.Provisioner == nil {
		return nil, internalErr(errors.New("provisioner not configured"))
	}
	var p warmSpecParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, badRequest(err)
	}
	if p.Model == "" {
		return nil, badRequest(errors.New("model is required"))
	}
	dev, err := h.deps.Provisioner.CreateWarmTemplate(ctx, broker.DeviceSpec{
		Platform: "iOS",
		Model:    p.Model,
		Runtime:  p.Runtime,
	})
	if err != nil {
		return nil, provisionerErrorToIPC(err)
	}
	raw, err := json.Marshal(map[string]any{"device": dev})
	if err != nil {
		return nil, internalErr(err)
	}
	return raw, nil
}

func (h *handlers) warmList(ctx context.Context, _ ipc.Request) (json.RawMessage, *ipc.Error) {
	if h.deps.Provisioner == nil {
		return nil, internalErr(errors.New("provisioner not configured"))
	}
	devices, err := h.deps.Provisioner.ListWarmTemplates(ctx)
	if err != nil {
		return nil, provisionerErrorToIPC(err)
	}
	if devices == nil {
		devices = []broker.Device{}
	}
	raw, err := json.Marshal(map[string]any{"devices": devices})
	if err != nil {
		return nil, internalErr(err)
	}
	return raw, nil
}

func (h *handlers) warmRemove(ctx context.Context, req ipc.Request) (json.RawMessage, *ipc.Error) {
	if h.deps.Provisioner == nil {
		return nil, internalErr(errors.New("provisioner not configured"))
	}
	if h.deps.DeviceLister == nil || h.deps.DeviceDeleter == nil {
		return nil, internalErr(errors.New("device lister/deleter not configured"))
	}
	var p warmSpecParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, badRequest(err)
	}
	if p.Model == "" {
		return nil, badRequest(errors.New("model is required"))
	}

	templates, err := h.deps.Provisioner.ListWarmTemplates(ctx)
	if err != nil {
		return nil, provisionerErrorToIPC(err)
	}
	dev, err := findWarmTemplate(templates, p.Model, p.Runtime)
	if err != nil {
		return nil, badRequest(err)
	}

	if err := safety.DeleteSimulatorClone(ctx, h.deps.DeviceLister, h.deps.DeviceDeleter, dev.UDID, nil); err != nil {
		return nil, safetyErrorToIPC(err)
	}
	raw, mErr := json.Marshal(map[string]any{"device": dev})
	if mErr != nil {
		return nil, internalErr(mErr)
	}
	return raw, nil
}

func (h *handlers) prunePlan(ctx context.Context, _ ipc.Request) (json.RawMessage, *ipc.Error) {
	if h.deps.Broker == nil || h.deps.Reaper == nil {
		return nil, internalErr(errors.New("broker/reaper not configured"))
	}
	plan, err := h.buildPrunePlan(ctx)
	if err != nil {
		return nil, internalErr(err)
	}
	return marshalPrunePlan(plan)
}

func (h *handlers) pruneApply(ctx context.Context, _ ipc.Request) (json.RawMessage, *ipc.Error) {
	if h.deps.Broker == nil || h.deps.Reaper == nil {
		return nil, internalErr(errors.New("broker/reaper not configured"))
	}
	if h.deps.DeviceLister == nil || h.deps.DeviceDeleter == nil {
		return nil, internalErr(errors.New("device lister/deleter not configured"))
	}
	// Re-derive the plan fresh — never trust a client-supplied plan.
	plan, err := h.buildPrunePlan(ctx)
	if err != nil {
		return nil, internalErr(err)
	}

	summary := pruneApplySummary{
		HeldReclaimed:  0,
		OrphansDeleted: 0,
		Errors:         nil,
	}
	for _, lease := range plan.Held {
		if err := h.deps.Reaper.Reclaim(ctx, lease); err != nil {
			summary.Errors = append(summary.Errors, fmt.Sprintf("held %s: %v", lease.ID, err))
			continue
		}
		summary.HeldReclaimed++
	}
	for _, udid := range plan.Orphans {
		if err := safety.DeleteSimulatorClone(ctx, h.deps.DeviceLister, h.deps.DeviceDeleter, udid, nil); err != nil {
			summary.Errors = append(summary.Errors, fmt.Sprintf("orphan %s: %v", udid, err))
			continue
		}
		summary.OrphansDeleted++
	}
	// Active bucket is intentionally never touched.
	raw, mErr := json.Marshal(summary)
	if mErr != nil {
		return nil, internalErr(mErr)
	}
	return raw, nil
}

func (h *handlers) buildPrunePlan(ctx context.Context) (reaper.PruneBucket, error) {
	leases := h.deps.Broker.List(broker.ListFilter{})
	return h.deps.Reaper.PlanPrune(ctx, leases, h.deps.Broker.KnownDeviceUDIDs())
}

type pruneApplySummary struct {
	HeldReclaimed  int      `json:"heldReclaimed"`
	OrphansDeleted int      `json:"orphansDeleted"`
	Errors         []string `json:"errors,omitempty"`
}

func marshalPrunePlan(plan reaper.PruneBucket) (json.RawMessage, *ipc.Error) {
	held := plan.Held
	if held == nil {
		held = []broker.Lease{}
	}
	active := plan.Active
	if active == nil {
		active = []broker.Lease{}
	}
	orphans := plan.Orphans
	if orphans == nil {
		orphans = []string{}
	}
	raw, err := json.Marshal(map[string]any{
		"held":               held,
		"orphans":            orphans,
		"active":             active,
		"orphanScanDeferred": plan.OrphanScanDeferred,
	})
	if err != nil {
		return nil, internalErr(err)
	}
	return raw, nil
}

func findWarmTemplate(templates []broker.Device, model, runtime string) (broker.Device, error) {
	prefix := fmt.Sprintf("simurgh-warm[%s:", model)
	var matches []broker.Device
	for _, d := range templates {
		if !strings.HasPrefix(d.Name, prefix) {
			continue
		}
		if runtime != "" && d.Runtime != runtime && d.Name != fmt.Sprintf("simurgh-warm[%s:%s]", model, runtime) {
			continue
		}
		matches = append(matches, d)
	}
	switch len(matches) {
	case 0:
		if runtime != "" {
			return broker.Device{}, fmt.Errorf("no warm template for model %q runtime %q", model, runtime)
		}
		return broker.Device{}, fmt.Errorf("no warm template for model %q", model)
	case 1:
		return matches[0], nil
	default:
		return broker.Device{}, fmt.Errorf("multiple warm templates for model %q; pass --runtime", model)
	}
}

// provisionerErrorToIPC maps provisioner typed errors to ipc.Error wire triples.
func provisionerErrorToIPC(err error) *ipc.Error {
	if err == nil {
		return nil
	}
	var dt *provisioner.DeviceTypeNotFoundError
	if errors.As(err, &dt) && dt.ErrorEnvelope != nil {
		return copyProvisionerEnvelope(dt.ErrorEnvelope)
	}
	var rt *provisioner.RuntimeNotFoundError
	if errors.As(err, &rt) && rt.ErrorEnvelope != nil {
		return copyProvisionerEnvelope(rt.ErrorEnvelope)
	}
	var cf *provisioner.CloneFailedError
	if errors.As(err, &cf) && cf.ErrorEnvelope != nil {
		return copyProvisionerEnvelope(cf.ErrorEnvelope)
	}
	var bt *provisioner.BootTimeoutError
	if errors.As(err, &bt) && bt.ErrorEnvelope != nil {
		return copyProvisionerEnvelope(bt.ErrorEnvelope)
	}
	return internalErr(err)
}

func copyProvisionerEnvelope(env *provisioner.Error) *ipc.Error {
	if env == nil {
		out, _ := ipc.NewError("INTERNAL", "provisioner error missing envelope", "simurgh doctor")
		return out
	}
	out, _ := ipc.NewError(env.Code, env.Message, env.Fix)
	return out
}

// safetyErrorToIPC maps safety typed errors to ipc.Error wire triples.
func safetyErrorToIPC(err error) *ipc.Error {
	if err == nil {
		return nil
	}
	var nm *safety.DeviceNotManagedError
	if errors.As(err, &nm) && nm.ErrorEnvelope != nil {
		return copySafetyEnvelope(nm.ErrorEnvelope)
	}
	var nf *safety.DeviceNotFoundError
	if errors.As(err, &nf) && nf.ErrorEnvelope != nil {
		return copySafetyEnvelope(nf.ErrorEnvelope)
	}
	var df *safety.DeviceDeleteFailedError
	if errors.As(err, &df) && df.ErrorEnvelope != nil {
		return copySafetyEnvelope(df.ErrorEnvelope)
	}
	return internalErr(err)
}

func copySafetyEnvelope(env *safety.Error) *ipc.Error {
	if env == nil {
		out, _ := ipc.NewError("INTERNAL", "safety error missing envelope", "simurgh doctor")
		return out
	}
	out, _ := ipc.NewError(env.Code, env.Message, env.Fix)
	return out
}

// ReclaimLoop applies watcher decisions and periodically scans for orphans.
//
// Orphan policy (M2.3): the background scan remains log-and-report only.
// Human-invoked `simurgh prune --apply` is the explicit deletion path for
// discovered orphans. Watcher-driven reclaim of known ACTIVE leases still
// deletes via Reaper.Reclaim → safety.DeleteSimulatorClone.
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
	wg.Add(3)
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
	go func() {
		defer wg.Done()
		l.runQueueRetry(ctx, watchInterval)
	}()
	wg.Wait()
}

// runQueueRetry periodically re-attempts promotion for queued leases on
// the same cadence as the watcher tick. Broker.RetryQueues exists because
// a promotion that fails solely on a transient pressure dip otherwise has
// no other trigger to retry once conditions recover (see its doc comment).
func (l *ReclaimLoop) runQueueRetry(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.Deps.Broker.RetryQueues(ctx)
		}
	}
}

func (l *ReclaimLoop) applyDecisions(ctx context.Context, decisions []watcher.ReclaimDecision, logger func(string, ...any)) {
	for _, d := range decisions {
		if d.Reason == "queued owner process exited" {
			lease, err := l.Deps.Broker.CancelQueued(d.LeaseID)
			if err != nil {
				// A promotion between watcher snapshot and apply makes the
				// decision stale. Skip it; never fall through to Reclaim.
				logger("simurgh reclaim: skip stale queued-owner decision lease=%s: %v", d.LeaseID, err)
				continue
			}
			logger("simurgh reclaim: cancelled QUEUED lease %s reason=%q state=%s", lease.ID, d.Reason, lease.State)
			continue
		}

		cur, ok := l.Deps.Broker.Get(d.LeaseID)
		if !ok {
			logger("simurgh reclaim: lease %s gone before apply reason=%q", d.LeaseID, d.Reason)
			continue
		}
		if cur.State != broker.LeaseActive {
			logger("simurgh reclaim: skip stale decision lease=%s state=%s reason=%q", d.LeaseID, cur.State, d.Reason)
			continue
		}
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
			orphans, err := l.Deps.Reaper.Reconcile(ctx, knownNonTerminalUDIDs(l.Deps.Broker))
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

// knownNonTerminalUDIDs delegates to Broker.KnownDeviceUDIDs (crash-durable
// after Rehydrate). Kept as a thin helper so nil Broker stays safe.
func knownNonTerminalUDIDs(b *broker.Broker) map[string]bool {
	if b == nil {
		return map[string]bool{}
	}
	return b.KnownDeviceUDIDs()
}
