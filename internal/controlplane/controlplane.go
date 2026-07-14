package controlplane

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
)

// Options configures New.
type Options struct {
	// BuildSlots is the host build concurrency (SIMURGH_BUILD_SLOTS). Zero
	// means DefaultBuildSlots.
	BuildSlots int
	// Broker is required for device-lease compatibility adapter paths.
	Broker *broker.Broker
	// Clock defaults to time.Now.
	Clock func() time.Time
	// DeviceSpec is the default device used when a job enters a device phase.
	DeviceSpec broker.DeviceSpec
}

// DefaultBuildSlots is used when SIMURGH_BUILD_SLOTS / Options.BuildSlots is unset.
const DefaultBuildSlots = 2

// ControlPlane is the minimal in-memory session/job scheduler API.
type ControlPlane struct {
	broker     *broker.Broker
	builds     *buildScheduler
	clock      func() time.Time
	deviceSpec broker.DeviceSpec

	mu       sync.Mutex
	sessions map[string]*Session
	jobs     map[string]*Job
	events   []Event
	idSeq    atomic.Uint64
}

// New constructs a ControlPlane. Session/job state is non-durable (see doc.go).
func New(opts Options) (*ControlPlane, error) {
	if opts.Broker == nil {
		return nil, fmt.Errorf("controlplane: Broker is required")
	}
	slots := opts.BuildSlots
	if slots <= 0 {
		slots = DefaultBuildSlots
	}
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	spec := opts.DeviceSpec
	if spec.Platform == "" {
		spec = broker.DeviceSpec{Platform: "iOS", Model: "iPhone 17 Pro", Runtime: "27.0"}
	}
	return &ControlPlane{
		broker:     opts.Broker,
		builds:     newBuildScheduler(slots, clock),
		clock:      clock,
		deviceSpec: spec,
		sessions:   make(map[string]*Session),
		jobs:       make(map[string]*Job),
	}, nil
}

// BuildSlotsFromEnv parses SIMURGH_BUILD_SLOTS. Empty → DefaultBuildSlots.
// Invalid values return an error.
func BuildSlotsFromEnv() (int, error) {
	v := os.Getenv("SIMURGH_BUILD_SLOTS")
	if v == "" {
		return DefaultBuildSlots, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("SIMURGH_BUILD_SLOTS must be a positive integer, got %q", v)
	}
	return n, nil
}

// Broker returns the underlying device lease broker (compatibility adapter).
func (cp *ControlPlane) Broker() *broker.Broker { return cp.broker }

// BuildStatus returns additive pool.status build-slot fields.
func (cp *ControlPlane) BuildStatus() BuildStatus { return cp.builds.status() }

// DeviceLeaseRequest mirrors broker.AcquireRequest for the compatibility path.
// lease.acquire never routes through the build scheduler.
type DeviceLeaseRequest struct {
	Spec          broker.DeviceSpec
	TTL           time.Duration
	Wait          bool
	Owner         broker.Owner
	Labels        map[string]string
	ReuseIfExists bool
	SessionID     string // optional control-plane session to attach the lease to
}

// AcquireDeviceLease is the compatibility adapter over broker.Acquire.
// It does not consult build slots and never returns BUILD_QUEUED / build_slot.
func (cp *ControlPlane) AcquireDeviceLease(ctx context.Context, req DeviceLeaseRequest) (broker.Lease, error) {
	lease, err := cp.broker.Acquire(ctx, broker.AcquireRequest{
		Spec:          req.Spec,
		TTL:           req.TTL,
		Wait:          req.Wait,
		Owner:         req.Owner,
		Labels:        req.Labels,
		ReuseIfExists: req.ReuseIfExists,
	})
	if err != nil {
		return broker.Lease{}, err
	}
	if req.SessionID != "" {
		cp.mu.Lock()
		if s, ok := cp.sessions[req.SessionID]; ok {
			s.LeaseIDs = appendUnique(s.LeaseIDs, lease.ID)
		}
		cp.mu.Unlock()
	}
	return lease, nil
}

