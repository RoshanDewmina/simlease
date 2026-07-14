package controlplane

import "time"

// JobState is the R-WP3 job finite-state vocabulary (exact wire values).
type JobState string

const (
	JobQueued           JobState = "queued"
	JobSyncing          JobState = "syncing"
	JobPreparing        JobState = "preparing"
	JobBuilding         JobState = "building"
	JobWaitingForDevice JobState = "waiting_for_device"
	JobInstalling       JobState = "installing"
	JobLaunching        JobState = "launching"
	JobTesting          JobState = "testing"
	JobCollecting       JobState = "collecting"
	JobSucceeded        JobState = "succeeded"
	JobFailed           JobState = "failed"
	JobCancelled        JobState = "cancelled"
	JobOrphaned         JobState = "orphaned"
	JobRetrying         JobState = "retrying"
)

// JobKind classifies the work a Job performs.
type JobKind string

const (
	JobKindSync    JobKind = "sync"
	JobKindBuild   JobKind = "build"
	JobKindTest    JobKind = "test"
	JobKindInstall JobKind = "install"
	JobKindLaunch  JobKind = "launch"
	JobKindCapture JobKind = "capture"
	JobKindCleanup JobKind = "cleanup"
)

// ResourceClassBuildSlot is the contended resource for the build scheduler.
// Independent from R-WP2 device/simulator deny-order classes.
const ResourceClassBuildSlot = "build_slot"

// FailureClass buckets why a job failed (feeds retry classification).
type FailureClass string

const (
	FailureInfrastructure FailureClass = "infrastructure"
	FailureSource         FailureClass = "source"
	FailureCancelled      FailureClass = "cancelled"
	FailureUnknown        FailureClass = "unknown"
)

// RetryClass answers whether a failed job is safe to retry.
type RetryClass string

const (
	RetrySafe    RetryClass = "safe"
	RetryUnsafe  RetryClass = "unsafe"
	RetryUnknown RetryClass = "unknown"
)

// Session is a parent around one or more Jobs and optional device leases.
type Session struct {
	ID          string
	Project     string
	Agent       string
	Task        string
	Labels      map[string]string
	Workspace   *Workspace
	WorkerID    string
	JobIDs      []string
	LeaseIDs    []string
	ArtifactIDs []string
	CreatedAt   time.Time
}

// Job is a single unit of work under a Session.
type Job struct {
	ID            string
	SessionID     string
	Kind          JobKind
	State         JobState
	WorkerID      string
	BuildGrantID  string
	DeviceLeaseID string
	FailureClass  FailureClass
	RetryClass    RetryClass
	FailureDetail string
	ArtifactIDs   []string
	Timings       JobTimings
	CreatedAt     time.Time
	UpdatedAt     time.Time
	generation    uint64
}

// Worker describes a build/device host (local-only for R-WP3).
type Worker struct {
	ID              string
	DisplayName     string
	BuildSlots      int
	SimulatorSlots  int
	PhysicalSlots   int
	LastHeartbeatAt time.Time
}

// Workspace is the source tree a Session operates on.
type Workspace struct {
	ID          string
	Path        string
	Repository  string
	Branch      string
	Fingerprint string
}

// Artifact references a produced file or bundle.
type Artifact struct {
	ID        string
	SessionID string
	JobID     string
	Kind      string
	Path      string
	CreatedAt time.Time
}

// Event is a structured control-plane history record (in-memory only).
type Event struct {
	ID        string
	SessionID string
	JobID     string
	Type      string
	Message   string
	At        time.Time
}

// PhaseTiming records start/end/duration for one job state.
type PhaseTiming struct {
	StartedAt time.Time
	EndedAt   time.Time
	Duration  time.Duration
}

// Instrumented phase aliases (R-WP4). Recorded additively alongside JobState
// keys using the same real clock samples — never fabricated.
const (
	PhaseAcquire = "acquire"
	PhaseBuild   = "build"
	PhaseInstall = "install"
	PhaseLaunch  = "launch"
	PhaseTest    = "test"
	PhaseCapture = "capture"
)

// InstrumentedPhases is the required R-WP4 coverage set.
var InstrumentedPhases = []string{
	PhaseAcquire,
	PhaseBuild,
	PhaseInstall,
	PhaseLaunch,
	PhaseTest,
	PhaseCapture,
}

// instrumentedAlias maps job states onto the acquire/build/install/launch/test/capture vocabulary.
var instrumentedAlias = map[JobState]string{
	JobWaitingForDevice: PhaseAcquire,
	JobBuilding:         PhaseBuild,
	JobInstalling:       PhaseInstall,
	JobLaunching:        PhaseLaunch,
	JobTesting:          PhaseTest,
	JobCollecting:       PhaseCapture,
}

// JobTimings holds queue wait and per-phase timings.
type JobTimings struct {
	QueuedAt   time.Time
	DequeuedAt time.Time
	QueueWait  time.Duration
	Phases     map[string]PhaseTiming
}

// BuildGrant is an exclusive hold on one build slot.
type BuildGrant struct {
	ID        string
	JobID     string
	GrantedAt time.Time
}

// BuildStatus is the additive pool.status build-slot snapshot.
type BuildStatus struct {
	BuildSlots   int `json:"buildSlots"`
	ActiveBuilds int `json:"activeBuilds"`
	QueuedBuilds int `json:"queuedBuilds"`
}

// CreateSessionRequest configures a new in-memory Session.
type CreateSessionRequest struct {
	Project   string
	Agent     string
	Task      string
	Labels    map[string]string
	Workspace *Workspace
	WorkerID  string
}
