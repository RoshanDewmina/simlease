package provisioner

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ReadinessProbe reports whether the subject is ready.
// It must be non-blocking aside from its own short I/O; PollReadiness owns the wait loop.
type ReadinessProbe func(ctx context.Context) (ready bool, err error)

// ReadinessPollOptions bounds a readiness poll. Timeout is required; zero means default.
type ReadinessPollOptions struct {
	Timeout         time.Duration
	Interval        time.Duration
	FallbackTimeout time.Duration
	Probe           ReadinessProbe
	// Fallback is an optional weaker check consulted once after Probe times out.
	// If it reports ready, PollReadiness succeeds (bounded timeout fallback).
	// If nil or not ready, the timeout is classified as READINESS_TIMEOUT.
	Fallback ReadinessProbe
	Subject  string
}

const (
	defaultReadinessTimeout  = 30 * time.Second
	defaultReadinessInterval = 200 * time.Millisecond
	maxFallbackTimeout       = 5 * time.Second
)

// PollReadiness repeatedly invokes Probe until ready, ctx cancel, or Timeout.
// It never waits forever: Timeout, an existing ctx deadline, or the default
// readiness timeout always bounds the loop.
func PollReadiness(ctx context.Context, opts ReadinessPollOptions) error {
	if opts.Probe == nil {
		return fmt.Errorf("provisioner: readiness probe is required")
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		if deadline, ok := ctx.Deadline(); ok {
			timeout = time.Until(deadline)
			if timeout <= 0 {
				if err := ctx.Err(); errors.Is(err, context.Canceled) {
					return err
				}
				return readinessTimeout(opts.Subject, 0)
			}
		} else {
			timeout = defaultReadinessTimeout
		}
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = defaultReadinessInterval
	}

	callerCtx := ctx
	primaryCtx, cancel := context.WithTimeout(callerCtx, timeout)
	defer cancel()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		ready, err := opts.Probe(primaryCtx)
		if err != nil && primaryCtx.Err() == nil {
			return err
		}
		if ready {
			return nil
		}
		select {
		case <-primaryCtx.Done():
			if errors.Is(callerCtx.Err(), context.Canceled) {
				return context.Canceled
			}
			if opts.Fallback == nil {
				return readinessTimeout(opts.Subject, timeout)
			}

			fallbackTimeout := opts.FallbackTimeout
			if fallbackTimeout <= 0 {
				fallbackTimeout = min(timeout, maxFallbackTimeout)
				if fallbackTimeout <= 0 {
					fallbackTimeout = maxFallbackTimeout
				}
			}
			fallbackCtx, fallbackCancel := context.WithTimeout(callerCtx, fallbackTimeout)
			type fallbackResult struct {
				ready bool
				err   error
			}
			done := make(chan fallbackResult, 1)
			go func() {
				ready, err := opts.Fallback(fallbackCtx)
				done <- fallbackResult{ready: ready, err: err}
			}()
			select {
			case result := <-done:
				fallbackCancel()
				if result.err == nil && result.ready {
					return nil
				}
				if errors.Is(callerCtx.Err(), context.Canceled) {
					return context.Canceled
				}
				return readinessTimeout(opts.Subject, timeout+fallbackTimeout)
			case <-fallbackCtx.Done():
				fallbackCancel()
				if errors.Is(callerCtx.Err(), context.Canceled) {
					return context.Canceled
				}
				return readinessTimeout(opts.Subject, timeout+fallbackTimeout)
			}
		case <-ticker.C:
		}
	}
}
