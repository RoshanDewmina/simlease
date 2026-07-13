package provisioner

import (
	"errors"
	"fmt"
)

// Error is the stable user-facing error triple (code/message/fix).
//
// Duplicated from internal/broker rather than imported for the envelope type:
// this package stays free of transport concerns. Importing broker for
// DeviceSpec/Device is intentional and one-directional.
type Error struct {
	Code    string
	Message string
	Fix     string
}

const (
	CodeDeviceTypeNotFound = "DEVICE_TYPE_NOT_FOUND"
	CodeRuntimeNotFound    = "RUNTIME_NOT_FOUND"
	CodeCloneFailed        = "CLONE_FAILED"
	CodeBootTimeout        = "BOOT_TIMEOUT"

	FixDeviceTypeNotFound = "xcrun simctl list devicetypes"
	FixRuntimeNotFound    = "xcodebuild -downloadPlatform iOS"
	FixCloneFailed        = "simurgh acquire   # retry after checking simctl list devices"
	FixBootTimeout        = "check `xcrun simctl list devices` for a stuck boot"
)

var (
	errDeviceTypeNotFound = errors.New("provisioner: device type not found")
	errRuntimeNotFound    = errors.New("provisioner: runtime not found")
	errCloneFailed        = errors.New("provisioner: clone failed")
	errBootTimeout        = errors.New("provisioner: boot timeout")
	errInvalidError       = errors.New("provisioner: error requires code, message, and fix")
)

// NewError constructs a validated error triple.
func NewError(code, message, fix string) (*Error, error) {
	err := &Error{Code: code, Message: message, Fix: fix}
	if err.Code == "" || err.Message == "" || err.Fix == "" {
		return nil, errInvalidError
	}
	return err, nil
}

// DeviceTypeNotFoundError is returned when DeviceSpec.Model matches no installed type.
type DeviceTypeNotFoundError struct {
	Model         string
	ErrorEnvelope *Error
}

func (e *DeviceTypeNotFoundError) Error() string {
	if e == nil || e.ErrorEnvelope == nil {
		return errDeviceTypeNotFound.Error()
	}
	return fmt.Sprintf("provisioner: %s: %s", e.ErrorEnvelope.Code, e.ErrorEnvelope.Message)
}

func (e *DeviceTypeNotFoundError) Unwrap() error { return errDeviceTypeNotFound }

// RuntimeNotFoundError is returned when the requested (or newest) iOS runtime is missing.
type RuntimeNotFoundError struct {
	Runtime       string
	ErrorEnvelope *Error
}

func (e *RuntimeNotFoundError) Error() string {
	if e == nil || e.ErrorEnvelope == nil {
		return errRuntimeNotFound.Error()
	}
	return fmt.Sprintf("provisioner: %s: %s", e.ErrorEnvelope.Code, e.ErrorEnvelope.Message)
}

func (e *RuntimeNotFoundError) Unwrap() error { return errRuntimeNotFound }

// CloneFailedError is returned when simctl clone/create/rename fails.
type CloneFailedError struct {
	Cause         error
	ErrorEnvelope *Error
}

func (e *CloneFailedError) Error() string {
	if e == nil || e.ErrorEnvelope == nil {
		return errCloneFailed.Error()
	}
	return fmt.Sprintf("provisioner: %s: %s", e.ErrorEnvelope.Code, e.ErrorEnvelope.Message)
}

func (e *CloneFailedError) Unwrap() error {
	if e != nil && e.Cause != nil {
		return e.Cause
	}
	return errCloneFailed
}

// BootTimeoutError is returned when the clone does not reach Booted in time.
type BootTimeoutError struct {
	UDID          string
	ErrorEnvelope *Error
}

func (e *BootTimeoutError) Error() string {
	if e == nil || e.ErrorEnvelope == nil {
		return errBootTimeout.Error()
	}
	return fmt.Sprintf("provisioner: %s: %s", e.ErrorEnvelope.Code, e.ErrorEnvelope.Message)
}

func (e *BootTimeoutError) Unwrap() error { return errBootTimeout }

func deviceTypeNotFound(model string) error {
	msg := fmt.Sprintf("device type %q not found", model)
	env, err := NewError(CodeDeviceTypeNotFound, msg, FixDeviceTypeNotFound)
	if err != nil {
		return errDeviceTypeNotFound
	}
	return &DeviceTypeNotFoundError{Model: model, ErrorEnvelope: env}
}

func runtimeNotFound(runtime string) error {
	msg := "no installed iOS simulator runtime"
	if runtime != "" {
		msg = fmt.Sprintf("iOS runtime %q not found", runtime)
	}
	env, err := NewError(CodeRuntimeNotFound, msg, FixRuntimeNotFound)
	if err != nil {
		return errRuntimeNotFound
	}
	return &RuntimeNotFoundError{Runtime: runtime, ErrorEnvelope: env}
}

func cloneFailed(cause error) error {
	msg := "simctl clone failed"
	if cause != nil {
		msg = fmt.Sprintf("simctl clone failed: %v", cause)
	}
	env, err := NewError(CodeCloneFailed, msg, FixCloneFailed)
	if err != nil {
		return &CloneFailedError{Cause: cause}
	}
	return &CloneFailedError{Cause: cause, ErrorEnvelope: env}
}

func bootTimeout(udid string) error {
	msg := fmt.Sprintf("device %s did not reach Booted within the timeout", udid)
	env, err := NewError(CodeBootTimeout, msg, FixBootTimeout)
	if err != nil {
		return errBootTimeout
	}
	return &BootTimeoutError{UDID: udid, ErrorEnvelope: env}
}
