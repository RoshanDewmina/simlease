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
	"github.com/RoshanDewmina/simurgh/internal/provisioner"
	"github.com/RoshanDewmina/simurgh/internal/reaper"
	"github.com/RoshanDewmina/simurgh/internal/safety"
	"github.com/RoshanDewmina/simurgh/internal/watcher"
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
		Lease  broker.Lease `json:"lease"`
		Reused bool         `json:"reused"`
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
	if wrap.Reused {
		t.Fatal("new allocation must report reused=false")
	}
}

func TestAcquireReuseIdentityAndObservableBoundary(t *testing.T) {
	t.Run("raw callers without identity never reuse", func(t *testing.T) {
		deps, _, _ := newTestDeps(t, 2)
		h := newHandlers(deps)
		params := map[string]any{
			"spec":          map[string]string{"platform": "iOS", "model": "iPhone 17 Pro"},
			"labels":        map[string]string{"project": "same"},
			"reuseIfExists": true,
			"wait":          true,
			"owner":         map[string]any{"pid": 1},
		}
		first := callHandler(t, h.acquire, "raw-1", params)
		params["owner"] = map[string]any{"pid": 2}
		second := callHandler(t, h.acquire, "raw-2", params)
		if !first.OK || !second.OK {
			t.Fatalf("acquires failed: first=%+v second=%+v", first.Error, second.Error)
		}
		var a, b struct {
			Lease  broker.Lease `json:"lease"`
			Reused bool         `json:"reused"`
		}
		if err := json.Unmarshal(first.Result, &a); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(second.Result, &b); err != nil {
			t.Fatal(err)
		}
		if a.Lease.ID == b.Lease.ID || a.Reused || b.Reused {
			t.Fatalf("raw callers reused: first=%+v second=%+v", a, b)
		}
	})

	t.Run("same explicit session reuses and reports true", func(t *testing.T) {
		deps, _, _ := newTestDeps(t, 2)
		h := newHandlers(deps)
		params := map[string]any{
			"spec":          map[string]string{"platform": "iOS", "model": "iPhone 17 Pro"},
			"labels":        map[string]string{"project": "same"},
			"reuseIfExists": true,
			"wait":          true,
			"owner": map[string]any{
				"pid": 1, "agent": "agent-a", "sessionId": "session-a",
			},
		}
		first := callHandler(t, h.acquire, "explicit-1", params)
		params["owner"] = map[string]any{
			"pid": 2, "agent": "agent-a", "sessionId": "session-a",
		}
		second := callHandler(t, h.acquire, "explicit-2", params)
		if !first.OK || !second.OK {
			t.Fatalf("acquires failed: first=%+v second=%+v", first.Error, second.Error)
		}
		var a, b struct {
			Lease  broker.Lease `json:"lease"`
			Reused bool         `json:"reused"`
		}
		if err := json.Unmarshal(first.Result, &a); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(second.Result, &b); err != nil {
			t.Fatal(err)
		}
		if a.Lease.ID != b.Lease.ID || a.Reused || !b.Reused {
			t.Fatalf("explicit session reuse flags wrong: first=%+v second=%+v", a, b)
		}
	})
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

func TestPoolStatusCountsByState(t *testing.T) {
	deps, b, _ := newTestDeps(t, 3)
	h := newHandlers(deps)

	lease, err := b.Acquire(context.Background(), broker.AcquireRequest{
		Spec:  testSpec(),
		TTL:   time.Hour,
		Wait:  true,
		Owner: broker.Owner{PID: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Release(context.Background(), lease.ID); err != nil {
		t.Fatal(err)
	}
	// Second acquire stays ACTIVE.
	if _, err := b.Acquire(context.Background(), broker.AcquireRequest{
		Spec:  testSpec(),
		TTL:   time.Hour,
		Wait:  true,
		Owner: broker.Owner{PID: 2},
	}); err != nil {
		t.Fatal(err)
	}

	resp := callHandler(t, h.poolStatus, "ps1", map[string]any{})
	if !resp.OK {
		t.Fatalf("pool.status: %+v", resp.Error)
	}
	var got struct {
		Capacity int            `json:"capacity"`
		Leases   map[string]int `json:"leases"`
	}
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("unmarshal: %v raw=%s", err, resp.Result)
	}
	if got.Capacity != 3 {
		t.Fatalf("capacity = %d want 3", got.Capacity)
	}
	if got.Leases["active"] != 1 {
		t.Fatalf("active = %d want 1; leases=%v", got.Leases["active"], got.Leases)
	}
	if got.Leases["released"] != 1 {
		t.Fatalf("released = %d want 1; leases=%v", got.Leases["released"], got.Leases)
	}
	for _, key := range []string{"queued", "provisioning", "cancelled", "failed", "reclaimed"} {
		if _, ok := got.Leases[key]; !ok {
			t.Fatalf("missing lease count key %q in %v", key, got.Leases)
		}
	}
}

// --- warm.* handler tests ---

type warmFakeSimctl struct {
	mu       sync.Mutex
	devices  []provisioner.SimDevice
	types    []provisioner.DeviceType
	runtimes []provisioner.Runtime
	next     int
	deleted  []string
}

func newWarmFakeSimctl() *warmFakeSimctl {
	return &warmFakeSimctl{
		types: []provisioner.DeviceType{
			{Name: "iPhone 17 Pro", Identifier: "dt-iphone"},
		},
		runtimes: []provisioner.Runtime{
			{Name: "iOS 27.0", Identifier: "rt-27", Version: "27.0", IsAvailable: true, PlatformName: "iOS"},
		},
	}
}

func (f *warmFakeSimctl) ListDeviceTypes(ctx context.Context) ([]provisioner.DeviceType, error) {
	_ = ctx
	return append([]provisioner.DeviceType(nil), f.types...), nil
}
func (f *warmFakeSimctl) ListRuntimes(ctx context.Context) ([]provisioner.Runtime, error) {
	_ = ctx
	return append([]provisioner.Runtime(nil), f.runtimes...), nil
}
func (f *warmFakeSimctl) ListDevices(ctx context.Context) ([]provisioner.SimDevice, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]provisioner.SimDevice(nil), f.devices...), nil
}
func (f *warmFakeSimctl) Create(ctx context.Context, name, deviceTypeID, runtimeID string) (string, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	udid := fmt.Sprintf("WARM-UDID-%d", f.next)
	f.devices = append(f.devices, provisioner.SimDevice{
		UDID: udid, Name: name, State: "Shutdown",
		RuntimeIdentifier: runtimeID, DeviceTypeIdentifier: deviceTypeID, IsAvailable: true,
	})
	return udid, nil
}
func (f *warmFakeSimctl) Clone(ctx context.Context, sourceUDID, newName string) (string, error) {
	_ = ctx
	_ = sourceUDID
	_ = newName
	return "", errors.New("clone not used in warm tests")
}
func (f *warmFakeSimctl) Rename(ctx context.Context, udid, newName string) error {
	_ = ctx
	_ = udid
	_ = newName
	return nil
}
func (f *warmFakeSimctl) Boot(ctx context.Context, udid string) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.devices {
		if f.devices[i].UDID == udid {
			f.devices[i].State = "Booted"
			return nil
		}
	}
	return fmt.Errorf("unknown %s", udid)
}
func (f *warmFakeSimctl) WaitBooted(ctx context.Context, udid string) error {
	_ = ctx
	_ = udid
	return nil
}
func (f *warmFakeSimctl) Shutdown(ctx context.Context, udid string) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.devices {
		if f.devices[i].UDID == udid {
			f.devices[i].State = "Shutdown"
			return nil
		}
	}
	return fmt.Errorf("unknown %s", udid)
}

