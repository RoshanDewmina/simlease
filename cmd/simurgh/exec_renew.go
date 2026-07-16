package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
)

const defaultBrokerTTL = 60 * time.Minute

// computeExecRenewInterval returns how often exec should renew a lease while
// the child process runs. Rule: lease TTL / 3 when TTL is known; otherwise
// remaining time until ExpiresAt / 3; otherwise default broker TTL / 3 (20m).
func computeExecRenewInterval(lease broker.Lease, now time.Time) time.Duration {
	var base time.Duration
	switch {
	case lease.TTL > 0:
		base = lease.TTL
	case !lease.ExpiresAt.IsZero():
		remaining := lease.ExpiresAt.Sub(now)
		if remaining > 0 {
			base = remaining
		}
	}
	if base <= 0 {
		base = defaultBrokerTTL
	}
	interval := base / 3
	if interval < time.Minute {
		return time.Minute
	}
	return interval
}

type execRenewLoop struct {
	leaseID  string
	interval time.Duration
	renew    func(context.Context) error
	logf     func(format string, args ...any)
}

func newExecRenewLoop(leaseID string, lease broker.Lease, renew func(context.Context) error) *execRenewLoop {
	return &execRenewLoop{
		leaseID:  leaseID,
		interval: computeExecRenewInterval(lease, time.Now()),
		renew:    renew,
		logf:     func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) },
	}
}

func (l *execRenewLoop) run(ctx context.Context) {
	if l.renew == nil || l.interval <= 0 {
		return
	}
	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := l.renew(ctx); err != nil {
				l.logf("simurgh exec: lease renew failed for %s: %v", l.leaseID, err)
			}
		}
	}
}

func logExecLeaseGoneAfterChild(leaseID string, err error, w io.Writer) {
	if err == nil || w == nil {
		return
	}
	var re *rpcError
	if !errors.As(err, &re) || re.e == nil || re.e.Code != broker.CodeLeaseNotFound {
		return
	}
	fmt.Fprintf(w, "simurgh exec: lease %s is gone after command exit (%s)\n", leaseID, re.e.Message)
}

func logExecLeaseStatusAfterChild(leaseID string) {
	if _, getErr := dialAndCall(context.Background(), "lease.get", map[string]any{"id": leaseID}); getErr != nil {
		logExecLeaseGoneAfterChild(leaseID, getErr, os.Stderr)
	}
}
