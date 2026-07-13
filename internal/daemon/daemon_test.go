package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
	"github.com/RoshanDewmina/simurgh/internal/ipc"
	"github.com/RoshanDewmina/simurgh/internal/reaper"
	"github.com/RoshanDewmina/simurgh/internal/safety"
)

func testSpec() broker.DeviceSpec {
	return broker.DeviceSpec{Platform: "iOS", Model: "iPhone 17 Pro", Runtime: "27.0"}
}

type fakeProvisioner struct {
	n     atomic.Int64
	delay time.Duration
}

func (f *fakeProvisioner) Provision(ctx context.Context, req broker.ProvisionRequest) (broker.Device, error) {
	_ = ctx
	_ = req
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	id := f.n.Add(1)
	return broker.Device{
		UDID:   fmt.Sprintf("UDID-%d", id),
		Name:   fmt.Sprintf("simurgh-clone[lease-%d:iPhone 17 Pro:27.0]", id),
		Booted: true,
	}, nil
}

type fakeLister struct {
	mu      sync.Mutex
	devices []safety.Device
}

func (f *fakeLister) ListDevices(ctx context.Context) ([]safety.Device, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]safety.Device, len(f.devices))
	copy(out, f.devices)
	return out, nil
}

type fakeDeleter struct {
	mu       sync.Mutex
	calls    []string
	failOnce atomic.Bool
}

func (f *fakeDeleter) Shutdown(ctx context.Context, udid string) error {
	_ = ctx
	_ = udid
	return nil
}

func (f *fakeDeleter) Delete(ctx context.Context, udid string) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, udid)
	if f.failOnce.CompareAndSwap(true, false) {
		return errors.New("injected delete failure")
	}
	return nil
}

func (f *fakeDeleter) deleted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

func newTestDeps(t *testing.T, capacity int) (Deps, *broker.Broker, *fakeDeleter) {
	t.Helper()
	b := broker.New(broker.BrokerOptions{
		Capacity:    capacity,
		Provisioner: &fakeProvisioner{},
		Clock:       time.Now,
		PressureOK:  func() (bool, string) { return true, "" },
	})
	deleter := &fakeDeleter{}
	r := reaper.New(reaper.ReaperOptions{
		LeasesRoot: t.TempDir(),
		Lister:     &fakeLister{},
		Deleter:    deleter,
		StartedAt:  func() time.Time { return time.Now().Add(-time.Minute) },
		Now:        time.Now,
	})
	return Deps{Broker: b, Reaper: r, LeasesRoot: t.TempDir()}, b, deleter
}

func callHandler(t *testing.T, h ipc.HandlerFunc, id string, params any) ipc.Response {
	t.Helper()
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal params: %v", err)
		}
		raw = b
	}
	result, herr := h(context.Background(), ipc.Request{
		V: ipc.ProtocolVersion, ID: id, Method: "test", Params: raw,
	})
	if herr != nil {
		return ipc.Response{V: ipc.ProtocolVersion, ID: id, OK: false, Error: herr}
	}
	return ipc.Response{V: ipc.ProtocolVersion, ID: id, OK: true, Result: result}
}

func TestAcquireReturnsLeaseJSON(t *testing.T) {
	deps, _, _ := newTestDeps(t, 2)
	h := newHandlers(deps)

	resp := callHandler(t, h.acquire, "r1", map[string]any{
		"spec":  map[string]string{"platform": "iOS", "model": "iPhone 17 Pro"},
		"ttl":   "30m",
		"wait":  true,
		"owner": map[string]any{"pid": 4242, "agent": "test"},
	})
	if !resp.OK {
		t.Fatalf("acquire failed: %+v", resp.Error)
	}
	var wrap struct {
		Lease broker.Lease `json:"lease"`
	}
	if err := json.Unmarshal(resp.Result, &wrap); err != nil {
		t.Fatalf("unmarshal: %v raw=%s", err, resp.Result)
	}
	if wrap.Lease.ID == "" || wrap.Lease.State != broker.LeaseActive {
		t.Fatalf("lease = %+v", wrap.Lease)
	}
	if wrap.Lease.Device == nil || wrap.Lease.Device.UDID == "" {
		t.Fatalf("missing device: %+v", wrap.Lease)
	}
	if wrap.Lease.TTL != 30*time.Minute {
		t.Fatalf("ttl = %v want 30m", wrap.Lease.TTL)
	}
}

