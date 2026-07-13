package provisioner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
)

type fakeSimctl struct {
	mu sync.Mutex

	deviceTypes []DeviceType
	runtimes    []Runtime
	devices     []SimDevice

	createCalls []createCall
	cloneCalls  []cloneCall
	renameCalls []renameCall
	bootCalls   []string
	waitCalls   []string

	cloneErr  error
	bootErr   error
	waitErr   error
	createErr error

	nextUDID int

	// raceDelay, when set, slows ListDevices (no base yet) and Create so two
	// concurrent ensureBaseDevice callers can both observe an empty list before
	// either Create finishes — the list-then-create race window.
	raceDelay time.Duration
}

type createCall struct {
	Name, DeviceTypeID, RuntimeID string
}

type cloneCall struct {
	SourceUDID, NewName string
}

type renameCall struct {
	UDID, NewName string
}

func (f *fakeSimctl) ListDeviceTypes(ctx context.Context) ([]DeviceType, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]DeviceType(nil), f.deviceTypes...), nil
}

func (f *fakeSimctl) ListRuntimes(ctx context.Context) ([]Runtime, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Runtime(nil), f.runtimes...), nil
}

func (f *fakeSimctl) ListDevices(ctx context.Context) ([]SimDevice, error) {
	_ = ctx
	f.mu.Lock()
	out := append([]SimDevice(nil), f.devices...)
	delay := f.raceDelay
	f.mu.Unlock()

	if delay > 0 && !hasBaseDevice(out) {
		time.Sleep(delay)
	}
	return out, nil
}

func hasBaseDevice(devices []SimDevice) bool {
	for _, d := range devices {
		if strings.HasPrefix(d.Name, "simurgh-base[") {
			return true
		}
	}
	return false
}

func (f *fakeSimctl) Create(ctx context.Context, name, deviceTypeID, runtimeID string) (string, error) {
	_ = ctx
	f.mu.Lock()
	delay := f.raceDelay
	f.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls = append(f.createCalls, createCall{name, deviceTypeID, runtimeID})
	if f.createErr != nil {
		return "", f.createErr
	}
	f.nextUDID++
	udid := fmt.Sprintf("BASE-%d", f.nextUDID)
	f.devices = append(f.devices, SimDevice{
		UDID:                 udid,
		Name:                 name,
		State:                "Shutdown",
		RuntimeIdentifier:    runtimeID,
		DeviceTypeIdentifier: deviceTypeID,
		IsAvailable:          true,
	})
	return udid, nil
}

func (f *fakeSimctl) Clone(ctx context.Context, sourceUDID, newName string) (string, error) {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cloneCalls = append(f.cloneCalls, cloneCall{sourceUDID, newName})
	if f.cloneErr != nil {
		return "", f.cloneErr
	}
	f.nextUDID++
	udid := fmt.Sprintf("CLONE-%d", f.nextUDID)
	var src SimDevice
	for _, d := range f.devices {
		if d.UDID == sourceUDID {
			src = d
			break
		}
	}
	f.devices = append(f.devices, SimDevice{
		UDID:                 udid,
		Name:                 newName,
		State:                "Shutdown",
		RuntimeIdentifier:    src.RuntimeIdentifier,
		DeviceTypeIdentifier: src.DeviceTypeIdentifier,
		IsAvailable:          true,
	})
	return udid, nil
}

func (f *fakeSimctl) Rename(ctx context.Context, udid, newName string) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renameCalls = append(f.renameCalls, renameCall{udid, newName})
	for i := range f.devices {
		if f.devices[i].UDID == udid {
			f.devices[i].Name = newName
			return nil
		}
	}
	return fmt.Errorf("unknown udid %s", udid)
}

func (f *fakeSimctl) Boot(ctx context.Context, udid string) error {
	_ = ctx
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bootCalls = append(f.bootCalls, udid)
	if f.bootErr != nil {
		return f.bootErr
	}
	for i := range f.devices {
		if f.devices[i].UDID == udid {
			f.devices[i].State = "Booted"
			return nil
		}
	}
	return fmt.Errorf("unknown udid %s", udid)
}

