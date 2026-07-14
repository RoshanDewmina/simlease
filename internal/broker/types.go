package broker

import (
	"encoding/json"
	"fmt"
	"time"
)

// LeaseState is the lease lifecycle state (ARCHITECTURE §4).
// LeaseExpiring is reserved for a future soft-expiry signal; LeaseReclaimed
// is set by Broker.Reclaim (M1.3 watcher/reaper path).
type LeaseState string

const (
	LeaseQueued       LeaseState = "queued"
	LeaseProvisioning LeaseState = "provisioning"
	LeaseActive       LeaseState = "active"
	LeaseExpiring     LeaseState = "expiring"
	LeaseReleased     LeaseState = "released"
	LeaseCancelled    LeaseState = "cancelled"
	LeaseFailed       LeaseState = "failed"
	LeaseReclaimed    LeaseState = "reclaimed"
)

// Owner identifies the process/agent that holds a lease.
type Owner struct {
	PID       int    `json:"pid"`
	PPID      int    `json:"ppid,omitempty"`
	Agent     string `json:"agent,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
}

// DeviceSpec is the requested simulator shape.
type DeviceSpec struct {
	Platform string `json:"platform"`
	Model    string `json:"model"`
	Runtime  string `json:"runtime,omitempty"`
}

// Device is a granted simulator instance.
type Device struct {
	UDID    string `json:"udid"`
	Name    string `json:"name"`
	Runtime string `json:"runtime,omitempty"`
	CloneOf string `json:"cloneOf,omitempty"`
	Booted  bool   `json:"booted"`
}

// Connection describes how clients reach the device. v0 is always local.
type Connection struct {
	Transport string `json:"transport"`
}

// Timeouts holds Limrun-style inactivity and hard caps (enforced in M1.3).
// JSON encodes durations as ParseDuration strings (e.g. "30m"), not nanoseconds
// via MarshalJSON/UnmarshalJSON (struct tags alone cannot change Duration encoding).
type Timeouts struct {
	Inactivity time.Duration `json:"inactivity,omitempty"`
	Hard       time.Duration `json:"hard,omitempty"`
}

// MarshalJSON encodes Inactivity/Hard as duration strings.
func (t Timeouts) MarshalJSON() ([]byte, error) {
	type wire struct {
		Inactivity string `json:"inactivity,omitempty"`
		Hard       string `json:"hard,omitempty"`
	}
	return json.Marshal(wire{
		Inactivity: durationString(t.Inactivity),
		Hard:       durationString(t.Hard),
	})
}

// UnmarshalJSON decodes Inactivity/Hard from duration strings.
func (t *Timeouts) UnmarshalJSON(data []byte) error {
	type wire struct {
		Inactivity string `json:"inactivity"`
		Hard       string `json:"hard"`
	}
	var w wire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	inact, err := parseDurationField(w.Inactivity)
	if err != nil {
		return fmt.Errorf("timeouts.inactivity: %w", err)
	}
	hard, err := parseDurationField(w.Hard)
	if err != nil {
		return fmt.Errorf("timeouts.hard: %w", err)
	}
	t.Inactivity = inact
	t.Hard = hard
	return nil
}

// IsolationEnv holds per-lease env/flags emission (populated by a later isolator
// package; zero-valued here in M1.1).
//
// Wire names invented to match ARCHITECTURE §7 (env / xcodebuildFlags /
// shellSnippet); not every IsolationEnv field is spelled out on Lease examples.
type IsolationEnv struct {
	Env             map[string]string `json:"env,omitempty"`
	XcodebuildFlags []string          `json:"xcodebuildFlags,omitempty"`
	ShellSnippet    string            `json:"shellSnippet,omitempty"`
}

// Lease is the ARCHITECTURE §3 lease record.
//
// TTL (and Timeouts) serialize as duration strings at the JSON boundary only;
// in-process code continues to use plain time.Duration.
type Lease struct {
	ID    string     `json:"id"`
	State LeaseState `json:"state"`
	// Reused is transient acquisition metadata. It is emitted by acquire
	// response wrappers but is not persisted as lease state.
	Reused           bool              `json:"-"`
	Owner            Owner             `json:"owner"`
	Labels           map[string]string `json:"labels,omitempty"`
	Spec             DeviceSpec        `json:"spec"`
	Device           *Device           `json:"device,omitempty"`
	Env              IsolationEnv      `json:"env,omitempty"`
	Connection       Connection        `json:"connection"`
	AcquiredAt       time.Time         `json:"acquiredAt"`
	ExpiresAt        time.Time         `json:"expiresAt"`
	TTL              time.Duration     `json:"ttl"`
	HeartbeatAt      time.Time         `json:"heartbeatAt"`
	Timeouts         Timeouts          `json:"timeouts"`
	ReleaseOnPIDExit bool              `json:"releaseOnPIDExit"`
}

type leaseWire struct {
	ID               string            `json:"id"`
	State            LeaseState        `json:"state"`
	Owner            Owner             `json:"owner"`
	Labels           map[string]string `json:"labels,omitempty"`
	Spec             DeviceSpec        `json:"spec"`
	Device           *Device           `json:"device,omitempty"`
	Env              IsolationEnv      `json:"env,omitempty"`
	Connection       Connection        `json:"connection"`
	AcquiredAt       time.Time         `json:"acquiredAt"`
	ExpiresAt        time.Time         `json:"expiresAt"`
	TTL              string            `json:"ttl"`
	HeartbeatAt      time.Time         `json:"heartbeatAt"`
	Timeouts         Timeouts          `json:"timeouts"`
	ReleaseOnPIDExit bool              `json:"releaseOnPIDExit"`
}

// MarshalJSON encodes TTL as a duration string; other fields use wire names.
func (l Lease) MarshalJSON() ([]byte, error) {
	return json.Marshal(leaseWire{
		ID:               l.ID,
		State:            l.State,
		Owner:            l.Owner,
		Labels:           l.Labels,
		Spec:             l.Spec,
		Device:           l.Device,
		Env:              l.Env,
		Connection:       l.Connection,
		AcquiredAt:       l.AcquiredAt,
		ExpiresAt:        l.ExpiresAt,
		TTL:              durationString(l.TTL),
		HeartbeatAt:      l.HeartbeatAt,
		Timeouts:         l.Timeouts,
		ReleaseOnPIDExit: l.ReleaseOnPIDExit,
	})
}

// UnmarshalJSON decodes TTL from a duration string.
func (l *Lease) UnmarshalJSON(data []byte) error {
	var w leaseWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	ttl, err := parseDurationField(w.TTL)
	if err != nil {
		return fmt.Errorf("ttl: %w", err)
	}
	*l = Lease{
		ID:               w.ID,
		State:            w.State,
		Owner:            w.Owner,
		Labels:           w.Labels,
		Spec:             w.Spec,
		Device:           w.Device,
		Env:              w.Env,
		Connection:       w.Connection,
		AcquiredAt:       w.AcquiredAt,
		ExpiresAt:        w.ExpiresAt,
		TTL:              ttl,
		HeartbeatAt:      w.HeartbeatAt,
		Timeouts:         w.Timeouts,
		ReleaseOnPIDExit: w.ReleaseOnPIDExit,
	}
	return nil
}

// AcquireRequest is the transport-agnostic shape of lease.acquire params.
type AcquireRequest struct {
	Spec             DeviceSpec        `json:"spec"`
	Labels           map[string]string `json:"labels,omitempty"`
	TTL              time.Duration     `json:"ttl,omitempty"`
	ReuseIfExists    bool              `json:"reuseIfExists"`
	Wait             bool              `json:"wait"`
	ReleaseOnPIDExit bool              `json:"releaseOnPIDExit"`
	Owner            Owner             `json:"owner"`
	Timeouts         Timeouts          `json:"timeouts"`
}

type acquireRequestWire struct {
	Spec             DeviceSpec        `json:"spec"`
	Labels           map[string]string `json:"labels,omitempty"`
	TTL              string            `json:"ttl,omitempty"`
	ReuseIfExists    bool              `json:"reuseIfExists"`
	Wait             bool              `json:"wait"`
	ReleaseOnPIDExit bool              `json:"releaseOnPIDExit"`
	Owner            Owner             `json:"owner"`
	Timeouts         Timeouts          `json:"timeouts"`
}

// MarshalJSON encodes TTL as a duration string.
func (r AcquireRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(acquireRequestWire{
		Spec:             r.Spec,
		Labels:           r.Labels,
		TTL:              durationString(r.TTL),
		ReuseIfExists:    r.ReuseIfExists,
		Wait:             r.Wait,
		ReleaseOnPIDExit: r.ReleaseOnPIDExit,
		Owner:            r.Owner,
		Timeouts:         r.Timeouts,
	})
}

// UnmarshalJSON decodes TTL from a duration string.
func (r *AcquireRequest) UnmarshalJSON(data []byte) error {
	var w acquireRequestWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	ttl, err := parseDurationField(w.TTL)
	if err != nil {
		return fmt.Errorf("ttl: %w", err)
	}
	*r = AcquireRequest{
		Spec:             w.Spec,
		Labels:           w.Labels,
		TTL:              ttl,
		ReuseIfExists:    w.ReuseIfExists,
		Wait:             w.Wait,
		ReleaseOnPIDExit: w.ReleaseOnPIDExit,
		Owner:            w.Owner,
		Timeouts:         w.Timeouts,
	}
	return nil
}

// ListFilter selects leases. Labels use AND semantics across keys
// (ARCHITECTURE §9 label-selector convention).
type ListFilter struct {
	Labels map[string]string `json:"labels,omitempty"`
}

func durationString(d time.Duration) string {
	if d == 0 {
		return ""
	}
	return d.String()
}

func parseDurationField(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}