func TestAcquireCapacityQueuedHasQueuePosition(t *testing.T) {
	deps, _, _ := newTestDeps(t, 1)
	h := newHandlers(deps)

	ok := callHandler(t, h.acquire, "a1", map[string]any{
		"spec":  map[string]string{"platform": "iOS", "model": "iPhone 17 Pro"},
		"wait":  false,
		"owner": map[string]any{"pid": 1},
	})
	if !ok.OK {
		t.Fatalf("first acquire: %+v", ok.Error)
	}

	resp := callHandler(t, h.acquire, "a2", map[string]any{
		"spec":  map[string]string{"platform": "iOS", "model": "iPhone 17 Pro"},
		"wait":  false,
		"owner": map[string]any{"pid": 2},
	})
	if resp.OK || resp.Error == nil {
		t.Fatalf("want CAPACITY_QUEUED, got %+v", resp)
	}
	if resp.Error.Code != broker.CodeCapacityQueued {
		t.Fatalf("code = %q", resp.Error.Code)
	}
	if resp.Error.QueuePosition == nil || *resp.Error.QueuePosition < 1 {
		t.Fatalf("queuePosition missing: %+v", resp.Error)
	}
	raw, err := json.Marshal(resp.Error)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["queuePosition"]; !ok {
		t.Fatalf("wire error missing queuePosition: %s", raw)
	}
}

func TestGetListRenewReleaseWaitRoundTrip(t *testing.T) {
	deps, b, _ := newTestDeps(t, 2)
	h := newHandlers(deps)

	acq := callHandler(t, h.acquire, "r1", map[string]any{
		"spec":   map[string]string{"platform": "iOS", "model": "iPhone 17 Pro"},
		"labels": map[string]string{"task": "paywall"},
		"wait":   true,
		"owner":  map[string]any{"pid": 10},
	})
	if !acq.OK {
		t.Fatalf("acquire: %+v", acq.Error)
	}
	var acqWrap struct {
		Lease broker.Lease `json:"lease"`
	}
	if err := json.Unmarshal(acq.Result, &acqWrap); err != nil {
		t.Fatal(err)
	}
	id := acqWrap.Lease.ID

	get := callHandler(t, h.get, "g1", map[string]any{"id": id})
	if !get.OK {
		t.Fatalf("get: %+v", get.Error)
	}

	list := callHandler(t, h.list, "l1", map[string]any{
		"labels": map[string]string{"task": "paywall"},
	})
	if !list.OK {
		t.Fatalf("list: %+v", list.Error)
	}
	var listWrap struct {
		Leases []broker.Lease `json:"leases"`
	}
	if err := json.Unmarshal(list.Result, &listWrap); err != nil {
		t.Fatal(err)
	}
	if len(listWrap.Leases) != 1 || listWrap.Leases[0].ID != id {
		t.Fatalf("list = %+v", listWrap.Leases)
	}

	renew := callHandler(t, h.renew, "n1", map[string]any{"id": id, "ttl": "15m"})
	if !renew.OK {
		t.Fatalf("renew: %+v", renew.Error)
	}
	var renewWrap struct {
		Lease broker.Lease `json:"lease"`
	}
	if err := json.Unmarshal(renew.Result, &renewWrap); err != nil {
		t.Fatal(err)
	}
	if renewWrap.Lease.TTL != 15*time.Minute {
		t.Fatalf("renew ttl = %v", renewWrap.Lease.TTL)
	}

	wait := callHandler(t, h.wait, "w1", map[string]any{"id": id})
	if !wait.OK {
		t.Fatalf("wait: %+v", wait.Error)
	}

	rel := callHandler(t, h.release, "x1", map[string]any{"id": id})
	if !rel.OK {
		t.Fatalf("release: %+v", rel.Error)
	}
	got, ok := b.Get(id)
	if !ok || got.State != broker.LeaseReleased {
		t.Fatalf("after release: ok=%v state=%s", ok, got.State)
	}
}

func TestUnknownLeaseIDReturnsLeaseNotFound(t *testing.T) {
	deps, _, _ := newTestDeps(t, 1)
	h := newHandlers(deps)

	cases := []struct {
		name   string
		fn     ipc.HandlerFunc
		params map[string]any
	}{
		{"get", h.get, map[string]any{"id": "lease-missing"}},
		{"renew", h.renew, map[string]any{"id": "lease-missing", "ttl": "10m"}},
		{"release", h.release, map[string]any{"id": "lease-missing"}},
		{"wait", h.wait, map[string]any{"id": "lease-missing"}},
	}
	for _, tc := range cases {
		resp := callHandler(t, tc.fn, "e1", tc.params)
		if resp.OK || resp.Error == nil || resp.Error.Code != broker.CodeLeaseNotFound {
			t.Fatalf("%s: want LEASE_NOT_FOUND, got %+v", tc.name, resp)
		}
	}
}

