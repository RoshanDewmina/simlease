package safety

import (
	"errors"
	"fmt"
)

// Error is the stable user-facing error triple (code/message/fix).
//
// Duplicated from internal/broker and internal/provisioner rather than
// imported: this package must not depend on ipc/broker/provisioner.
type Error struct {
	Code    string
	Message string
	Fix     string
}

const (
	CodeInvalidLeaseID        = "INVALID_LEASE_ID"
	CodePathOutsideLeasesRoot = "PATH_OUTSIDE_LEASES_ROOT"
	CodeDeviceNotManaged      = "DEVICE_NOT_MANAGED"
	CodeDeviceNotFound        = "DEVICE_NOT_FOUND"
	CodeDeviceDeleteFailed    = "DEVICE_DELETE_FAILED"

	FixInvalidLeaseID        = "use a lease ID of the form lease-<digits> (e.g. lease-1)"
	FixPathOutsideLeasesRoot = "only per-lease directories under the injected leases root may be deleted"
	FixDeviceNotManaged      = "only devices named simurgh-clone[...] or simurgh-warm[...] may be deleted"
	FixDeviceNotFound        = "xcrun simctl list devices"
	FixDeviceDeleteFailed    = "xcrun simctl list devices   # inspect state, then retry"
)

var (
	errInvalidLeaseID        = errors.New("safety: invalid lease id")
	errPathOutsideLeasesRoot = errors.New("safety: path outside leases root")
	errDeviceNotManaged      = errors.New("safety: device not simurgh-managed")
	errDeviceNotFound        = errors.New("safety: device not found")
	errDeviceDeleteFailed    = errors.New("safety: device delete failed")
	errInvalidError          = errors.New("safety: error requires code, message, and fix")
)

// NewError constructs a validated error triple.
func NewError(code, message, fix string) (*Error, error) {
	err := &Error{Code: code, Message: message, Fix: fix}
	if err.Code == "" || err.Message == "" || err.Fix == "" {
		return nil, errInvalidError
	}
	return err, nil
}

// InvalidLeaseIDError is returned when leaseID fails the allowlist pattern.
type InvalidLeaseIDError struct {
	LeaseID       string
	ErrorEnvelope *Error
}

func (e *InvalidLeaseIDError) Error() string {
	if e == nil || e.ErrorEnvelope == nil {
		return errInvalidLeaseID.Error()
	}
	return fmt.Sprintf("safety: %s: %s", e.ErrorEnvelope.Code, e.ErrorEnvelope.Message)
}

func (e *InvalidLeaseIDError) Unwrap() error { return errInvalidLeaseID }

// PathOutsideLeasesRootError is returned when a resolved path escapes the
// leases root, equals the root, or otherwise fails the strict-child check.
type PathOutsideLeasesRootError struct {
	LeasesRoot    string
	Target        string
	ErrorEnvelope *Error
}

func (e *PathOutsideLeasesRootError) Error() string {
	if e == nil || e.ErrorEnvelope == nil {
		return errPathOutsideLeasesRoot.Error()
	}
	return fmt.Sprintf("safety: %s: %s", e.ErrorEnvelope.Code, e.ErrorEnvelope.Message)
}

func (e *PathOutsideLeasesRootError) Unwrap() error { return errPathOutsideLeasesRoot }

// DeviceNotManagedError is returned when a live device name does not match
// the simurgh-(clone|warm) deletion pattern.
type DeviceNotManagedError struct {
	UDID          string
	Name          string
	ErrorEnvelope *Error
}

func (e *DeviceNotManagedError) Error() string {
	if e == nil || e.ErrorEnvelope == nil {
		return errDeviceNotManaged.Error()
	}
	return fmt.Sprintf("safety: %s: %s", e.ErrorEnvelope.Code, e.ErrorEnvelope.Message)
}

func (e *DeviceNotManagedError) Unwrap() error { return errDeviceNotManaged }

// DeviceNotFoundError is returned when no device with the given UDID appears
// in a live simctl listing.
type DeviceNotFoundError struct {
	UDID          string
	ErrorEnvelope *Error
}

func (e *DeviceNotFoundError) Error() string {
	if e == nil || e.ErrorEnvelope == nil {
		return errDeviceNotFound.Error()
	}
	return fmt.Sprintf("safety: %s: %s", e.ErrorEnvelope.Code, e.ErrorEnvelope.Message)
}

func (e *DeviceNotFoundError) Unwrap() error { return errDeviceNotFound }

// DeviceDeleteFailedError wraps a failed DeviceDeleter.Delete call.
type DeviceDeleteFailedError struct {
	UDID          string
	Cause         error
	ErrorEnvelope *Error
}

func (e *DeviceDeleteFailedError) Error() string {
	if e == nil || e.ErrorEnvelope == nil {
		return errDeviceDeleteFailed.Error()
	}
	return fmt.Sprintf("safety: %s: %s", e.ErrorEnvelope.Code, e.ErrorEnvelope.Message)
}

func (e *DeviceDeleteFailedError) Unwrap() error {
	if e != nil && e.Cause != nil {
		return e.Cause
	}
	return errDeviceDeleteFailed
}

func invalidLeaseID(id string) error {
	msg := fmt.Sprintf("lease id %q is not a valid lease-<digits> identifier", id)
	env, err := NewError(CodeInvalidLeaseID, msg, FixInvalidLeaseID)
	if err != nil {
		return errInvalidLeaseID
	}
	return &InvalidLeaseIDError{LeaseID: id, ErrorEnvelope: env}
}

func pathOutsideLeasesRoot(root, target, reason string) error {
	msg := reason
	if msg == "" {
		msg = fmt.Sprintf("resolved path %q is not a strict child of leases root %q", target, root)
	}
	env, err := NewError(CodePathOutsideLeasesRoot, msg, FixPathOutsideLeasesRoot)
	if err != nil {
		return errPathOutsideLeasesRoot
	}
	return &PathOutsideLeasesRootError{LeasesRoot: root, Target: target, ErrorEnvelope: env}
}

func deviceNotManaged(udid, name string) error {
	msg := fmt.Sprintf("device %s name %q is not a simurgh-managed clone/warm name", udid, name)
	env, err := NewError(CodeDeviceNotManaged, msg, FixDeviceNotManaged)
	if err != nil {
		return errDeviceNotManaged
	}
	return &DeviceNotManagedError{UDID: udid, Name: name, ErrorEnvelope: env}
}

func deviceNotFound(udid string) error {
	msg := fmt.Sprintf("device %s not found in live simctl listing", udid)
	env, err := NewError(CodeDeviceNotFound, msg, FixDeviceNotFound)
	if err != nil {
		return errDeviceNotFound
	}
	return &DeviceNotFoundError{UDID: udid, ErrorEnvelope: env}
}

func deviceDeleteFailed(udid string, cause error) error {
	msg := "simctl delete failed"
	if cause != nil {
		msg = fmt.Sprintf("simctl delete failed: %v", cause)
	}
	env, err := NewError(CodeDeviceDeleteFailed, msg, FixDeviceDeleteFailed)
	if err != nil {
		return &DeviceDeleteFailedError{UDID: udid, Cause: cause}
	}
	return &DeviceDeleteFailedError{UDID: udid, Cause: cause, ErrorEnvelope: env}
}
