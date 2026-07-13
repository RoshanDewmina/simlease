package broker

import "time"

// LeaseState is the lease lifecycle state (ARCHITECTURE §4).
// expiring and reclaimed are declared for M1.3 but never set by this package.
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
	PID       int
	PPID      int
	Agent     string
	SessionID string
}

// DeviceSpec is the requested simulator shape.
type DeviceSpec struct {
	Platform string
	Model    string
	Runtime  string
}

// Device is a granted simulator instance.
type Device struct {
	UDID    string
	Name    string
	Runtime string
	CloneOf string
	Booted  bool
}

// Connection describes how clients reach the device. v0 is always local.
type Connection struct {
	Transport string
}

// Timeouts holds Limrun-style inactivity and hard caps (enforced in M1.3).
type Timeouts struct {
	Inactivity time.Duration
	Hard       time.Duration
}

// IsolationEnv holds per-lease env/flags emission (populated by a later isolator
// package; zero-valued here in M1.1).
type IsolationEnv struct {
	Env             map[string]string
	XcodebuildFlags []string
	ShellSnippet    string
}

// Lease is the ARCHITECTURE §3 lease record.
type Lease struct {
	ID               string
	State            LeaseState
	Owner            Owner
	Labels           map[string]string
	Spec             DeviceSpec
	Device           *Device
	Env              IsolationEnv
	Connection       Connection
	AcquiredAt       time.Time
	ExpiresAt        time.Time
	TTL              time.Duration
	HeartbeatAt      time.Time
	Timeouts         Timeouts
	ReleaseOnPIDExit bool
}

// AcquireRequest is the transport-agnostic shape of lease.acquire params.
type AcquireRequest struct {
	Spec             DeviceSpec
	Labels           map[string]string
	TTL              time.Duration
	ReuseIfExists    bool
	Wait             bool
	ReleaseOnPIDExit bool
	Owner            Owner
	Timeouts         Timeouts
}

// ListFilter selects leases. Labels use AND semantics across keys
// (ARCHITECTURE §9 label-selector convention).
type ListFilter struct {
	Labels map[string]string
}