func (f *fakeSimctl) WaitBooted(ctx context.Context, udid string) error {
	f.mu.Lock()
	f.waitCalls = append(f.waitCalls, udid)
	err := f.waitErr
	f.mu.Unlock()
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func defaultFake() *fakeSimctl {
	return &fakeSimctl{
		deviceTypes: []DeviceType{
			{Name: "iPhone 17 Pro", Identifier: "com.apple.CoreSimulator.SimDeviceType.iPhone-17-Pro"},
		},
		runtimes: []Runtime{
			{
				Name:         "iOS 26.0",
				Identifier:   "com.apple.CoreSimulator.SimRuntime.iOS-26-0",
				Version:      "26.0",
				IsAvailable:  true,
				PlatformName: "iOS",
			},
			{
				Name:         "iOS 27.0",
				Identifier:   "com.apple.CoreSimulator.SimRuntime.iOS-27-0",
				Version:      "27.0",
				IsAvailable:  true,
				PlatformName: "iOS",
			},
		},
	}
}

func TestProvisionSuccessCloneAndBoot(t *testing.T) {
	fake := defaultFake()
	p := New(fake)
	req := broker.ProvisionRequest{
		LeaseID: "lease-1",
		Spec:    broker.DeviceSpec{Platform: "iOS", Model: "iPhone 17 Pro"},
	}

	dev, err := p.Provision(context.Background(), req)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}

	wantName := "simurgh-clone[lease-1:iPhone 17 Pro:27.0]"
	if dev.Name != wantName {
		t.Fatalf("Name=%q want %q", dev.Name, wantName)
	}
	if !strings.HasPrefix(dev.UDID, "CLONE-") {
		t.Fatalf("UDID=%q want CLONE-*", dev.UDID)
	}
	if dev.Runtime != "27.0" {
		t.Fatalf("Runtime=%q want 27.0 (newest)", dev.Runtime)
	}
	if !dev.Booted {
		t.Fatal("Booted=false want true")
	}
	if !strings.HasPrefix(dev.CloneOf, "BASE-") {
		t.Fatalf("CloneOf=%q want BASE-*", dev.CloneOf)
	}
	if len(fake.cloneCalls) != 1 {
		t.Fatalf("cloneCalls=%d want 1", len(fake.cloneCalls))
	}
	if len(fake.bootCalls) != 1 || fake.bootCalls[0] != dev.UDID {
		t.Fatalf("bootCalls=%v want [%s]", fake.bootCalls, dev.UDID)
	}
	if len(fake.renameCalls) != 1 || fake.renameCalls[0].NewName != wantName {
		t.Fatalf("renameCalls=%v want rename to %s", fake.renameCalls, wantName)
	}
}

func TestProvisionMissingDeviceType(t *testing.T) {
	fake := defaultFake()
	p := New(fake)
	_, err := p.Provision(context.Background(), broker.ProvisionRequest{
		LeaseID: "lease-1",
		Spec:    broker.DeviceSpec{Model: "iPhone Not Real"},
	})
	var dtErr *DeviceTypeNotFoundError
	if !errors.As(err, &dtErr) {
		t.Fatalf("err=%v want DeviceTypeNotFoundError", err)
	}
	if dtErr.ErrorEnvelope == nil || dtErr.ErrorEnvelope.Fix != FixDeviceTypeNotFound {
		t.Fatalf("fix=%v want %q", dtErr.ErrorEnvelope, FixDeviceTypeNotFound)
	}
	if dtErr.ErrorEnvelope.Code != CodeDeviceTypeNotFound {
		t.Fatalf("code=%q want %q", dtErr.ErrorEnvelope.Code, CodeDeviceTypeNotFound)
	}
}

func TestProvisionMissingRuntime(t *testing.T) {
	fake := defaultFake()
	fake.runtimes = nil
	p := New(fake)
	_, err := p.Provision(context.Background(), broker.ProvisionRequest{
		LeaseID: "lease-1",
		Spec:    broker.DeviceSpec{Model: "iPhone 17 Pro", Runtime: "99.0"},
	})
	var rtErr *RuntimeNotFoundError
	if !errors.As(err, &rtErr) {
		t.Fatalf("err=%v want RuntimeNotFoundError", err)
	}
	if rtErr.ErrorEnvelope == nil || rtErr.ErrorEnvelope.Fix != FixRuntimeNotFound {
		t.Fatalf("fix=%v want exact %q", rtErr.ErrorEnvelope, FixRuntimeNotFound)
	}
	if rtErr.ErrorEnvelope.Fix != "xcodebuild -downloadPlatform iOS" {
		t.Fatalf("fix=%q", rtErr.ErrorEnvelope.Fix)
	}
}

func TestProvisionMissingRuntimeExactWant(t *testing.T) {
	fake := defaultFake()
	p := New(fake)
	_, err := p.Provision(context.Background(), broker.ProvisionRequest{
		LeaseID: "lease-1",
		Spec:    broker.DeviceSpec{Model: "iPhone 17 Pro", Runtime: "18.0"},
	})
	var rtErr *RuntimeNotFoundError
	if !errors.As(err, &rtErr) {
		t.Fatalf("err=%v want RuntimeNotFoundError", err)
	}
	if rtErr.ErrorEnvelope.Fix != "xcodebuild -downloadPlatform iOS" {
		t.Fatalf("fix=%q", rtErr.ErrorEnvelope.Fix)
	}
}

