package broker

import (
	"strings"
	"time"
)

// Reuse matching (R-WP1) — conservative lease-level reuse, not a session abstraction.
//
// Compatibility key (only fields that exist on Spec + Labels + Owner today):
//
//	DeviceSpec (platform | model | runtime)
//	Labels     — exact map equality, including non-empty "project"
//	Owner.Agent + Owner.SessionID
//
// Explicitly NOT part of the key:
//   - Owner.PID / Owner.PPID — process restarts must still match
//   - project path, Xcode version, scheme/configuration — not first-class on
//     AcquireRequest; callers that need those trust boundaries put them in Labels
//
// Eligible states: ACTIVE (renew in place) and PROVISIONING (attach/wait so
// concurrent duplicates share one provision). RELEASED / FAILED / RECLAIMED /
// CANCELLED / QUEUED never reused as a grant target (QUEUED may still be
// cancelled by waiter/owner liveness — separate from reuse).
//
// On compatible ACTIVE reuse the broker renews HeartbeatAt/ExpiresAt/TTL,
// updates Owner (new PID) and ReleaseOnPIDExit from the request, and returns
// the same lease ID / Device / isolation identity without reprovisioning.

// reuseCompatible reports whether req may attach to lease under the R-WP1 key.
func reuseCompatible(req AcquireRequest, lease *Lease) bool {
	if lease == nil {
		return false
	}
	if req.Owner.Agent == "" || req.Owner.SessionID == "" {
		return false
	}
	if lease.Owner.Agent == "" || lease.Owner.SessionID == "" {
		return false
	}
	if len(req.Labels) == 0 || strings.TrimSpace(req.Labels["project"]) == "" {
		return false
	}
	if len(lease.Labels) == 0 || strings.TrimSpace(lease.Labels["project"]) == "" {
		return false
	}
	if SpecKey(req.Spec) != SpecKey(lease.Spec) {
		return false
	}
	if !labelsExactEqual(req.Labels, lease.Labels) {
		return false
	}
	if req.Owner.Agent != lease.Owner.Agent {
		return false
	}
	if req.Owner.SessionID != lease.Owner.SessionID {
		return false
	}
	return true
}

func labelsExactEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// findReusableLocked returns a compatible ACTIVE lease, or else a compatible
// PROVISIONING lease (for concurrent attach). Caller must hold b.mu.
func (b *Broker) findReusableLocked(req AcquireRequest) *Lease {
	var active, provisioning *Lease
	for _, lease := range b.leases {
		if !reuseCompatible(req, lease) {
			continue
		}
		switch lease.State {
		case LeaseActive:
			if active == nil || lease.ID < active.ID {
				active = lease
			}
		case LeaseProvisioning:
			if provisioning == nil || lease.ID < provisioning.ID {
				provisioning = lease
			}
		}
	}
	if active != nil {
		return active
	}
	return provisioning
}

// renewInPlaceLocked applies a reuse renew onto an ACTIVE lease.
// Caller must hold b.mu; lease.State must be LeaseActive.
func (b *Broker) renewInPlaceLocked(lease *Lease, req AcquireRequest, ttl time.Duration) {
	now := b.clock()
	lease.Owner = req.Owner
	lease.ReleaseOnPIDExit = req.ReleaseOnPIDExit
	lease.HeartbeatAt = now
	lease.TTL = ttl
	lease.ExpiresAt = now.Add(ttl)
	if req.Timeouts.Inactivity > 0 || req.Timeouts.Hard > 0 {
		lease.Timeouts = req.Timeouts
	}
}
