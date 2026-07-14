package controlplane

import (
	"context"
	"errors"
	"os"
	"reflect"
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

type blockingProvisioner struct {
	started chan broker.ProvisionRequest
	resume  chan struct{}
}

type stagedProvisioner struct {
	mu      sync.Mutex
	calls   int
	blocked chan broker.ProvisionRequest
	resume  chan struct{}
}

func (f *stagedProvisioner) Provision(_ context.Context, req broker.ProvisionRequest) (broker.Device, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	f.mu.Unlock()
	if call > 1 {
		f.blocked <- req
		<-f.resume
	}
	return broker.Device{
		UDID: "UDID-" + req.LeaseID, Name: "simurgh-clone[test]",
		Runtime: req.Spec.Runtime, Booted: true,
	}, nil
}

func (f *blockingProvisioner) Provision(_ context.Context, req broker.ProvisionRequest) (broker.Device, error) {
	f.started <- req
	<-f.resume
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

func TestJobStateTransitionMatrixExhaustive(t *testing.T) {
	states := []JobState{
		JobQueued, JobSyncing, JobPreparing, JobBuilding,
		JobWaitingForDevice, JobInstalling, JobLaunching, JobTesting,
		JobCollecting, JobSucceeded, JobFailed, JobCancelled,
		JobOrphaned, JobRetrying,
	}
	allowed := map[[2]JobState]bool{
		{JobQueued, JobSyncing}:              true,
		{JobSyncing, JobPreparing}:           true,
		{JobPreparing, JobBuilding}:          true,
		{JobBuilding, JobWaitingForDevice}:   true,
		{JobWaitingForDevice, JobInstalling}: true,
		{JobInstalling, JobLaunching}:        true,
		{JobLaunching, JobTesting}:           true,
		{JobTesting, JobCollecting}:          true,
		{JobCollecting, JobSucceeded}:        true,
		{JobFailed, JobRetrying}:             true,
		{JobRetrying, JobQueued}:             true,
	}
	for _, from := range states {
		if from == JobFailed || from == JobRetrying || from == JobSucceeded || from == JobCancelled || from == JobOrphaned {
			continue
		}
		allowed[[2]JobState{from, JobFailed}] = true
		allowed[[2]JobState{from, JobCancelled}] = true
		allowed[[2]JobState{from, JobOrphaned}] = true
	}
	for _, from := range states {
		for _, to := range states {
			want := allowed[[2]JobState{from, to}]
			if got := CanTransition(from, to); got != want {
				t.Errorf("CanTransition(%s→%s)=%v want %v", from, to, got, want)
			}
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
	if st.BuildSlots != 1 || st.ActiveBuilds != 1 || st.QueuedBuilds != 0 {
		t.Fatalf("status=%+v", st)
	}
}

func TestWaitFalseBuildQueueDoesNotConsumeFutureSlot(t *testing.T) {
	cp := newTestPlane(t, 1, 1)
	ctx := context.Background()
	s, _ := cp.CreateSession(CreateSessionRequest{Project: "p", Agent: "a"})
	holder, _ := cp.StartJob(s.ID, JobKindBuild)
	abandoned, _ := cp.StartJob(s.ID, JobKindBuild)
	waiter, _ := cp.StartJob(s.ID, JobKindBuild)

	if _, err := cp.AcquireBuildSlot(ctx, BuildSlotRequest{JobID: holder.ID, Wait: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.AcquireBuildSlot(ctx, BuildSlotRequest{JobID: abandoned.ID, Wait: false}); err == nil {
		t.Fatal("expected BUILD_QUEUED")
	}
	if got := cp.BuildStatus().QueuedBuilds; got != 0 {
		t.Fatalf("queuedBuilds=%d want 0 after Wait=false", got)
	}

	granted := make(chan BuildGrant, 1)
	errs := make(chan error, 1)
	go func() {
		g, err := cp.AcquireBuildSlot(ctx, BuildSlotRequest{JobID: waiter.ID, Wait: true})
		if err != nil {
			errs <- err
			return
		}
		granted <- g
	}()
	for deadline := time.Now().Add(time.Second); cp.BuildStatus().QueuedBuilds != 1; {
		if time.Now().After(deadline) {
			t.Fatal("Wait=true job did not enter build queue")
		}
		time.Sleep(time.Millisecond)
	}
	if err := cp.ReleaseBuildSlot(holder.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errs:
		t.Fatal(err)
	case g := <-granted:
		if g.JobID != waiter.ID {
			t.Fatalf("grant went to %s want waiter %s", g.JobID, waiter.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("real waiter was not promoted")
	}
	abandonedJob, _ := cp.GetJob(abandoned.ID)
	if abandonedJob.BuildGrantID != "" {
		t.Fatalf("Wait=false job consumed future slot: %s", abandonedJob.BuildGrantID)
	}
}

func TestBuildSlotsFromEnv(t *testing.T) {
	old, had := os.LookupEnv("SIMURGH_BUILD_SLOTS")
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("SIMURGH_BUILD_SLOTS", old)
		} else {
			_ = os.Unsetenv("SIMURGH_BUILD_SLOTS")
		}
	})
	tests := []struct {
		name    string
		value   *string
		want    int
		wantErr bool
	}{
		{name: "empty", value: nil, want: DefaultBuildSlots},
		{name: "invalid", value: ptr("nope"), wantErr: true},
		{name: "zero", value: ptr("0"), wantErr: true},
		{name: "positive", value: ptr("4"), want: 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.value == nil {
				_ = os.Unsetenv("SIMURGH_BUILD_SLOTS")
			} else {
				_ = os.Setenv("SIMURGH_BUILD_SLOTS", *tc.value)
			}
			got, err := BuildSlotsFromEnv()
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Fatalf("got=%d want=%d", got, tc.want)
			}
		})
	}
}

func ptr(s string) *string { return &s }

func TestTransitionCancelDuringDeviceAcquireCleansLease(t *testing.T) {
	prov := &blockingProvisioner{
		started: make(chan broker.ProvisionRequest, 1),
		resume:  make(chan struct{}),
	}
	b := broker.New(broker.BrokerOptions{
		Capacity: 1, Provisioner: prov, Clock: time.Now,
		PressureOK: func() (bool, string) { return true, "" },
	})
	cp, err := New(Options{BuildSlots: 1, Broker: b})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := cp.CreateSession(CreateSessionRequest{Project: "p", Agent: "a"})
	j, _ := cp.StartJob(s.ID, JobKindTest)
	for _, state := range []JobState{JobSyncing, JobPreparing, JobBuilding} {
		if _, err := cp.TransitionJob(context.Background(), j.ID, state); err != nil {
			t.Fatal(err)
		}
	}

	transitionErr := make(chan error, 1)
	go func() {
		_, err := cp.TransitionJob(context.Background(), j.ID, JobWaitingForDevice)
		transitionErr <- err
	}()
	req := <-prov.started
	cancelled, err := cp.CancelJob(context.Background(), j.ID)
	if err != nil {
		t.Fatal(err)
	}
	timingsAfterCancel := cancelled.Timings
	close(prov.resume)
	if err := <-transitionErr; err == nil {
		t.Fatal("raced transition unexpectedly committed")
	}

	got, _ := cp.GetJob(j.ID)
	if got.State != JobCancelled || got.DeviceLeaseID != "" {
		t.Fatalf("job after race=%+v", got)
	}
	if !reflect.DeepEqual(got.Timings, timingsAfterCancel) {
		t.Fatalf("failed transition mutated winner timings:\n got=%+v\nwant=%+v", got.Timings, timingsAfterCancel)
	}
	lease, ok := b.Get(req.LeaseID)
	if !ok {
		t.Fatalf("acquired lease %s missing", req.LeaseID)
	}
	if lease.State != broker.LeaseReleased {
		t.Fatalf("leaked raced lease state=%s want RELEASED", lease.State)
	}
	fresh, err := b.Acquire(context.Background(), broker.AcquireRequest{
		Spec: cp.deviceSpec, Wait: false, Owner: broker.Owner{PID: 42},
	})
	if err != nil || fresh.State != broker.LeaseActive {
		t.Fatalf("capacity not restored: lease=%+v err=%v", fresh, err)
	}
}

func TestFailCancelAndSucceedRacesPreserveTerminalWinner(t *testing.T) {
	t.Run("fail-vs-cancel", func(t *testing.T) {
		cp := newTestPlane(t, 1, 1)
		s, _ := cp.CreateSession(CreateSessionRequest{Project: "p", Agent: "a"})
		j, _ := cp.StartJob(s.ID, JobKindBuild)
		if _, err := cp.TransitionJob(context.Background(), j.ID, JobSyncing); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan Job, 2)
		errs := make(chan error, 2)
		go func() {
			<-start
			got, err := cp.FailJob(context.Background(), j.ID, FailureSource, "compile")
			results <- got
			errs <- err
		}()
		go func() {
			<-start
			got, err := cp.CancelJob(context.Background(), j.ID)
			results <- got
			errs <- err
		}()
		close(start)
		<-results
		<-results
		<-errs
		<-errs
		got, _ := cp.GetJob(j.ID)
		if got.State != JobFailed && got.State != JobCancelled {
			t.Fatalf("non-terminal winner: %s", got.State)
		}
		if got.State == JobFailed {
			if _, err := cp.CancelJob(context.Background(), j.ID); err == nil {
				t.Fatal("Failed→Cancelled must be rejected")
			}
			again, _ := cp.GetJob(j.ID)
			if again.State != JobFailed {
				t.Fatalf("cancel clobbered failed winner: %s", again.State)
			}
		} else {
			again, err := cp.FailJob(context.Background(), j.ID, FailureSource, "late")
			if err != nil || again.State != JobCancelled {
				t.Fatalf("late fail should idempotently preserve cancelled: state=%s err=%v", again.State, err)
			}
		}
	})

	t.Run("fail-vs-succeed", func(t *testing.T) {
		cp := newTestPlane(t, 1, 1)
		s, _ := cp.CreateSession(CreateSessionRequest{Project: "p", Agent: "a"})
		j, _ := cp.StartJob(s.ID, JobKindBuild)
		for _, state := range []JobState{
			JobSyncing, JobPreparing, JobBuilding, JobWaitingForDevice,
			JobInstalling, JobLaunching, JobTesting, JobCollecting,
		} {
			if _, err := cp.TransitionJob(context.Background(), j.ID, state); err != nil {
				t.Fatal(err)
			}
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, _ = cp.FailJob(context.Background(), j.ID, FailureSource, "late")
		}()
		go func() {
			defer wg.Done()
			<-start
			_, _ = cp.TransitionJob(context.Background(), j.ID, JobSucceeded)
		}()
		close(start)
		wg.Wait()
		got, _ := cp.GetJob(j.ID)
		if got.State != JobFailed && got.State != JobSucceeded {
			t.Fatalf("terminal winner clobbered: %s", got.State)
		}
	})
}

func TestCancelFailedJobIsRejectedWithoutMutation(t *testing.T) {
	cp := newTestPlane(t, 1, 1)
	s, _ := cp.CreateSession(CreateSessionRequest{Project: "p", Agent: "a"})
	j, _ := cp.StartJob(s.ID, JobKindBuild)
	if _, err := cp.TransitionJob(context.Background(), j.ID, JobSyncing); err != nil {
		t.Fatal(err)
	}
	failed, err := cp.FailJob(context.Background(), j.ID, FailureSource, "compile")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cp.CancelJob(context.Background(), j.ID); err == nil {
		t.Fatal("Failed→Cancelled must be rejected")
	}
	got, _ := cp.GetJob(j.ID)
	if !reflect.DeepEqual(got, failed) {
		t.Fatalf("cancel mutated failed job:\n got=%+v\nwant=%+v", got, failed)
	}
}

func TestTerminalClaimBeatsInstallingToLaunching(t *testing.T) {
	for _, tc := range []struct {
		name     string
		terminal JobState
	}{
		{name: "cancel", terminal: JobCancelled},
		{name: "fail", terminal: JobFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prov := &stagedProvisioner{
				blocked: make(chan broker.ProvisionRequest, 1),
				resume:  make(chan struct{}),
			}
			resumed := false
			defer func() {
				if !resumed {
					close(prov.resume)
				}
			}()
			b := broker.New(broker.BrokerOptions{
				Capacity: 1, Provisioner: prov, Clock: time.Now,
				PressureOK: func() (bool, string) { return true, "" },
			})
			cp, err := New(Options{BuildSlots: 1, Broker: b})
			if err != nil {
				t.Fatal(err)
			}
			s, _ := cp.CreateSession(CreateSessionRequest{Project: "p", Agent: "a"})
			j, _ := cp.StartJob(s.ID, JobKindTest)
			for _, state := range []JobState{
				JobSyncing, JobPreparing, JobBuilding,
				JobWaitingForDevice, JobInstalling,
			} {
				if _, err := cp.TransitionJob(context.Background(), j.ID, state); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := cp.GetJob(j.ID)
			ownedLeaseID := before.DeviceLeaseID

			// Queue a second lease so terminal cleanup blocks while broker
			// promotes/provisions it. The terminal state must already be visible.
			_, queueErr := b.Acquire(context.Background(), broker.AcquireRequest{
				Spec: cp.deviceSpec, Wait: false, Owner: broker.Owner{PID: 99},
			})
			var cq *broker.CapacityQueuedError
			if !errors.As(queueErr, &cq) {
				t.Fatalf("expected queued follower: %v", queueErr)
			}

			done := make(chan error, 1)
			go func() {
				if tc.terminal == JobCancelled {
					_, err := cp.CancelJob(context.Background(), j.ID)
					done <- err
					return
				}
				_, err := cp.FailJob(context.Background(), j.ID, FailureSource, "compile")
				done <- err
			}()
			<-prov.blocked

			claimed, _ := cp.GetJob(j.ID)
			if claimed.State != tc.terminal {
				t.Fatalf("terminal not claimed before cleanup: %s", claimed.State)
			}
			if claimed.DeviceLeaseID != ownedLeaseID {
				t.Fatalf("cleanup ownership cleared early: got %q want %q", claimed.DeviceLeaseID, ownedLeaseID)
			}
			if _, err := cp.TransitionJob(context.Background(), j.ID, JobLaunching); err == nil {
				t.Fatal("installing→launching won after terminal claim")
			}
			close(prov.resume)
			resumed = true
			if err := <-done; err != nil {
				t.Fatalf("terminal cleanup: %v", err)
			}

			got, _ := cp.GetJob(j.ID)
			if got.State != tc.terminal || got.DeviceLeaseID != "" || got.BuildGrantID != "" {
				t.Fatalf("incoherent terminal job: %+v", got)
			}
			ownedLease, ok := b.Get(ownedLeaseID)
			if !ok || ownedLease.State != broker.LeaseReleased {
				t.Fatalf("owned lease=%+v ok=%v", ownedLease, ok)
			}
			for _, l := range b.List(broker.ListFilter{}) {
				if l.State == broker.LeaseActive {
					_, _ = b.Release(context.Background(), l.ID)
				}
			}
			fresh, err := b.Acquire(context.Background(), broker.AcquireRequest{
				Spec: cp.deviceSpec, Wait: false, Owner: broker.Owner{PID: 100},
			})
			if err != nil || fresh.State != broker.LeaseActive {
				t.Fatalf("capacity incoherent: lease=%+v err=%v", fresh, err)
			}
		})
	}
}

func TestIdempotentTerminalCallRetriesRetainedCleanup(t *testing.T) {
	prov := &blockingProvisioner{
		started: make(chan broker.ProvisionRequest, 1),
		resume:  make(chan struct{}),
	}
	b := broker.New(broker.BrokerOptions{
		Capacity: 1, Provisioner: prov, Clock: time.Now,
		PressureOK: func() (bool, string) { return true, "" },
	})
	cp, err := New(Options{BuildSlots: 1, Broker: b})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := cp.CreateSession(CreateSessionRequest{Project: "p", Agent: "a"})
	j, _ := cp.StartJob(s.ID, JobKindBuild)
	if _, err := cp.TransitionJob(context.Background(), j.ID, JobSyncing); err != nil {
		t.Fatal(err)
	}

	acquired := make(chan error, 1)
	go func() {
		_, err := b.Acquire(context.Background(), broker.AcquireRequest{
			Spec: cp.deviceSpec, Wait: true, Owner: broker.Owner{PID: 7},
		})
		acquired <- err
	}()
	req := <-prov.started // lease is PROVISIONING; Release must fail
	cp.mu.Lock()
	cp.jobs[j.ID].DeviceLeaseID = req.LeaseID
	cp.jobs[j.ID].generation++
	cp.mu.Unlock()

	first, err := cp.CancelJob(context.Background(), j.ID)
	if err == nil {
		t.Fatal("expected cleanup error while lease is provisioning")
	}
	if first.State != JobCancelled || first.DeviceLeaseID != req.LeaseID {
		t.Fatalf("cleanup ownership not retained: %+v", first)
	}

	close(prov.resume)
	if err := <-acquired; err != nil {
		t.Fatal(err)
	}
	second, err := cp.CancelJob(context.Background(), j.ID)
	if err != nil {
		t.Fatalf("idempotent cleanup retry: %v", err)
	}
	if second.State != JobCancelled || second.DeviceLeaseID != "" {
		t.Fatalf("cleanup retry did not clear ownership: %+v", second)
	}
	lease, _ := b.Get(req.LeaseID)
	if lease.State != broker.LeaseReleased {
		t.Fatalf("lease state=%s want RELEASED", lease.State)
	}
}

func TestPromotedBuildWaiterCancellationReleasesGrantAndPromotesNext(t *testing.T) {
	s := newBuildScheduler(1, time.Now)
	holder := s.grantLocked("holder")
	w1 := buildWaiter{jobID: "waiter-1", enqueued: time.Now(), ch: make(chan BuildGrant, 1)}
	w2 := buildWaiter{jobID: "waiter-2", enqueued: time.Now(), ch: make(chan BuildGrant, 1)}
	s.queue = append(s.queue, w1, w2)

	if err := s.releaseOwned("holder", holder.ID); err != nil {
		t.Fatal(err)
	}
	promoted := <-w1.ch // ready is selectable
	if promoted.ID == "" {
		t.Fatal("first waiter was not promoted")
	}
	s.cancelWaiter("waiter-1") // deterministic ctx-wins branch after promotion

	select {
	case next := <-w2.ch:
		if next.ID == "" || next.JobID != "waiter-2" {
			t.Fatalf("next grant=%+v", next)
		}
		if _, ok := s.lookup(promoted.ID); ok {
			t.Fatalf("cancelled promoted grant %s stranded active", promoted.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("next waiter not promoted after promoted waiter cancellation")
	}
}

func TestBuildWaiterPromotionCancelRaceDoesNotStrandSlot(t *testing.T) {
	for i := 0; i < 100; i++ {
		s := newBuildScheduler(1, time.Now)
		holder := s.grantLocked("holder")
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			_, _, _, _, err := s.acquire(ctx, "waiter", true)
			result <- err
		}()
		for deadline := time.Now().Add(time.Second); s.status().QueuedBuilds != 1; {
			if time.Now().After(deadline) {
				t.Fatal("waiter did not queue")
			}
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_ = s.releaseOwned("holder", holder.ID)
		}()
		go func() {
			defer wg.Done()
			<-start
			cancel()
		}()
		close(start)
		wg.Wait()
		err := <-result
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if err != nil {
			if got := s.status().ActiveBuilds; got != 0 {
				t.Fatalf("iteration %d: cancelled waiter stranded %d active build", i, got)
			}
		} else {
			_ = s.release("waiter")
		}
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

func TestPhaseInstrumentationCoverage(t *testing.T) {
	clock := time.Date(2026, 7, 14, 15, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	now := clock
	cp, err := New(Options{
		BuildSlots: 1,
		Broker:     testBroker(t, 2),
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
	j, err := cp.StartJob(s.ID, JobKindTest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cp.AcquireBuildSlot(ctx, BuildSlotRequest{JobID: j.ID, Wait: false}); err != nil {
		t.Fatal(err)
	}

	// Happy-path progress covering acquire/build/install/launch/test/capture.
	sequence := []JobState{
		JobSyncing, JobPreparing, JobBuilding,
		JobWaitingForDevice, JobInstalling, JobLaunching, JobTesting, JobCollecting, JobSucceeded,
	}
	for _, next := range sequence {
		advance(100 * time.Millisecond)
		if _, err := cp.TransitionJob(ctx, j.ID, next); err != nil {
			t.Fatalf("transition %s: %v", next, err)
		}
	}

	got, err := cp.GetJob(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range InstrumentedPhases {
		pt, ok := got.Timings.Phases[name]
		if !ok {
			t.Fatalf("missing instrumented phase %q in %#v", name, got.Timings.Phases)
		}
		if pt.StartedAt.IsZero() {
			t.Fatalf("phase %q missing StartedAt: %+v", name, pt)
		}
		if pt.EndedAt.IsZero() || pt.Duration <= 0 {
			t.Fatalf("phase %q missing closed timing: %+v", name, pt)
		}
	}
	// Additive: JobState keys remain present alongside aliases.
	if _, ok := got.Timings.Phases[string(JobBuilding)]; !ok {
		t.Fatal("expected JobState building timing to remain alongside build alias")
	}
	if got.Timings.Phases[PhaseBuild].StartedAt != got.Timings.Phases[string(JobBuilding)].StartedAt {
		t.Fatal("build alias must share real StartedAt with building state timing")
	}
}
