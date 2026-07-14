package provisioner

import (
	"context"
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
