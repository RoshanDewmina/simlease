package broker

import "context"

// Provisioner creates (and boots) a device for a DeviceSpec.
// M1.2 supplies the real simctl-backed implementation; this package only
// depends on the interface. Unit tests inject a fake.
type Provisioner interface {
	Provision(ctx context.Context, spec DeviceSpec) (Device, error)
}
