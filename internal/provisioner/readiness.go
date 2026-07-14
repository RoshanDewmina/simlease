package provisioner

import (
	"context"
	"fmt"
	"time"
)

// ReadinessProbe reports whether the subject is ready.
// It must be non-blocking aside from its own short I/O; PollReadiness owns the wait loop.
type ReadinessProbe func(ctx context.Context) (ready bool, err error)

// ReadinessPollOptions bounds a readiness poll. Timeout is required; zero means default.
type ReadinessPollOptions struct {
	Timeout  time.Duration
	Interval time.Duration
	Probe    ReadinessProbe
	// Fallback is an optional weaker check consulted once after Probe times out.
	// If it reports ready, PollReadiness succeeds (bounded timeout fallback).
	// If nil or not ready, the timeout is classified as READINESS_TIMEOUT.
	Fallback ReadinessProbe
	Subject  string
}

const (
	defaultReadinessTimeout  = 30 * time.Second
	defaultReadinessInterval = 200 * time.Millisecond
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
				if opts.Fallback != nil {
					fbReady, fbErr := opts.Fallback(context.Background())
					if fbErr == nil && fbReady {
						return nil
					}
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

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		ready, err := opts.Probe(ctx)
		if err != nil && ctx.Err() == nil {
			return err
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			if opts.Fallback != nil {
				fbReady, fbErr := opts.Fallback(context.Background())
				if fbErr == nil && fbReady {
					return nil
				}
			}
			return readinessTimeout(opts.Subject, timeout)
		case <-ticker.C:
		}
	}
}
