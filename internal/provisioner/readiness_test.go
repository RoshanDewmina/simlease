package provisioner

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReadinessPollSuccess(t *testing.T) {
	deadline := time.Now().Add(2 * time.Second)
	attempts := 0
	err := PollReadiness(context.Background(), ReadinessPollOptions{
		Timeout:  time.Second,
		Interval: 5 * time.Millisecond,
		Probe: func(ctx context.Context) (bool, error) {
			attempts++
			return attempts >= 3, nil
		},
	})
	if err != nil {
		t.Fatalf("PollReadiness: %v", err)
	}
	if attempts < 3 {
		t.Fatalf("attempts=%d want >=3", attempts)
	}
	if time.Now().After(deadline) {
		t.Fatal("poll took too long")
	}
}

func TestReadinessPollTimeoutClassified(t *testing.T) {
	start := time.Now()
	err := PollReadiness(context.Background(), ReadinessPollOptions{
		Timeout:  40 * time.Millisecond,
		Interval: 5 * time.Millisecond,
		Probe: func(ctx context.Context) (bool, error) {
			return false, nil
		},
		Subject: "device-test-udid",
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected readiness timeout")
	}
	var rt *ReadinessTimeoutError
	if !asReadinessTimeout(err, &rt) {
		t.Fatalf("error type=%T (%v); want ReadinessTimeoutError", err, err)
	}
	if rt.ErrorEnvelope == nil || rt.ErrorEnvelope.Code != CodeReadinessTimeout {
		t.Fatalf("envelope=%+v want code %s", rt.ErrorEnvelope, CodeReadinessTimeout)
	}
	if rt.Subject != "device-test-udid" {
		t.Fatalf("subject=%q", rt.Subject)
	}
	// Bounded: must not hang far past the timeout.
	if elapsed > 500*time.Millisecond {
		t.Fatalf("poll hung: elapsed=%v", elapsed)
	}

	// Fallback probe: primary never ready, but fallback accepts Booted → success.
	fallbackCalls := 0
	err = PollReadiness(context.Background(), ReadinessPollOptions{
		Timeout:  30 * time.Millisecond,
		Interval: 5 * time.Millisecond,
		Probe: func(ctx context.Context) (bool, error) {
			return false, nil
		},
		Fallback: func(ctx context.Context) (bool, error) {
			fallbackCalls++
			return true, nil
		},
		Subject: "fallback-udid",
	})
	if err != nil {
		t.Fatalf("fallback path: %v", err)
	}
	if fallbackCalls == 0 {
		t.Fatal("expected fallback probe to run after timeout")
	}
}

func asReadinessTimeout(err error, target **ReadinessTimeoutError) bool {
	if err == nil {
		return false
	}
	if e, ok := err.(*ReadinessTimeoutError); ok {
		*target = e
		return true
	}
	return false
}

func TestReadinessBlockingFallbackIsBounded(t *testing.T) {
	start := time.Now()
	err := PollReadiness(context.Background(), ReadinessPollOptions{
		Timeout:         20 * time.Millisecond,
		Interval:        2 * time.Millisecond,
		FallbackTimeout: 30 * time.Millisecond,
		Probe: func(context.Context) (bool, error) {
			return false, nil
		},
		Fallback: func(context.Context) (bool, error) {
			select {} // deliberately ignores context
		},
		Subject: "blocked-fallback",
	})
	if time.Since(start) > 300*time.Millisecond {
		t.Fatalf("blocking fallback exceeded bound: %v", time.Since(start))
	}
	var timeout *ReadinessTimeoutError
	if !errors.As(err, &timeout) {
		t.Fatalf("err=%T %v; want ReadinessTimeoutError", err, err)
	}
}

func TestReadinessCallerCancellationDistinguishable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(15 * time.Millisecond)
		cancel()
	}()
	err := PollReadiness(ctx, ReadinessPollOptions{
		Timeout:  time.Second,
		Interval: 2 * time.Millisecond,
		Probe: func(context.Context) (bool, error) {
			return false, nil
		},
		Fallback: func(context.Context) (bool, error) {
			return false, nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%T %v; want context.Canceled", err, err)
	}
	var timeout *ReadinessTimeoutError
	if errors.As(err, &timeout) {
		t.Fatalf("caller cancellation misclassified as readiness timeout: %v", err)
	}
}