func TestProvisionBootTimeout(t *testing.T) {
	fake := defaultFake()
	fake.waitErr = context.DeadlineExceeded
	p := New(fake)
	_, err := p.Provision(context.Background(), broker.ProvisionRequest{
		LeaseID: "lease-1",
		Spec:    broker.DeviceSpec{Model: "iPhone 17 Pro"},
	})
	var bootErr *BootTimeoutError
	if !errors.As(err, &bootErr) {
		t.Fatalf("err=%v want BootTimeoutError", err)
	}
	if bootErr.ErrorEnvelope == nil || bootErr.ErrorEnvelope.Fix != FixBootTimeout {
		t.Fatalf("fix=%v want %q", bootErr.ErrorEnvelope, FixBootTimeout)
	}
}

func TestProvisionReusesBaseDevice(t *testing.T) {
	fake := defaultFake()
	p := New(fake)
	req := broker.ProvisionRequest{
		LeaseID: "lease-1",
		Spec:    broker.DeviceSpec{Model: "iPhone 17 Pro"},
	}

	dev1, err := p.Provision(context.Background(), req)
	if err != nil {
		t.Fatalf("first Provision: %v", err)
	}

	req.LeaseID = "lease-2"
	dev2, err := p.Provision(context.Background(), req)
	if err != nil {
		t.Fatalf("second Provision: %v", err)
	}

	if len(fake.createCalls) != 1 {
		t.Fatalf("createCalls=%d want 1 (base reused, not recreated)", len(fake.createCalls))
	}
	if dev1.CloneOf != dev2.CloneOf {
		t.Fatalf("CloneOf diverged: %q vs %q", dev1.CloneOf, dev2.CloneOf)
	}
	if len(fake.cloneCalls) != 2 {
		t.Fatalf("cloneCalls=%d want 2", len(fake.cloneCalls))
	}
	for _, c := range fake.cloneCalls {
		if c.SourceUDID != dev1.CloneOf {
			t.Fatalf("clone source=%q want base %q", c.SourceUDID, dev1.CloneOf)
		}
	}
}

func TestConcurrentSameSpecProvisionCreatesBaseOnce(t *testing.T) {
	fake := defaultFake()
	fake.raceDelay = 20 * time.Millisecond
	p := New(fake)
	spec := broker.DeviceSpec{Platform: "iOS", Model: "iPhone 17 Pro"}

	var wg sync.WaitGroup
	devs := make([]broker.Device, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			dev, err := p.Provision(context.Background(), broker.ProvisionRequest{
				LeaseID: fmt.Sprintf("lease-%d", i+1),
				Spec:    spec,
			})
			devs[i] = dev
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("Provision %d: %v", i, err)
		}
	}
	if len(fake.createCalls) != 1 {
		t.Fatalf("createCalls=%d want 1 (single base under concurrent same-spec Provision)", len(fake.createCalls))
	}
	if !strings.HasPrefix(fake.createCalls[0].Name, "simurgh-base[") {
		t.Fatalf("create name=%q want simurgh-base[...]", fake.createCalls[0].Name)
	}
	if devs[0].CloneOf == "" || devs[0].CloneOf != devs[1].CloneOf {
		t.Fatalf("CloneOf diverged: %q vs %q", devs[0].CloneOf, devs[1].CloneOf)
	}
	if len(fake.cloneCalls) != 2 {
		t.Fatalf("cloneCalls=%d want 2", len(fake.cloneCalls))
	}
}

func TestCompareVersionNewest(t *testing.T) {
	if compareVersion("27.0", "26.1") <= 0 {
		t.Fatal("27.0 should be newer than 26.1")
	}
	if compareVersion("26.1", "26.0") <= 0 {
		t.Fatal("26.1 should be newer than 26.0")
	}
	if compareVersion("27.0", "27.0") != 0 {
		t.Fatal("equal versions")
	}
}

func TestBaseNameDoesNotMatchCloneWarmPattern(t *testing.T) {
	base := baseDeviceName("iPhone 17 Pro", "27.0")
	if strings.HasPrefix(base, "simurgh-clone[") || strings.HasPrefix(base, "simurgh-warm[") {
		t.Fatalf("base name %q must not look like clone/warm", base)
	}
	if !strings.HasPrefix(base, "simurgh-base[") {
		t.Fatalf("base=%q", base)
	}
}
