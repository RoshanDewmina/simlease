package controlplane

import (
	"errors"
	"fmt"
)

const (
	CodeBuildQueued = "BUILD_QUEUED"

	FixBuildQueued = "simurgh status --json   # inspect buildSlots/activeBuilds/queuedBuilds; retry when a slot frees"
)

// Error is the stable code/message/fix triple for control-plane errors.
type Error struct {
	Code          string
	Message       string
	Fix           string
	QueuePosition *int
	Reason        string
	ResourceClass string
}

func (e *Error) Error() string {
	if e == nil {
		return "controlplane: error"
	}
	return fmt.Sprintf("controlplane: %s: %s", e.Code, e.Message)
}

// BuildQueuedError is returned when a build slot cannot be granted immediately
// and Wait is false (or as the waiter metadata source).
type BuildQueuedError struct {
	Position      int
	Reason        string
	ResourceClass string
	ErrorEnvelope *Error
}

func (e *BuildQueuedError) Error() string {
	if e == nil || e.ErrorEnvelope == nil {
		return "controlplane: build queued"
	}
	return e.ErrorEnvelope.Error()
}

func (e *BuildQueuedError) Unwrap() error { return errBuildQueued }

var (
	errBuildQueued   = errors.New("controlplane: build queued")
	errInvalidState  = errors.New("controlplane: invalid job state")
	errNotFound      = errors.New("controlplane: not found")
	errInvalidError  = errors.New("controlplane: error requires code, message, and fix")
)

func newError(code, message, fix string, queuePosition *int) (*Error, error) {
	err := &Error{Code: code, Message: message, Fix: fix, QueuePosition: queuePosition}
	if err.Code == "" || err.Message == "" || err.Fix == "" {
		return nil, errInvalidError
	}
	return err, nil
}

func buildQueued(position, slots, active int) error {
	msg := fmt.Sprintf("%d builds ahead of you (build slots %d/%d active)", position, active, slots)
	env, err := newError(CodeBuildQueued, msg, FixBuildQueued, &position)
	if err != nil {
		return errBuildQueued
	}
	env.Reason = ResourceClassBuildSlot
	env.ResourceClass = ResourceClassBuildSlot
	return &BuildQueuedError{
		Position:      position,
		Reason:        ResourceClassBuildSlot,
		ResourceClass: ResourceClassBuildSlot,
		ErrorEnvelope: env,
	}
}

// InvalidJobStateError is returned for illegal FSM transitions.
type InvalidJobStateError struct {
	JobID string
	From  JobState
	To    JobState
	Op    string
}

func (e *InvalidJobStateError) Error() string {
	if e == nil {
		return errInvalidState.Error()
	}
	return fmt.Sprintf("controlplane: job %q cannot %s from %s to %s", e.JobID, e.Op, e.From, e.To)
}

func (e *InvalidJobStateError) Unwrap() error { return errInvalidState }

// NotFoundError is returned when a session/job ID is unknown.
type NotFoundError struct {
	Kind string
	ID   string
}

func (e *NotFoundError) Error() string {
	if e == nil {
		return errNotFound.Error()
	}
	return fmt.Sprintf("controlplane: %s %q not found", e.Kind, e.ID)
}

func (e *NotFoundError) Unwrap() error { return errNotFound }