func TestReclaimLoopCallsBrokerThenReaper(t *testing.T) {
	deps, b, deleter := newTestDeps(t, 2)
	lease, err := b.Acquire(context.Background(), broker.AcquireRequest{
		Spec:             testSpec(),
		TTL:              time.Hour,
		Wait:             true,
		ReleaseOnPIDExit: true,
		Owner:            broker.Owner{PID: 99999},
	})
	if err != nil {
		t.Fatal(err)
	}
	if lease.Device == nil {
		t.Fatal("missing device")
	}

	lister := &fakeLister{devices: []safety.Device{{
		UDID: lease.Device.UDID,
		Name: lease.Device.Name,
	}}}
	deps.Reaper = reaper.New(reaper.ReaperOptions{
		LeasesRoot: t.TempDir(),
		Lister:     lister,
		Deleter:    deleter,
		StartedAt:  func() time.Time { return time.Now().Add(-time.Minute) },
		Now:        time.Now,
	})

	loop := &ReclaimLoop{
		Deps:           deps,
		Logger:         func(string, ...any) {},
		IsProcessAlive: func(pid int) bool { return false },
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		loop.Run(ctx, 20*time.Millisecond, time.Hour)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, ok := b.Get(lease.ID)
		if ok && got.State == broker.LeaseReclaimed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	got, ok := b.Get(lease.ID)
	if !ok || got.State != broker.LeaseReclaimed {
		t.Fatalf("lease state = %v ok=%v want reclaimed", got.State, ok)
	}
	if deleted := deleter.deleted(); len(deleted) == 0 || deleted[0] != lease.Device.UDID {
		t.Fatalf("deleter calls = %v want %s", deleted, lease.Device.UDID)
	}
}

func TestReclaimLoopContinuesAfterFailure(t *testing.T) {
	deps, b, deleter := newTestDeps(t, 2)
	deleter.failOnce.Store(true)

	var leases []broker.Lease
	for i := 0; i < 2; i++ {
		lease, err := b.Acquire(context.Background(), broker.AcquireRequest{
			Spec:             testSpec(),
			TTL:              time.Hour,
			Wait:             true,
			ReleaseOnPIDExit: true,
			Owner:            broker.Owner{PID: 88000 + i},
		})
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, lease)
	}

	lister := &fakeLister{}
	for _, l := range leases {
		lister.devices = append(lister.devices, safety.Device{UDID: l.Device.UDID, Name: l.Device.Name})
	}
	deps.Reaper = reaper.New(reaper.ReaperOptions{
		LeasesRoot: t.TempDir(),
		Lister:     lister,
		Deleter:    deleter,
		StartedAt:  func() time.Time { return time.Now().Add(-time.Minute) },
		Now:        time.Now,
	})

	loop := &ReclaimLoop{
		Deps:           deps,
		Logger:         func(string, ...any) {},
		IsProcessAlive: func(pid int) bool { return false },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		loop.Run(ctx, 20*time.Millisecond, time.Hour)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		a, _ := b.Get(leases[0].ID)
		c, _ := b.Get(leases[1].ID)
		if a.State == broker.LeaseReclaimed && c.State == broker.LeaseReclaimed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	for _, l := range leases {
		got, ok := b.Get(l.ID)
		if !ok || got.State != broker.LeaseReclaimed {
			t.Fatalf("lease %s state=%s ok=%v", l.ID, got.State, ok)
		}
	}
	if n := len(deleter.deleted()); n < 2 {
		t.Fatalf("deleter calls = %d want >= 2 (got %v)", n, deleter.deleted())
	}
}

func TestOrphanScanLogsOnlyDoesNotDelete(t *testing.T) {
	deps, _, deleter := newTestDeps(t, 1)
	orphanUDID := "ORPHAN-UDID"
	lister := &fakeLister{devices: []safety.Device{{
		UDID: orphanUDID,
		Name: "simurgh-clone[orphan:iPhone 17 Pro:27.0]",
	}}}
	deps.Reaper = reaper.New(reaper.ReaperOptions{
		LeasesRoot: t.TempDir(),
		Lister:     lister,
		Deleter:    deleter,
		StartedAt:  func() time.Time { return time.Now().Add(-time.Minute) },
		Now:        time.Now,
	})

	var logs []string
	var logMu sync.Mutex
	loop := &ReclaimLoop{
		Deps: deps,
		Logger: func(format string, args ...any) {
			logMu.Lock()
			defer logMu.Unlock()
			logs = append(logs, fmt.Sprintf(format, args...))
		},
		IsProcessAlive: func(int) bool { return true },
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		loop.Run(ctx, time.Hour, 20*time.Millisecond)
	}()

	deadline := time.Now().Add(2 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		logMu.Lock()
		for _, line := range logs {
			if containsSub(line, orphanUDID) {
				found = true
			}
		}
		logMu.Unlock()
		if found {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	if !found {
		t.Fatalf("expected orphan log mentioning %s; logs=%v", orphanUDID, logs)
	}
	if n := len(deleter.deleted()); n != 0 {
		t.Fatalf("orphan scan must not delete; deleter calls=%v", deleter.deleted())
	}
}

func containsSub(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})())
}