// CreateSession allocates an in-memory Session.
func (cp *ControlPlane) CreateSession(req CreateSessionRequest) (Session, error) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	id := fmt.Sprintf("sess-%d", cp.idSeq.Add(1))
	now := cp.clock()
	s := &Session{
		ID:        id,
		Project:   req.Project,
		Agent:     req.Agent,
		Task:      req.Task,
		Labels:    cloneLabels(req.Labels),
		Workspace: req.Workspace,
		WorkerID:  req.WorkerID,
		CreatedAt: now,
	}
	cp.sessions[id] = s
	cp.events = append(cp.events, Event{
		ID:        fmt.Sprintf("evt-%d", cp.idSeq.Add(1)),
		SessionID: id,
		Type:      "session.created",
		Message:   "session created",
		At:        now,
	})
	return cloneSession(s), nil
}

// StartJob creates a Job in queued state under a Session.
func (cp *ControlPlane) StartJob(sessionID string, kind JobKind) (Job, error) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	s, ok := cp.sessions[sessionID]
	if !ok {
		return Job{}, &NotFoundError{Kind: "session", ID: sessionID}
	}
	id := fmt.Sprintf("job-%d", cp.idSeq.Add(1))
	now := cp.clock()
	j := &Job{
		ID:         id,
		SessionID:  sessionID,
		Kind:       kind,
		State:      JobQueued,
		WorkerID:   s.WorkerID,
		Timings:    JobTimings{Phases: make(map[string]PhaseTiming)},
		CreatedAt:  now,
		UpdatedAt:  now,
		generation: 1,
	}
	j.Timings.Phases[string(JobQueued)] = PhaseTiming{StartedAt: now}
	cp.jobs[id] = j
	s.JobIDs = append(s.JobIDs, id)
	return cloneJob(j), nil
}

// GetJob returns a copy of the job.
func (cp *ControlPlane) GetJob(jobID string) (Job, error) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	j, ok := cp.jobs[jobID]
	if !ok {
		return Job{}, &NotFoundError{Kind: "job", ID: jobID}
	}
	return cloneJob(j), nil
}

// BuildSlotRequest asks for an independent build slot for a Job.
type BuildSlotRequest struct {
	JobID string
	Wait  bool
}

// AcquireBuildSlot grants or queues a build_slot for the job.
func (cp *ControlPlane) AcquireBuildSlot(ctx context.Context, req BuildSlotRequest) (BuildGrant, error) {
	cp.mu.Lock()
	j, ok := cp.jobs[req.JobID]
	if !ok {
		cp.mu.Unlock()
		return BuildGrant{}, &NotFoundError{Kind: "job", ID: req.JobID}
	}
	generation := j.generation
	cp.mu.Unlock()

	grant, queuedAt, dequeuedAt, queueWait, err := cp.builds.acquire(ctx, req.JobID, req.Wait)
	if err != nil {
		if queuedAt.IsZero() {
			return BuildGrant{}, err
		}
		// Still record queue start for Wait=false metadata.
		cp.mu.Lock()
		if j2, ok := cp.jobs[req.JobID]; ok && j2.Timings.QueuedAt.IsZero() {
			j2.Timings.QueuedAt = queuedAt
		}
		cp.mu.Unlock()
		return BuildGrant{}, err
	}

	cp.mu.Lock()
	defer cp.mu.Unlock()
	j, ok = cp.jobs[req.JobID]
	if !ok {
		_ = cp.builds.releaseOwned(req.JobID, grant.ID)
		return BuildGrant{}, &NotFoundError{Kind: "job", ID: req.JobID}
	}
	if j.generation != generation || terminalStates[j.State] {
		_ = cp.builds.releaseOwned(req.JobID, grant.ID)
		return BuildGrant{}, &InvalidJobStateError{
			JobID: req.JobID, From: j.State, To: j.State, Op: "commit build grant (raced)",
		}
	}
	j.BuildGrantID = grant.ID
	j.UpdatedAt = cp.clock()
	j.generation++
	if !queuedAt.IsZero() {
		j.Timings.QueuedAt = queuedAt
		j.Timings.DequeuedAt = dequeuedAt
		j.Timings.QueueWait = queueWait
	}
	return grant, nil
}

