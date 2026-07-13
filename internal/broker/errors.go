package broker

import (
	"errors"
	"fmt"
)

// Error is the stable user-facing error triple (code/message/fix), plus optional
// queue position for CAPACITY_QUEUED.
//
// Duplicated from internal/ipc rather than imported: this package must stay
// transport-agnostic. Future cleanup: extract a shared errors package.
type Error struct {
	Code          string
	Message       string
	Fix           string
	QueuePosition *int
}

const (
	CodeCapacityQueued  = "CAPACITY_QUEUED"
	CodeLeaseNotFound   = "LEASE_NOT_FOUND"
	CodeInvalidState    = "INVALID_STATE"
	CodeProvisionFailed = "PROVISION_FAILED"

	FixCapacityQueued  = "simurgh status --queue   # or pass wait:true"
	FixLeaseNotFound   = "simurgh list"
	FixInvalidState    = "simurgh status"
	FixProvisionFailed = "simurgh acquire   # retry after fixing the provisioner error"
)

var (
	errLeaseNotFound   = errors.New("broker: lease not found")
	errInvalidState    = errors.New("broker: invalid lease state")
	errCapacityQueued  = errors.New("broker: capacity queued")
	errProvisionFailed = errors.New("broker: provision failed")
	errInvalidError    = errors.New("broker: error requires code, message, and fix")
)

// NewError constructs a validated error triple.
func NewError(code, message, fix string) (*Error, error) {
	return newError(code, message, fix, nil)
}

// NewErrorWithQueuePosition constructs a validated error triple with queue position.
func NewErrorWithQueuePosition(code, message, fix string, queuePosition int) (*Error, error) {
	pos := queuePosition
	return newError(code, message, fix, &pos)
}

func newError(code, message, fix string, queuePosition *int) (*Error, error) {
	err := &Error{Code: code, Message: message, Fix: fix, QueuePosition: queuePosition}
	if err.Code == "" || err.Message == "" || err.Fix == "" {
		return nil, errInvalidError
	}
	return err, nil
}

// LeaseNotFoundError is returned when a lease ID is unknown.
type LeaseNotFoundError struct {
	LeaseID       string
	ErrorEnvelope *Error
}

func (e *LeaseNotFoundError) Error() string {
	if e == nil || e.ErrorEnvelope == nil {
		return errLeaseNotFound.Error()
	}
	return fmt.Sprintf("broker: %s: %s", e.ErrorEnvelope.Code, e.ErrorEnvelope.Message)
}

func (e *LeaseNotFoundError) Unwrap() error { return errLeaseNotFound }

// InvalidStateError is returned when an operation is illegal for the lease's state.
type InvalidStateError struct {
	LeaseID       string
	State         LeaseState
	ErrorEnvelope *Error
}

func (e *InvalidStateError) Error() string {
	if e == nil || e.ErrorEnvelope == nil {
		return errInvalidState.Error()
	}
	return fmt.Sprintf("broker: %s: %s", e.ErrorEnvelope.Code, e.ErrorEnvelope.Message)
}

func (e *InvalidStateError) Unwrap() error { return errInvalidState }

// CapacityQueuedError is returned when Acquire cannot grant immediately (capacity
// full or pressure) and Wait is false.
type CapacityQueuedError struct {
	Position      int
	ErrorEnvelope *Error
}

func (e *CapacityQueuedError) Error() string {
	if e == nil || e.ErrorEnvelope == nil {
		return errCapacityQueued.Error()
	}
	return fmt.Sprintf("broker: %s: %s", e.ErrorEnvelope.Code, e.ErrorEnvelope.Message)
}

func (e *CapacityQueuedError) Unwrap() error { return errCapacityQueued }

// ProvisionFailedError is returned when Provisioner.Provision fails; the lease
// is left in FAILED.
type ProvisionFailedError struct {
	Cause         error
	ErrorEnvelope *Error
}

func (e *ProvisionFailedError) Error() string {
	if e == nil || e.ErrorEnvelope == nil {
		return errProvisionFailed.Error()
	}
	return fmt.Sprintf("broker: %s: %s", e.ErrorEnvelope.Code, e.ErrorEnvelope.Message)
}

func (e *ProvisionFailedError) Unwrap() error {
	if e != nil && e.Cause != nil {
		return e.Cause
	}
	return errProvisionFailed
}

func leaseNotFound(id string) error {
	msg := fmt.Sprintf("lease %q not found", id)
	env, err := NewError(CodeLeaseNotFound, msg, FixLeaseNotFound)
	if err != nil {
		return errLeaseNotFound
	}
	return &LeaseNotFoundError{LeaseID: id, ErrorEnvelope: env}
}

func invalidState(id string, state LeaseState, op string) error {
	msg := fmt.Sprintf("lease %q is %s; cannot %s", id, state, op)
	env, err := NewError(CodeInvalidState, msg, FixInvalidState)
	if err != nil {
		return errInvalidState
	}
	return &InvalidStateError{LeaseID: id, State: state, ErrorEnvelope: env}
}

func capacityQueued(position, capacity, active int, reason string) error {
	msg := fmt.Sprintf("%d leases ahead of you (capacity %d/%d active)", position, active, capacity)
	if reason != "" {
		msg = fmt.Sprintf("%s: %s", msg, reason)
	}
	env, err := NewErrorWithQueuePosition(CodeCapacityQueued, msg, FixCapacityQueued, position)
	if err != nil {
		return errCapacityQueued
	}
	return &CapacityQueuedError{Position: position, ErrorEnvelope: env}
}

func provisionFailed(cause error) error {
	msg := "device provisioning failed"
	if cause != nil {
		msg = fmt.Sprintf("device provisioning failed: %v", cause)
	}
	env, err := NewError(CodeProvisionFailed, msg, FixProvisionFailed)
	if err != nil {
		return &ProvisionFailedError{Cause: cause}
	}
	return &ProvisionFailedError{Cause: cause, ErrorEnvelope: env}
}
