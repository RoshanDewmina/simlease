package safety

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestIsWarmTemplateName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		// Intended positives
		{"simurgh-warm[iPhone 17 Pro:27.0]", true},
		{"simurgh-warm[a:b:c]", true},
		{"simurgh-warm[]", true}, // pattern allows empty bracket body via .*

		// Near-miss adversarial negatives (same discipline as TestIsSimurghManagedName)
		{"simurgh-clone[iPhone 17 Pro:27.0]", false},
		{"simurgh-base[iPhone 17 Pro:27.0]", false},
		{"Simurgh-warm[x]", false},     // wrong case
		{"simurgh-warm[x]evil", false}, // trailing garbage
		{"xsimurgh-warm[x]", false},    // leading garbage
		{"simurgh-warmy[x]", false},    // wrong kind
		{"simurgh-warm(x)", false},     // wrong bracket
		{"simurgh-warm", false},        // missing brackets
		{"", false},
		{"simurgh-warm[x]\n", false},
		{" simurgh-warm[x]", false},
		{"simurgh-warm[x] ", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsWarmTemplateName(tc.name); got != tc.want {
				t.Fatalf("IsWarmTemplateName(%q)=%v want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestIsSimurghManagedName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		// Intended positives
		{"simurgh-clone[x]", true},
		{"simurgh-warm[a:b:c]", true},
		{"simurgh-clone[lease-1:iPhone 17 Pro:27.0]", true},
		{"simurgh-warm[iPhone 17 Pro:27.0]", true},
		{"simurgh-clone[]", true}, // pattern allows empty bracket body via .*

		// Near-miss adversarial negatives
		{"Simurgh-clone[x]", false},     // wrong case
		{"simurgh-clone[x]evil", false}, // trailing garbage
		{"xsimurgh-clone[x]", false},    // leading garbage
		{"simurgh-cloney[x]", false},    // wrong kind
		{"simurgh-clone(x)", false},     // wrong bracket
		{"simurgh-base[x]", false},      // bases are never deletable
		{"simurgh-base[iPhone 17 Pro:27.0]", false},
		{"simurgh-clone", false}, // missing brackets
		{"simurgh-warm", false},
		{"", false},
		{"simurgh-clone[x]\n", false},
		{" simurgh-clone[x]", false},
		{"simurgh-clone[x] ", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsSimurghManagedName(tc.name); got != tc.want {
				t.Fatalf("IsSimurghManagedName(%q)=%v want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestIsSimurghManagedName_FullyAnchored(t *testing.T) {
	// Explicitly prove both ends are anchored: prefix/suffix garbage must fail.
	if IsSimurghManagedName("PREFIXsimurgh-clone[x]") {
		t.Fatal("leading garbage matched — regex not ^-anchored")
	}
	if IsSimurghManagedName("simurgh-clone[x]SUFFIX") {
		t.Fatal("trailing garbage matched — regex not $-anchored")
	}
	if !IsSimurghManagedName("simurgh-clone[x]") {
		t.Fatal("exact managed name should match")
	}
	if !IsSimurghManagedName("simurgh-warm[y]") {
		t.Fatal("exact warm name should match")
	}
}

func TestValidateDeviceDeletion_ManagedName(t *testing.T) {
	lister := &fakeLister{devices: []Device{
		{UDID: "AAAA", Name: "simurgh-clone[lease-1:iPhone:27.0]"},
	}}
	known := map[string]bool{"AAAA": true}

	dev, knownLease, err := ValidateDeviceDeletion(context.Background(), lister, "AAAA", known)
	if err != nil {
		t.Fatalf("ValidateDeviceDeletion: %v", err)
	}
	if !knownLease {
		t.Fatal("expected knownLease=true")
	}
	if dev.UDID != "AAAA" || !IsSimurghManagedName(dev.Name) {
		t.Fatalf("unexpected device: %+v", dev)
	}
}

func TestValidateDeviceDeletion_KnownLeaseNeverOverridesBadName(t *testing.T) {
	lister := &fakeLister{devices: []Device{
		{UDID: "BBBB", Name: "simurgh-base[iPhone 17 Pro:27.0]"},
	}}
	known := map[string]bool{"BBBB": true} // in known set — must still refuse

	_, _, err := ValidateDeviceDeletion(context.Background(), lister, "BBBB", known)
	if err == nil {
		t.Fatal("expected DeviceNotManagedError")
	}
	var notManaged *DeviceNotManagedError
	if !errors.As(err, &notManaged) {
		t.Fatalf("want DeviceNotManagedError, got %T: %v", err, err)
	}
}

func TestValidateDeviceDeletion_UnknownUDID(t *testing.T) {
	lister := &fakeLister{devices: []Device{
		{UDID: "AAAA", Name: "simurgh-clone[x]"},
	}}
	_, _, err := ValidateDeviceDeletion(context.Background(), lister, "MISSING", nil)
	if err == nil {
		t.Fatal("expected DeviceNotFoundError")
	}
	var notFound *DeviceNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("want DeviceNotFoundError, got %T: %v", err, err)
	}
}

func TestValidateDeviceDeletion_UsesLiveNameNotCallerExpectation(t *testing.T) {
	// Caller might stale-expect a clone name; live listing says base.
	lister := &fakeLister{devices: []Device{
		{UDID: "CCCC", Name: "User's iPhone"}, // live name is non-managed
	}}
	_, _, err := ValidateDeviceDeletion(context.Background(), lister, "CCCC", map[string]bool{"CCCC": true})
	if err == nil {
		t.Fatal("must check live name, not caller expectation")
	}
	var notManaged *DeviceNotManagedError
	if !errors.As(err, &notManaged) {
		t.Fatalf("want DeviceNotManagedError, got %T: %v", err, err)
	}
}

func TestValidateDeviceDeletion_OrphanManagedStillOK(t *testing.T) {
	// Managed name, UDID not in knownLeaseUDIDs — still validates; knownLease=false.
	lister := &fakeLister{devices: []Device{
		{UDID: "ORPHAN", Name: "simurgh-clone[orphan]"},
	}}
	dev, knownLease, err := ValidateDeviceDeletion(context.Background(), lister, "ORPHAN", map[string]bool{})
	if err != nil {
		t.Fatalf("orphan managed device should validate: %v", err)
	}
	if knownLease {
		t.Fatal("expected knownLease=false for orphan")
	}
	if dev.UDID != "ORPHAN" {
		t.Fatalf("got %+v", dev)
	}
}

func TestDeleteSimulatorClone_ManagedDeletes(t *testing.T) {
	lister := &fakeLister{devices: []Device{
		{UDID: "DDDD", Name: "simurgh-clone[lease-9:m:r]"},
	}}
	deleter := &fakeDeleter{}
	err := DeleteSimulatorClone(context.Background(), lister, deleter, "DDDD", map[string]bool{"DDDD": true})
	if err != nil {
		t.Fatalf("DeleteSimulatorClone: %v", err)
	}
	if !deleter.shutdownCalled || !deleter.deleteCalled {
		t.Fatalf("expected shutdown+delete; got shutdown=%v delete=%v", deleter.shutdownCalled, deleter.deleteCalled)
	}
	if deleter.deleteUDID != "DDDD" {
		t.Fatalf("deleted wrong udid %q", deleter.deleteUDID)
	}
}

func TestDeleteSimulatorClone_NonManagedNeverDeletes(t *testing.T) {
	lister := &fakeLister{devices: []Device{
		{UDID: "EEEE", Name: "simurgh-base[iPhone 17 Pro:27.0]"},
	}}
	deleter := &fakeDeleter{}
	err := DeleteSimulatorClone(context.Background(), lister, deleter, "EEEE", map[string]bool{"EEEE": true})
	if err == nil {
		t.Fatal("expected refusal")
	}
	var notManaged *DeviceNotManagedError
	if !errors.As(err, &notManaged) {
		t.Fatalf("want DeviceNotManagedError, got %T: %v", err, err)
	}
	if deleter.deleteCalled {
		t.Fatal("Delete must never be called for non-managed device")
	}
	if deleter.shutdownCalled {
		t.Fatal("Shutdown must never be called when validation fails")
	}
}

func TestDeleteSimulatorClone_IgnoresAlreadyShutdown(t *testing.T) {
	lister := &fakeLister{devices: []Device{
		{UDID: "FFFF", Name: "simurgh-warm[pool:1]"},
	}}
	deleter := &fakeDeleter{shutdownErr: fmt.Errorf("Unable to shutdown device in current state: Shutdown")}
	err := DeleteSimulatorClone(context.Background(), lister, deleter, "FFFF", nil)
	if err != nil {
		t.Fatalf("already-shutdown should be ignored: %v", err)
	}
	if !deleter.deleteCalled {
		t.Fatal("Delete should still run after already-shutdown")
	}
}

func TestDeleteSimulatorClone_PropagatesDeleteError(t *testing.T) {
	lister := &fakeLister{devices: []Device{
		{UDID: "GGGG", Name: "simurgh-clone[x]"},
	}}
	cause := errors.New("simctl boom")
	deleter := &fakeDeleter{deleteErr: cause}
	err := DeleteSimulatorClone(context.Background(), lister, deleter, "GGGG", nil)
	if err == nil {
		t.Fatal("expected delete error")
	}
	var delErr *DeviceDeleteFailedError
	if !errors.As(err, &delErr) {
		t.Fatalf("want DeviceDeleteFailedError, got %T: %v", err, err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("cause not wrapped: %v", err)
	}
}

func TestDeleteSimulatorClone_UnknownNeverDeletes(t *testing.T) {
	lister := &fakeLister{}
	deleter := &fakeDeleter{}
	err := DeleteSimulatorClone(context.Background(), lister, deleter, "NOPE", nil)
	if err == nil {
		t.Fatal("expected not found")
	}
	var notFound *DeviceNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("want DeviceNotFoundError, got %T: %v", err, err)
	}
	if deleter.deleteCalled || deleter.shutdownCalled {
		t.Fatal("must not shutdown/delete unknown udid")
	}
}

type fakeLister struct {
	devices []Device
	err     error
}

func (f *fakeLister) ListDevices(ctx context.Context) ([]Device, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]Device, len(f.devices))
	copy(out, f.devices)
	return out, nil
}

type fakeDeleter struct {
	shutdownErr    error
	deleteErr      error
	shutdownCalled bool
	deleteCalled   bool
	deleteUDID     string
}

func (f *fakeDeleter) Shutdown(ctx context.Context, udid string) error {
	f.shutdownCalled = true
	return f.shutdownErr
}

func (f *fakeDeleter) Delete(ctx context.Context, udid string) error {
	f.deleteCalled = true
	f.deleteUDID = udid
	return f.deleteErr
}