// ReleaseBuildSlot frees the job's build grant (and any build queue entry).
func (cp *ControlPlane) ReleaseBuildSlot(jobID string) error {
	if err := cp.builds.release(jobID); err != nil {
		return err
	}
	cp.mu.Lock()
	defer cp.mu.Unlock()
	if j, ok := cp.jobs[jobID]; ok {
		j.BuildGrantID = ""
		j.UpdatedAt = cp.clock()
		j.generation++
	}
	return nil
}

func (cp *ControlPlane) lookupBuildGrant(grantID string) (*BuildGrant, bool) {
	return cp.builds.lookup(grantID)
}

// TransitionJob applies a legal FSM transition, acquiring/releasing device
// leases only around device phases, and recording phase timings.
func (cp *ControlPlane) TransitionJob(ctx context.Context, jobID string, to JobState) (Job, error) {
	cp.mu.Lock()
	j, ok := cp.jobs[jobID]
	if !ok {
		cp.mu.Unlock()
		return Job{}, &NotFoundError{Kind: "job", ID: jobID}
	}
	from := j.State
	if !CanTransition(from, to) {
		cp.mu.Unlock()
		return Job{}, &InvalidJobStateError{JobID: jobID, From: from, To: to, Op: "transition"}
	}
	generation := j.generation
	needDevice := HoldsDevice(to) && !HoldsDevice(from)
	dropDevice := HoldsDevice(from) && !HoldsDevice(to)
	leaseID := j.DeviceLeaseID
	buildGrantID := j.BuildGrantID
	sessionID := j.SessionID
	cp.mu.Unlock()

	acquiredLeaseID := ""
	if needDevice {
		lease, err := cp.broker.Acquire(ctx, broker.AcquireRequest{
			Spec:  cp.deviceSpec,
			TTL:   time.Hour,
			Wait:  true,
			Owner: broker.Owner{PID: os.Getpid(), Agent: "controlplane", SessionID: sessionID},
			Labels: map[string]string{
				"project": "controlplane",
				"job":     jobID,
			},
		})
		if err != nil {
			return Job{}, err
		}
		leaseID = lease.ID
		acquiredLeaseID = lease.ID
	}

	if dropDevice && leaseID != "" {
		if _, err := cp.broker.Release(ctx, leaseID); err != nil {
			return Job{}, err
		}
		leaseID = ""
	}

	// Release build slot when leaving building toward device/collect phases.
	if from == JobBuilding && to != JobBuilding {
		_ = cp.builds.releaseOwned(jobID, buildGrantID)
	}

	cp.mu.Lock()
	j, ok = cp.jobs[jobID]
	if !ok {
		cp.mu.Unlock()
		if acquiredLeaseID != "" {
			_, _ = cp.broker.Release(context.Background(), acquiredLeaseID)
		}
		return Job{}, &NotFoundError{Kind: "job", ID: jobID}
	}
	// Commit only if no state/resource mutation won while external work ran.
	if j.State != from || j.generation != generation {
		racedFrom := j.State
		cp.mu.Unlock()
		if acquiredLeaseID != "" {
			if _, cleanupErr := cp.broker.Release(context.Background(), acquiredLeaseID); cleanupErr != nil {
				return Job{}, fmt.Errorf("controlplane: clean raced device lease %q: %w", acquiredLeaseID, cleanupErr)
			}
		}
		return Job{}, &InvalidJobStateError{JobID: jobID, From: racedFrom, To: to, Op: "transition (raced)"}
	}
	now := cp.clock()
	cp.endPhaseLocked(j, from, now)
	j.State = to
	j.UpdatedAt = now
	j.generation++
	if needDevice {
		j.DeviceLeaseID = leaseID
		if s, ok := cp.sessions[sessionID]; ok {
			s.LeaseIDs = appendUnique(s.LeaseIDs, leaseID)
		}
	}
	if dropDevice {
		j.DeviceLeaseID = ""
	}
	if from == JobBuilding {
		j.BuildGrantID = ""
	}
	pt := j.Timings.Phases[string(to)]
	if pt.StartedAt.IsZero() {
		pt.StartedAt = now
	}
	j.Timings.Phases[string(to)] = pt
	cp.recordInstrumentedAliasLocked(j, to, pt)
	out := cloneJob(j)
	cp.mu.Unlock()
	return out, nil
}