type warmSafetyBridge struct {
	sim *warmFakeSimctl
}

func (b *warmSafetyBridge) ListDevices(ctx context.Context) ([]safety.Device, error) {
	devs, err := b.sim.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]safety.Device, len(devs))
	for i, d := range devs {
		out[i] = safety.Device{UDID: d.UDID, Name: d.Name}
	}
	return out, nil
}
func (b *warmSafetyBridge) Shutdown(ctx context.Context, udid string) error {
	return b.sim.Shutdown(ctx, udid)
}
func (b *warmSafetyBridge) Delete(ctx context.Context, udid string) error {
	b.sim.mu.Lock()
	defer b.sim.mu.Unlock()
	b.sim.deleted = append(b.sim.deleted, udid)
	kept := b.sim.devices[:0]
	for _, d := range b.sim.devices {
		if d.UDID != udid {
			kept = append(kept, d)
		}
	}
	b.sim.devices = kept
	return nil
}

func TestWarmCreateListRemove(t *testing.T) {
	fake := newWarmFakeSimctl()
	bridge := &warmSafetyBridge{sim: fake}
	prov := provisioner.New(fake)
	deps, _, _ := newTestDeps(t, 2)
	deps.Provisioner = prov
	deps.DeviceLister = bridge
	deps.DeviceDeleter = bridge
	h := newHandlers(deps)

	resp := callHandler(t, h.warmCreate, "wc1", map[string]any{
		"model": "iPhone 17 Pro",
	})
	if !resp.OK {
		t.Fatalf("warm.create: %+v", resp.Error)
	}
	var created struct {
		Device broker.Device `json:"device"`
	}
	if err := json.Unmarshal(resp.Result, &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.Device.Name != "simurgh-warm[iPhone 17 Pro:27.0]" {
		t.Fatalf("name=%q", created.Device.Name)
	}
	if created.Device.Booted {
		t.Fatal("warm template must be shutdown")
	}

	listResp := callHandler(t, h.warmList, "wl1", map[string]any{})
	if !listResp.OK {
		t.Fatalf("warm.list: %+v", listResp.Error)
	}
	var listed struct {
		Devices []broker.Device `json:"devices"`
	}
	if err := json.Unmarshal(listResp.Result, &listed); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(listed.Devices) != 1 {
		t.Fatalf("list len=%d want 1", len(listed.Devices))
	}

	rmResp := callHandler(t, h.warmRemove, "wr1", map[string]any{
		"model": "iPhone 17 Pro",
	})
	if !rmResp.OK {
		t.Fatalf("warm.remove: %+v", rmResp.Error)
	}
	if len(fake.deleted) != 1 || fake.deleted[0] != created.Device.UDID {
		t.Fatalf("deleted=%v want [%s]", fake.deleted, created.Device.UDID)
	}

	listResp2 := callHandler(t, h.warmList, "wl2", map[string]any{})
	if !listResp2.OK {
		t.Fatalf("warm.list after remove: %+v", listResp2.Error)
	}
	if err := json.Unmarshal(listResp2.Result, &listed); err != nil {
		t.Fatalf("unmarshal list2: %v", err)
	}
	if len(listed.Devices) != 0 {
		t.Fatalf("list after remove len=%d want 0", len(listed.Devices))
	}
}

func TestWarmCreate_NilProvisioner(t *testing.T) {
	deps, _, _ := newTestDeps(t, 1)
	h := newHandlers(deps)
	resp := callHandler(t, h.warmCreate, "wc-nil", map[string]any{"model": "iPhone 17 Pro"})
	if resp.OK {
		t.Fatal("expected failure with nil provisioner")
	}
	if resp.Error == nil || resp.Error.Code != "INTERNAL" {
		t.Fatalf("error=%+v want INTERNAL", resp.Error)
	}
}

func TestRelease_ActiveWithDevice_TriggersReaperTeardown(t *testing.T) {
	deps, b, deleter := newTestDeps(t, 2)
	h := newHandlers(deps)

	acq := callHandler(t, h.acquire, "r1", map[string]any{
		"spec":  map[string]string{"platform": "iOS", "model": "iPhone 17 Pro"},
		"wait":  true,
		"owner": map[string]any{"pid": 42},
	})
	if !acq.OK {
		t.Fatalf("acquire: %+v", acq.Error)
	}
	var wrap struct {
		Lease broker.Lease `json:"lease"`
	}
	if err := json.Unmarshal(acq.Result, &wrap); err != nil {
		t.Fatal(err)
	}
	if wrap.Lease.Device == nil {
		t.Fatal("missing device")
	}
	udid := wrap.Lease.Device.UDID

	// Reaper must see the live device for DeleteSimulatorClone to call Delete.
	lister := &fakeLister{devices: []safety.Device{{
		UDID: udid,
		Name: wrap.Lease.Device.Name,
	}}}
	deps.Reaper = reaper.New(reaper.ReaperOptions{
		LeasesRoot: t.TempDir(),
		Lister:     lister,
		Deleter:    deleter,
		StartedAt:  func() time.Time { return time.Now().Add(-time.Minute) },
		Now:        time.Now,
	})
	h = newHandlers(deps)

	rel := callHandler(t, h.release, "x1", map[string]any{"id": wrap.Lease.ID})
	if !rel.OK {
		t.Fatalf("release must succeed even if teardown is best-effort: %+v", rel.Error)
	}
	got, ok := b.Get(wrap.Lease.ID)
	if !ok || got.State != broker.LeaseReleased {
		t.Fatalf("after release: ok=%v state=%s", ok, got.State)
	}
	if deleted := deleter.deleted(); len(deleted) != 1 || deleted[0] != udid {
		t.Fatalf("want delete of %s, got %v", udid, deleted)
	}
}

func TestRelease_QueuedLease_NoDeviceDelete(t *testing.T) {
	deps, _, deleter := newTestDeps(t, 1)
	h := newHandlers(deps)

	first, err := deps.Broker.Acquire(context.Background(), broker.AcquireRequest{
		Spec:  testSpec(),
		Wait:  true,
		Owner: broker.Owner{PID: 1},
	})
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	_ = first

	// Wait=false returns the QUEUED lease alongside CAPACITY_QUEUED.
	queued, err := deps.Broker.Acquire(context.Background(), broker.AcquireRequest{
		Spec:  testSpec(),
		Wait:  false,
		Owner: broker.Owner{PID: 2},
	})
	var cq *broker.CapacityQueuedError
	if !errors.As(err, &cq) {
		t.Fatalf("want CapacityQueuedError, got %v", err)
	}
	if queued.State != broker.LeaseQueued {
		t.Fatalf("state=%s want queued", queued.State)
	}
	if queued.Device != nil {
		t.Fatalf("queued lease must have nil Device, got %+v", queued.Device)
	}

	rel := callHandler(t, h.release, "x1", map[string]any{"id": queued.ID})
	if !rel.OK {
		t.Fatalf("release queued: %+v", rel.Error)
	}
	if deleted := deleter.deleted(); len(deleted) != 0 {
		t.Fatalf("QUEUED→CANCELLED must not delete devices, got %v", deleted)
	}
}

func TestPrunePlan_ReturnsBuckets(t *testing.T) {
	root := t.TempDir()
	deps, b, deleter := newTestDeps(t, 2)
	deps.LeasesRoot = root
	lister := &fakeLister{}
	deps.DeviceLister = lister
	deps.DeviceDeleter = deleter
	deps.Reaper = reaper.New(reaper.ReaperOptions{
		LeasesRoot: root,
		Lister:     lister,
		Deleter:    deleter,
		StartedAt:  func() time.Time { return time.Now().Add(-time.Minute) },
		Now:        time.Now,
	})
	h := newHandlers(deps)

	active, err := b.Acquire(context.Background(), broker.AcquireRequest{
		Spec: testSpec(), Wait: true, Owner: broker.Owner{PID: 1}, TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Put a RELEASED lease with lingering device into the broker by releasing
	// without teardown (swap reaper to nil path: release with empty lister so
	// reclaim is best-effort no-op on missing device — but Device stays on lease).
	held, err := b.Acquire(context.Background(), broker.AcquireRequest{
		Spec: testSpec(), Wait: true, Owner: broker.Owner{PID: 2}, TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Bypass daemon release teardown: call Broker.Release directly so Device lingers.
	released, err := b.Release(context.Background(), held.ID)
	if err != nil {
		t.Fatal(err)
	}
	if released.Device == nil {
		t.Fatal("want lingering device on RELEASED lease")
	}

	orphanUDID := "ORPHAN-UDID"
	lister.mu.Lock()
	lister.devices = []safety.Device{
		{UDID: orphanUDID, Name: "simurgh-clone[orphan]"},
		{UDID: active.Device.UDID, Name: active.Device.Name},
		{UDID: released.Device.UDID, Name: released.Device.Name},
	}
	lister.mu.Unlock()

	resp := callHandler(t, h.prunePlan, "p1", map[string]any{})
	if !resp.OK {
		t.Fatalf("prune.plan: %+v", resp.Error)
	}
	var plan struct {
		Held    []broker.Lease `json:"held"`
		Orphans []string       `json:"orphans"`
		Active  []broker.Lease `json:"active"`
	}
	if err := json.Unmarshal(resp.Result, &plan); err != nil {
		t.Fatalf("unmarshal: %v raw=%s", err, resp.Result)
	}
	if len(plan.Held) != 1 || plan.Held[0].ID != released.ID {
		t.Fatalf("held=%+v want [%s]", plan.Held, released.ID)
	}
	if len(plan.Orphans) != 1 || plan.Orphans[0] != orphanUDID {
		t.Fatalf("orphans=%v want [%s]", plan.Orphans, orphanUDID)
	}
	foundActive := false
	for _, l := range plan.Active {
		if l.ID == active.ID {
			foundActive = true
		}
	}
	if !foundActive {
		t.Fatalf("active bucket missing %s: %+v", active.ID, plan.Active)
	}
}

func TestPruneApply_ReclaimsHeldAndDeletesOrphans_NeverTouchesActive(t *testing.T) {
	root := t.TempDir()
	deps, b, deleter := newTestDeps(t, 2)
	deps.LeasesRoot = root
	lister := &fakeLister{}
	deps.DeviceLister = lister
	deps.DeviceDeleter = deleter
	deps.Reaper = reaper.New(reaper.ReaperOptions{
		LeasesRoot: root,
		Lister:     lister,
		Deleter:    deleter,
		StartedAt:  func() time.Time { return time.Now().Add(-time.Minute) },
		Now:        time.Now,
	})
	h := newHandlers(deps)

	active, err := b.Acquire(context.Background(), broker.AcquireRequest{
		Spec: testSpec(), Wait: true, Owner: broker.Owner{PID: 1}, TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	held, err := b.Acquire(context.Background(), broker.AcquireRequest{
		Spec: testSpec(), Wait: true, Owner: broker.Owner{PID: 2}, TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	released, err := b.Release(context.Background(), held.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, released.ID), 0o700); err != nil {
		t.Fatal(err)
	}

	orphanUDID := "ORPHAN-APPLY"
	lister.mu.Lock()
	lister.devices = []safety.Device{
		{UDID: orphanUDID, Name: "simurgh-clone[orphan]"},
		{UDID: active.Device.UDID, Name: active.Device.Name},
		{UDID: released.Device.UDID, Name: released.Device.Name},
	}
	lister.mu.Unlock()

	resp := callHandler(t, h.pruneApply, "pa1", map[string]any{})
	if !resp.OK {
		t.Fatalf("prune.apply: %+v", resp.Error)
	}
	deleted := deleter.deleted()
	want := map[string]bool{orphanUDID: true, released.Device.UDID: true}
	for _, u := range deleted {
		delete(want, u)
	}
	if len(want) != 0 {
		t.Fatalf("missing deletes for %v; got %v", want, deleted)
	}
	for _, u := range deleted {
		if u == active.Device.UDID {
			t.Fatalf("prune.apply must never delete active device %s; deleted=%v", active.Device.UDID, deleted)
		}
	}
	still, ok := b.Get(active.ID)
	if !ok || still.State != broker.LeaseActive {
		t.Fatalf("active lease must remain ACTIVE, got ok=%v state=%v", ok, still.State)
	}
}

func TestReclaimLoopCancelsDeadQueuedOwner(t *testing.T) {
	deps, b, _ := newTestDeps(t, 1)
	first, err := b.Acquire(context.Background(), broker.AcquireRequest{
		Spec: testSpec(), TTL: time.Hour, Owner: broker.Owner{PID: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := b.Acquire(context.Background(), broker.AcquireRequest{
		Spec: testSpec(), TTL: time.Hour, Wait: false, Owner: broker.Owner{PID: 424242},
	})
	if !errors.As(err, new(*broker.CapacityQueuedError)) {
		t.Fatalf("want CAPACITY_QUEUED, got %v", err)
	}
	if queued.State != broker.LeaseQueued {
		t.Fatalf("state=%s want queued", queued.State)
	}

	loop := &ReclaimLoop{
		Deps:           deps,
		Logger:         func(string, ...any) {},
		IsProcessAlive: func(pid int) bool { return pid != 424242 },
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
		got, ok := b.Get(queued.ID)
		if ok && got.State == broker.LeaseCancelled {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	got, ok := b.Get(queued.ID)
	if !ok || got.State != broker.LeaseCancelled {
		t.Fatalf("queued lease state=%v ok=%v want cancelled", got.State, ok)
	}
	// Holding ACTIVE lease must remain untouched.
	if active, ok := b.Get(first.ID); !ok || active.State != broker.LeaseActive {
		t.Fatalf("active lease disturbed: %+v ok=%v", active, ok)
	}
}

func TestReclaimLoopSkipsQueuedOwnerDecisionAfterPromotion(t *testing.T) {
	deps, b, deleter := newTestDeps(t, 1)
	first, err := b.Acquire(context.Background(), broker.AcquireRequest{
		Spec: testSpec(), TTL: time.Hour, Owner: broker.Owner{PID: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := b.Acquire(context.Background(), broker.AcquireRequest{
		Spec: testSpec(), TTL: time.Hour, Wait: false, Owner: broker.Owner{PID: 424242},
	})
	if !errors.As(err, new(*broker.CapacityQueuedError)) {
		t.Fatalf("want CAPACITY_QUEUED, got %v", err)
	}

	// Deterministically model the race: watcher took its QUEUED snapshot, then
	// capacity freed and promoted the lease before applyDecisions ran.
	if _, err := b.Release(context.Background(), first.ID); err != nil {
		t.Fatalf("release first: %v", err)
	}
	promoted, ok := b.Get(queued.ID)
	if !ok || promoted.State != broker.LeaseActive {
		t.Fatalf("queued lease not promoted: %+v ok=%v", promoted, ok)
	}

	loop := &ReclaimLoop{Deps: deps}
	loop.applyDecisions(context.Background(), []watcher.ReclaimDecision{{
		LeaseID: queued.ID,
		Reason:  "queued owner process exited",
	}}, func(string, ...any) {})

	got, ok := b.Get(queued.ID)
	if !ok || got.State != broker.LeaseActive {
		t.Fatalf("stale queued-owner decision changed promoted lease: %+v ok=%v", got, ok)
	}
	if deleted := deleter.deleted(); len(deleted) != 0 {
		t.Fatalf("stale queued-owner decision reached reaper: deleted=%v", deleted)
	}
}

func TestAcquireCancelledMapsToActionableIPCError(t *testing.T) {
	deps, b, _ := newTestDeps(t, 1)
	h := newHandlers(deps)

	first, err := b.Acquire(context.Background(), broker.AcquireRequest{
		Spec: testSpec(), TTL: time.Hour, Owner: broker.Owner{PID: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = first

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan *ipc.Error, 1)
	go func() {
		params, _ := json.Marshal(map[string]any{
			"spec":  map[string]string{"platform": "iOS", "model": "iPhone 17 Pro", "runtime": "27.0"},
			"ttl":   "1h",
			"wait":  true,
			"owner": map[string]any{"pid": 2, "agent": "test"},
		})
		_, herr := h.acquire(ctx, ipc.Request{
			V: ipc.ProtocolVersion, ID: "cancel-wait", Method: "lease.acquire", Params: params,
		})
		errCh <- herr
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var queued bool
		for _, l := range b.List(broker.ListFilter{}) {
			if l.State == broker.LeaseQueued {
				queued = true
				break
			}
		}
		if queued {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	select {
	case herr := <-errCh:
		if herr == nil || herr.Code != broker.CodeAcquireCancelled {
			t.Fatalf("error=%+v want %s", herr, broker.CodeAcquireCancelled)
		}
		if herr.Message == "" || herr.Fix == "" {
			t.Fatalf("non-actionable error: %+v", herr)
		}
		if herr.Code == "INTERNAL" {
			t.Fatalf("want actionable ACQUIRE_CANCELLED, got INTERNAL %+v", herr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("acquire did not return after cancel")
	}

	for _, l := range b.List(broker.ListFilter{}) {
		if l.State == broker.LeaseQueued {
			t.Fatalf("QUEUED lease stranded: %+v", l)
		}
	}
}
