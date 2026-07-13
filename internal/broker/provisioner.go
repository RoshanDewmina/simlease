package broker

import "context"

// ProvisionRequest is the input to Provisioner.Provision.
//
// LeaseID is required so the clone can be named
// simurgh-clone[<lease-id>:<model>:<runtime>] — the safety-invariant pattern
// used by M1.3 reaper / M1.4 safety. A small request struct is preferred over
// an extra positional parameter so call sites stay readable as the provision
// surface grows.
type ProvisionRequest struct {
	Spec    DeviceSpec
	LeaseID string
}

// Provisioner creates (and boots) a device for a ProvisionRequest.
// M1.2 supplies the real simctl-backed implementation; this package only
// depends on the interface. Unit tests inject a fake.
type Provisioner interface {
	Provision(ctx context.Context, req ProvisionRequest) (Device, error)
}
