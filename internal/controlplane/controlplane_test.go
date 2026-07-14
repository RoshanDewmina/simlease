package controlplane

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
)

func testBroker(t *testing.T, capacity int) *broker.Broker {
	t.Helper()
	return broker.New(broker.BrokerOptions{
		Capacity:    capacity,
		Provisioner: &fakeProvisioner{},
		Clock:       time.Now,
		PressureOK:  func() (bool, string) { return true, "" },
	})
}

type fakeProvisioner struct {
	mu sync.Mutex
	n  int
}

func (f *fakeProvisioner) Provision(_ context.Context, req broker.ProvisionRequest) (broker.Device, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	return broker.Device{
		UDID:    "UDID-" + req.LeaseID,
		Name:    "simurgh-clone[test]",
		Runtime: req.Spec.Runtime,
		Booted:  true,
	}, nil
}

func newTestPlane(t *testing.T, buildSlots, simCapacity int) *ControlPlane {
	t.Helper()
	b := testBroker(t, simCapacity)
	cp, err := New(Options{
		BuildSlots: buildSlots,
		Broker:     b,
		Clock:      time.Now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return cp
}

func TestBuildProceedsWhileSimulatorFull(t *testing.T) {
	cp := newTestPlane(t, 2, 1)
	ctx := context.Background()

	// Saturate the simulator pool.
	lease, err := cp.AcquireDeviceLease(ctx, DeviceLeaseRequest{
		Spec:  broker.DeviceSpec{Platform: "iOS", Model: "iPhone 17 Pro", Runtime: "27.0"},
		TTL:   time.Hour,
		Wait:  true,
		Owner: broker.Owner{PID: 1, Agent: "a", SessionID: "s1"},
	})
	if err != nil {
		t.Fatalf("device acquire: %v", err)
	}
	if lease.State != broker.LeaseActive {
		t.Fatalf("lease state=%s", lease.State)
	}

	// Second device request must queue (simulator full).
	_, err = cp.AcquireDeviceLease(ctx, DeviceLeaseRequest{
		Spec:  broker.DeviceSpec{Platform: "iOS", Model: "iPhone 17 Pro", Runtime: "27.0"},
		TTL:   time.Hour,
		Wait:  false,
		Owner: broker.Owner{PID: 2, Agent: "a", SessionID: "s2"},
	})
	var cq *broker.CapacityQueuedError
	if !errors.As(err, &cq) {
		t.Fatalf("expected CapacityQueuedError, got %v", err)
	}
	if cq.ResourceClass != broker.QueueReasonSimulatorSlot {
		t.Fatalf("device queue resourceClass=%q", cq.ResourceClass)
	}

	// Build slot is independent — must still grant while simulator is full.
	sess, err := cp.CreateSession(CreateSessionRequest{
		Project: "demo",
		Agent:   "agent-a",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	job, err := cp.StartJob(sess.ID, JobKindBuild)
	if err != nil {
		t.Fatalf("StartJob: %v", err)
	}
	grant, err := cp.AcquireBuildSlot(ctx, BuildSlotRequest{JobID: job.ID, Wait: false})
	if err != nil {
		t.Fatalf("AcquireBuildSlot while simulator full: %v", err)
	}
	if grant.JobID != job.ID {
		t.Fatalf("grant job=%s want %s", grant.JobID, job.ID)
	}
	st := cp.BuildStatus()
	if st.BuildSlots != 2 || st.ActiveBuilds != 1 || st.QueuedBuilds != 0 {
		t.Fatalf("build status=%+v", st)
	}
}

func TestDeviceHeldOnlyDuringDevicePhase(t *testing.T) {
	cp := newTestPlane(t, 1, 1)
	ctx := context.Background()

	sess, err := cp.CreateSession(CreateSessionRequest{Project: "demo", Agent: "a"})
	if err != nil {
		t.Fatal(err)
	}
	job, err := cp.StartJob(sess.ID, JobKindTest)
	if err != nil {
		t.Fatal(err)
	}

	// Drive through pre-device phases — no device lease yet.
	for _, next := range []JobState{JobSyncing, JobPreparing, JobBuilding} {
		if _, err := cp.TransitionJob(ctx, job.ID, next); err != nil {
			t.Fatalf("transition to %s: %v", next, err)
		}
		got, err := cp.GetJob(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.DeviceLeaseID != "" {
			t.Fatalf("device held in %s: lease=%s", next, got.DeviceLeaseID)
		}
	}

	// Entering waiting_for_device acquires a simulator lease.
	got, err := cp.TransitionJob(ctx, job.ID, JobWaitingForDevice)
	if err != nil {
		t.Fatalf("waiting_for_device: %v", err)
	}
	if got.DeviceLeaseID == "" {
		t.Fatal("expected device lease during waiting_for_device")
	}
	leaseID := got.DeviceLeaseID

	for _, next := range []JobState{JobInstalling, JobLaunching, JobTesting} {
		got, err = cp.TransitionJob(ctx, job.ID, next)
		if err != nil {
			t.Fatalf("transition to %s: %v", next, err)
		}
		if got.DeviceLeaseID != leaseID {
			t.Fatalf("device lease changed in %s: %q vs %q", next, got.DeviceLeaseID, leaseID)
		}
	}

	// Leaving testing into collecting releases the simulator.
	got, err = cp.TransitionJob(ctx, job.ID, JobCollecting)
	if err != nil {
		t.Fatalf("collecting: %v", err)
	}
	if got.DeviceLeaseID != "" {
		t.Fatalf("device still held in collecting: %s", got.DeviceLeaseID)
	}
	l, ok := cp.Broker().Get(leaseID)
	if !ok {
		t.Fatal("expected released lease to remain in broker map")
	}
	if l.State == broker.LeaseActive || l.State == broker.LeaseProvisioning {
		t.Fatalf("released lease still occupying capacity: state=%s", l.State)
	}
}

func TestCancelReleasesOnlyOwnedResources(t *testing.T) {
	cp := newTestPlane(t, 2, 2)
	ctx := context.Background()

	s1, err := cp.CreateSession(CreateSessionRequest{Project: "p", Agent: "a"})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := cp.CreateSession(CreateSessionRequest{Project: "p", Agent: "b"})
	if err != nil {
		t.Fatal(err)
	}
	j1, err := cp.StartJob(s1.ID, JobKindBuild)
	if err != nil {
		t.Fatal(err)
	}
	j2, err := cp.StartJob(s2.ID, JobKindBuild)
	if err != nil {
		t.Fatal(err)
	}

	g1, err := cp.AcquireBuildSlot(ctx, BuildSlotRequest{JobID: j1.ID, Wait: true})
	if err != nil {
		t.Fatal(err)
	}
	g2, err := cp.AcquireBuildSlot(ctx, BuildSlotRequest{JobID: j2.ID, Wait: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cp.TransitionJob(ctx, j1.ID, JobSyncing); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.TransitionJob(ctx, j1.ID, JobPreparing); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.TransitionJob(ctx, j1.ID, JobBuilding); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.TransitionJob(ctx, j1.ID, JobWaitingForDevice); err != nil {
		t.Fatal(err)
	}
	j1got, _ := cp.GetJob(j1.ID)
	if j1got.DeviceLeaseID == "" {
		t.Fatal("j1 missing device")
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := cp.CancelJob(ctx, j1.ID); err != nil {
			t.Errorf("CancelJob j1: %v", err)
		}
	}()
	wg.Wait()

	j1got, err = cp.GetJob(j1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j1got.State != JobCancelled {
		t.Fatalf("j1 state=%s", j1got.State)
	}
	if j1got.DeviceLeaseID != "" || j1got.BuildGrantID != "" {
		t.Fatalf("j1 still holds resources: device=%q build=%q", j1got.DeviceLeaseID, j1got.BuildGrantID)
	}
	if g1.ID != "" {
		if _, ok := cp.lookupBuildGrant(g1.ID); ok {
			t.Fatal("j1 build grant still active")
		}
	}

	j2got, err := cp.GetJob(j2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j2got.State == JobCancelled {
		t.Fatal("j2 was cancelled by j1 cancel")
	}
	if j2got.BuildGrantID != g2.ID {
		t.Fatalf("j2 lost build grant: %q vs %q", j2got.BuildGrantID, g2.ID)
	}
	st := cp.BuildStatus()
	if st.ActiveBuilds != 1 {
		t.Fatalf("activeBuilds=%d want 1 (only j2)", st.ActiveBuilds)
	}
}

func TestJobStateTransitionsRejectInvalid(t *testing.T) {
	cp := newTestPlane(t, 1, 1)
	ctx := context.Background()
	sess, err := cp.CreateSession(CreateSessionRequest{Project: "p", Agent: "a"})
	if err != nil {
		t.Fatal(err)
	}
	job, err := cp.StartJob(sess.ID, JobKindBuild)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != JobQueued {
		t.Fatalf("start state=%s", job.State)
	}

	// Skip ahead is illegal.
	if _, err := cp.TransitionJob(ctx, job.ID, JobBuilding); err == nil {
		t.Fatal("expected reject queued→building")
	}
	// Backward is illegal.
	if _, err := cp.TransitionJob(ctx, job.ID, JobSyncing); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.TransitionJob(ctx, job.ID, JobQueued); err == nil {
		t.Fatal("expected reject syncing→queued")
	}

	// Terminal states reject further progress transitions.
	if _, err := cp.TransitionJob(ctx, job.ID, JobFailed); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.TransitionJob(ctx, job.ID, JobSucceeded); err == nil {
		t.Fatal("expected reject failed→succeeded")
	}

	// Table of clearly illegal pairs.
	illegal := []struct{ from, to JobState }{
		{JobPreparing, JobTesting},
		{JobBuilding, JobSucceeded},
		{JobSucceeded, JobBuilding},
		{JobCancelled, JobRetrying},
		{JobOrphaned, JobQueued},
	}
	for _, tc := range illegal {
		if CanTransition(tc.from, tc.to) {
			t.Fatalf("CanTransition(%s→%s) = true, want false", tc.from, tc.to)
		}
	}
}

func TestBuildSlotQueueMetadata(t *testing.T) {
	cp := newTestPlane(t, 1, 2)
	ctx := context.Background()
	s, err := cp.CreateSession(CreateSessionRequest{Project: "p", Agent: "a"})
	if err != nil {
		t.Fatal(err)
	}
	j1, err := cp.StartJob(s.ID, JobKindBuild)
	if err != nil {
		t.Fatal(err)
	}
	j2, err := cp.StartJob(s.ID, JobKindBuild)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cp.AcquireBuildSlot(ctx, BuildSlotRequest{JobID: j1.ID, Wait: false}); err != nil {
		t.Fatal(err)
	}
	_, err = cp.AcquireBuildSlot(ctx, BuildSlotRequest{JobID: j2.ID, Wait: false})
	var bq *BuildQueuedError
	if !errors.As(err, &bq) {
		t.Fatalf("expected BuildQueuedError, got %v", err)
	}
	if bq.ResourceClass != ResourceClassBuildSlot || bq.Reason != ResourceClassBuildSlot {
		t.Fatalf("reason/class=%q/%q want build_slot", bq.Reason, bq.ResourceClass)
	}
	if bq.Position < 1 {
		t.Fatalf("position=%d", bq.Position)
	}
	if bq.ErrorEnvelope == nil || bq.ErrorEnvelope.Code != CodeBuildQueued {
		t.Fatalf("envelope=%+v", bq.ErrorEnvelope)
	}
	st := cp.BuildStatus()
	if st.BuildSlots != 1 || st.ActiveBuilds != 1 || st.QueuedBuilds != 1 {
		t.Fatalf("status=%+v", st)
	}
}

func TestJobRetryClassification(t *testing.T) {
	cases := []struct {
		name string
		in   FailureClass
		want RetryClass
	}{
		{"infra capacity", FailureInfrastructure, RetrySafe},
		{"infra pressure", FailureInfrastructure, RetrySafe},
		{"source compile", FailureSource, RetryUnsafe},
		{"test assertion", FailureSource, RetryUnsafe},
		{"unknown", FailureUnknown, RetryUnknown},
		{"cancel", FailureCancelled, RetryUnsafe},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyRetry(tc.in)
			if got != tc.want {
				t.Fatalf("ClassifyRetry(%v)=%v want %v", tc.in, got, tc.want)
			}
		})
	}

	cp := newTestPlane(t, 1, 1)
	ctx := context.Background()
	s, _ := cp.CreateSession(CreateSessionRequest{Project: "p", Agent: "a"})
	j, _ := cp.StartJob(s.ID, JobKindBuild)
	if _, err := cp.TransitionJob(ctx, j.ID, JobSyncing); err != nil {
		t.Fatal(err)
	}
	got, err := cp.FailJob(ctx, j.ID, FailureInfrastructure, "provisioner timeout")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != JobFailed {
		t.Fatalf("state=%s", got.State)
	}
	if got.RetryClass != RetrySafe {
		t.Fatalf("retryClass=%s want safe", got.RetryClass)
	}
	retried, err := cp.TransitionJob(ctx, j.ID, JobRetrying)
	if err != nil {
		t.Fatal(err)
	}
	if retried.State != JobRetrying {
		t.Fatalf("state=%s", retried.State)
	}
}

func TestPhaseAndQueueTimings(t *testing.T) {
	clock := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	now := clock
	cp, err := New(Options{
		BuildSlots: 1,
		Broker:     testBroker(t, 1),
		Clock: func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return now
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	advance := func(d time.Duration) {
		mu.Lock()
		now = now.Add(d)
		mu.Unlock()
	}

	ctx := context.Background()
	s, err := cp.CreateSession(CreateSessionRequest{Project: "p", Agent: "a"})
	if err != nil {
		t.Fatal(err)
	}
	j1, err := cp.StartJob(s.ID, JobKindBuild)
	if err != nil {
		t.Fatal(err)
	}
	j2, err := cp.StartJob(s.ID, JobKindBuild)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := cp.AcquireBuildSlot(ctx, BuildSlotRequest{JobID: j1.ID, Wait: false}); err != nil {
		t.Fatal(err)
	}

	// j2 waits on the build queue; grant after j1 releases.
	var wg sync.WaitGroup
	wg.Add(1)
	var grant2 BuildGrant
	var acquireErr error
	go func() {
		defer wg.Done()
		grant2, acquireErr = cp.AcquireBuildSlot(ctx, BuildSlotRequest{JobID: j2.ID, Wait: true})
	}()
	// Let waiter enqueue.
	time.Sleep(20 * time.Millisecond)
	advance(1500 * time.Millisecond)
	if err := cp.ReleaseBuildSlot(j1.ID); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if acquireErr != nil {
		t.Fatalf("waiter: %v", acquireErr)
	}
	j2got, err := cp.GetJob(j2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j2got.Timings.QueueWait <= 0 {
		t.Fatalf("QueueWait=%v want >0; grant=%+v", j2got.Timings.QueueWait, grant2)
	}
	if j2got.Timings.QueuedAt.IsZero() || j2got.Timings.DequeuedAt.IsZero() {
		t.Fatalf("queue timestamps missing: %+v", j2got.Timings)
	}

	// Phase timings on j2.
	for _, next := range []JobState{JobSyncing, JobPreparing, JobBuilding} {
		advance(100 * time.Millisecond)
		if _, err := cp.TransitionJob(ctx, j2.ID, next); err != nil {
			t.Fatalf("%s: %v", next, err)
		}
	}
	j2got, _ = cp.GetJob(j2.ID)
	syncPhase := j2got.Timings.Phases[string(JobSyncing)]
	if syncPhase.StartedAt.IsZero() || syncPhase.EndedAt.IsZero() || syncPhase.Duration <= 0 {
		t.Fatalf("syncing phase timings=%+v", syncPhase)
	}
	prepPhase := j2got.Timings.Phases[string(JobPreparing)]
	if prepPhase.StartedAt.IsZero() || prepPhase.Duration <= 0 {
		// preparing may still be open until next transition ends it — building started ends preparing
		t.Fatalf("preparing phase timings=%+v", prepPhase)
	}
	buildPhase := j2got.Timings.Phases[string(JobBuilding)]
	if buildPhase.StartedAt.IsZero() {
		t.Fatalf("building phase missing start: %+v", buildPhase)
	}
	if buildPhase.EndedAt.IsZero() && j2got.State == JobBuilding {
		// still in building — end not required yet
	} else if j2got.State != JobBuilding && buildPhase.Duration < 0 {
		t.Fatalf("building duration=%v", buildPhase.Duration)
	}
}