// FailJob transitions to failed with retry classification.
func (cp *ControlPlane) FailJob(ctx context.Context, jobID string, class FailureClass, detail string) (Job, error) {
	cp.mu.Lock()
	j, ok := cp.jobs[jobID]
	if !ok {
		cp.mu.Unlock()
		return Job{}, &NotFoundError{Kind: "job", ID: jobID}
	}
	from := j.State
	if from == JobFailed {
		generation := j.generation
		leaseID, buildGrantID := j.DeviceLeaseID, j.BuildGrantID
		cp.mu.Unlock()
		return cp.cleanupTerminal(ctx, jobID, JobFailed, generation, buildGrantID, leaseID)
	}
	if terminalStates[from] {
		out := cloneJob(j)
		cp.mu.Unlock()
		return out, nil
	}
	if !CanTransition(from, JobFailed) {
		cp.mu.Unlock()
		return Job{}, &InvalidJobStateError{JobID: jobID, From: from, To: JobFailed, Op: "fail"}
	}
	now := cp.clock()
	cp.endPhaseLocked(j, from, now)
	j.State = JobFailed
	j.FailureClass = class
	j.RetryClass = ClassifyRetry(class)
	j.FailureDetail = detail
	j.UpdatedAt = now
	j.generation++
	pt := j.Timings.Phases[string(JobFailed)]
	pt.StartedAt = now
	pt.EndedAt = now
	j.Timings.Phases[string(JobFailed)] = pt
	terminalGeneration := j.generation
	leaseID := j.DeviceLeaseID
	buildGrantID := j.BuildGrantID
	cp.mu.Unlock()

	return cp.cleanupTerminal(ctx, jobID, JobFailed, terminalGeneration, buildGrantID, leaseID)
}

// CancelJob cancels a job and releases only resources owned by that job.
// Concurrent cancel with transitions is race-safe via job-state checks.
func (cp *ControlPlane) CancelJob(ctx context.Context, jobID string) (Job, error) {
	cp.mu.Lock()
	j, ok := cp.jobs[jobID]
	if !ok {
		cp.mu.Unlock()
		return Job{}, &NotFoundError{Kind: "job", ID: jobID}
	}
	if j.State == JobCancelled {
		generation := j.generation
		leaseID, buildGrantID := j.DeviceLeaseID, j.BuildGrantID
		cp.mu.Unlock()
		return cp.cleanupTerminal(ctx, jobID, JobCancelled, generation, buildGrantID, leaseID)
	}
	if !CanTransition(j.State, JobCancelled) {
		cp.mu.Unlock()
		return Job{}, &InvalidJobStateError{JobID: jobID, From: j.State, To: JobCancelled, Op: "cancel"}
	}
	from := j.State
	now := cp.clock()
	cp.endPhaseLocked(j, from, now)
	j.State = JobCancelled
	j.FailureClass = FailureCancelled
	j.RetryClass = RetryUnsafe
	j.UpdatedAt = now
	j.generation++
	pt := j.Timings.Phases[string(JobCancelled)]
	pt.StartedAt = now
	pt.EndedAt = now
	j.Timings.Phases[string(JobCancelled)] = pt
	terminalGeneration := j.generation
	leaseID := j.DeviceLeaseID
	buildGrantID := j.BuildGrantID
	cp.mu.Unlock()

	return cp.cleanupTerminal(ctx, jobID, JobCancelled, terminalGeneration, buildGrantID, leaseID)
}