func TestBrokerErrorToIPCMappings(t *testing.T) {
	pos := 2
	cases := []struct {
		name string
		err  error
		code string
		qp   *int
	}{
		{
			name: "not found",
			err: &broker.LeaseNotFoundError{
				LeaseID: "x",
				ErrorEnvelope: &broker.Error{
					Code: broker.CodeLeaseNotFound, Message: "missing", Fix: "list",
				},
			},
			code: broker.CodeLeaseNotFound,
		},
		{
			name: "invalid state",
			err: &broker.InvalidStateError{
				LeaseID: "x", State: broker.LeaseReleased,
				ErrorEnvelope: &broker.Error{
					Code: broker.CodeInvalidState, Message: "bad", Fix: "status",
				},
			},
			code: broker.CodeInvalidState,
		},
		{
			name: "capacity",
			err: &broker.CapacityQueuedError{
				Position: 2,
				ErrorEnvelope: &broker.Error{
					Code: broker.CodeCapacityQueued, Message: "full", Fix: "wait", QueuePosition: &pos,
				},
			},
			code: broker.CodeCapacityQueued,
			qp:   &pos,
		},
		{
			name: "provision",
			err: &broker.ProvisionFailedError{
				Cause: errors.New("boom"),
				ErrorEnvelope: &broker.Error{
					Code: broker.CodeProvisionFailed, Message: "fail", Fix: "retry",
				},
			},
			code: broker.CodeProvisionFailed,
		},
		{
			name: "fallback",
			err:  errors.New("mystery"),
			code: "INTERNAL",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := brokerErrorToIPC(tc.err)
			if got == nil || got.Code != tc.code {
				t.Fatalf("got %+v want code %s", got, tc.code)
			}
			if tc.qp != nil {
				if got.QueuePosition == nil || *got.QueuePosition != *tc.qp {
					t.Fatalf("queuePosition = %v want %d", got.QueuePosition, *tc.qp)
				}
			}
			if got.Fix == "" || got.Message == "" {
				t.Fatalf("incomplete triple: %+v", got)
			}
		})
	}
}

func TestRegisterLeaseHandlersWiresMethods(t *testing.T) {
	deps, _, _ := newTestDeps(t, 1)
	// macOS sun_path is ~104 bytes; t.TempDir() paths are often too long.
	dir, err := os.MkdirTemp("/tmp", "sm-d-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	ln, err := ipc.Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	srv := ipc.NewServer(ln)
	RegisterLeaseHandlers(srv, deps)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ctx) }()

	conn, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	if err := ipc.Encode(conn, ipc.Request{
		V: ipc.ProtocolVersion, ID: "ping", Method: "daemon.ping",
	}); err != nil {
		t.Fatal(err)
	}
	resp, err := ipc.DecodeResponse(conn)
	if err != nil || !resp.OK {
		t.Fatalf("ping: err=%v resp=%+v", err, resp)
	}

	params, _ := json.Marshal(map[string]any{
		"spec":  map[string]string{"platform": "iOS", "model": "iPhone 17 Pro"},
		"wait":  true,
		"owner": map[string]any{"pid": 1},
	})
	if err := ipc.Encode(conn, ipc.Request{
		V: ipc.ProtocolVersion, ID: "acq", Method: "lease.acquire", Params: params,
	}); err != nil {
		t.Fatal(err)
	}
	resp, err = ipc.DecodeResponse(conn)
	if err != nil || !resp.OK {
		t.Fatalf("acquire over socket: err=%v resp=%+v", err, resp)
	}
	_ = conn.Close()
	cancel()
	select {
	case <-errc:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not exit")
	}
}