// cleanupTerminal releases only resources snapshotted by the terminal claim.
// IDs remain on the Job until their exact release succeeds, allowing an
// idempotent terminal call to retry retained cleanup ownership.
func (cp *ControlPlane) cleanupTerminal(
	ctx context.Context,
	jobID string,
	terminal JobState,
	terminalGeneration uint64,
	buildGrantID, leaseID string,
) (Job, error) {
	cp.builds.cancelWaiter(jobID)
	buildReleased := buildGrantID == ""
	var cleanupErrs []error
	if buildGrantID != "" {
		if err := cp.builds.releaseOwned(jobID, buildGrantID); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("release build grant %q: %w", buildGrantID, err))
		} else {
			buildReleased = true
		}
	}
	deviceReleased := leaseID == ""
	if leaseID != "" {
		if _, err := cp.broker.Release(ctx, leaseID); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("release device lease %q: %w", leaseID, err))
		} else {
			deviceReleased = true
		}
	}

	cp.mu.Lock()
	defer cp.mu.Unlock()
	j, ok := cp.jobs[jobID]
	if !ok {
		return Job{}, &NotFoundError{Kind: "job", ID: jobID}
	}
	if j.State == terminal && j.generation == terminalGeneration {
		cleared := false
		if buildReleased && buildGrantID != "" && j.BuildGrantID == buildGrantID {
			j.BuildGrantID = ""
			cleared = true
		}
		if deviceReleased && leaseID != "" && j.DeviceLeaseID == leaseID {
			j.DeviceLeaseID = ""
			cleared = true
		}
		if cleared {
			j.UpdatedAt = cp.clock()
			j.generation++
		}
	}
	return cloneJob(j), errors.Join(cleanupErrs...)
}

func (cp *ControlPlane) endPhaseLocked(j *Job, phase JobState, now time.Time) {
	pt := j.Timings.Phases[string(phase)]
	if pt.StartedAt.IsZero() {
		return
	}
	if pt.EndedAt.IsZero() {
		pt.EndedAt = now
		pt.Duration = now.Sub(pt.StartedAt)
		j.Timings.Phases[string(phase)] = pt
		cp.recordInstrumentedAliasLocked(j, phase, pt)
	}
}

// recordInstrumentedAliasLocked mirrors a JobState timing under the R-WP4
// acquire/build/install/launch/test/capture alias when one applies.
func (cp *ControlPlane) recordInstrumentedAliasLocked(j *Job, phase JobState, pt PhaseTiming) {
	alias, ok := instrumentedAlias[phase]
	if !ok || alias == "" {
		return
	}
	j.Timings.Phases[alias] = pt
}

func cloneSession(s *Session) Session {
	out := *s
	out.Labels = cloneLabels(s.Labels)
	out.JobIDs = append([]string(nil), s.JobIDs...)
	out.LeaseIDs = append([]string(nil), s.LeaseIDs...)
	out.ArtifactIDs = append([]string(nil), s.ArtifactIDs...)
	if s.Workspace != nil {
		ws := *s.Workspace
		out.Workspace = &ws
	}
	return out
}

func cloneJob(j *Job) Job {
	out := *j
	out.ArtifactIDs = append([]string(nil), j.ArtifactIDs...)
	out.Timings.Phases = make(map[string]PhaseTiming, len(j.Timings.Phases))
	for k, v := range j.Timings.Phases {
		out.Timings.Phases[k] = v
	}
	return out
}

func cloneLabels(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func appendUnique(in []string, id string) []string {
	for _, x := range in {
		if x == id {
			return in
		}
	}
	return append(in, id)
}
