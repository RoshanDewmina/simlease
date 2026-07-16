package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/broker"
	"github.com/RoshanDewmina/simurgh/internal/ipc"
)

func TestSimurghHomeDirHonorsSimurghHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SIMURGH_HOME", dir)
	t.Setenv("HOME", "/polluted/home")
	got, err := simurghHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("simurghHomeDir() = %q want %q", got, dir)
	}
}

func TestSimurghHomeDirUsesPasswdHome(t *testing.T) {
	passwd, err := passwdHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SIMURGH_HOME", "")
	t.Setenv("HOME", passwd)
	got, err := simurghHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(passwd, ".simurgh")
	if got != want {
		t.Fatalf("simurghHomeDir() = %q want %q", got, want)
	}
}

func TestSimurghHomeDirFailsClosedOnHomeMismatch(t *testing.T) {
	passwd, err := passwdHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if passwd == "/tmp/polluted" {
		t.Skip("cannot construct mismatch against passwd home")
	}
	t.Setenv("SIMURGH_HOME", "")
	t.Setenv("HOME", "/tmp/polluted")
	_, err = simurghHomeDir()
	if err == nil {
		t.Fatal("expected error when HOME differs from passwd home")
	}
}

func TestComputeExecRenewInterval(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		lease broker.Lease
		want  time.Duration
	}{
		{
			name:  "ttl known",
			lease: broker.Lease{TTL: 60 * time.Minute},
			want:  20 * time.Minute,
		},
		{
			name:  "expiresAt fallback",
			lease: broker.Lease{ExpiresAt: now.Add(30 * time.Minute)},
			want:  10 * time.Minute,
		},
		{
			name:  "default broker ttl",
			lease: broker.Lease{},
			want:  20 * time.Minute,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := computeExecRenewInterval(tc.lease, now); got != tc.want {
				t.Fatalf("computeExecRenewInterval() = %v want %v", got, tc.want)
			}
		})
	}
}

func TestExecRenewLoopStopsOnCancel(t *testing.T) {
	renewCh := make(chan struct{}, 8)
	loop := &execRenewLoop{
		leaseID:  "lease-1",
		interval: time.Millisecond,
		renew: func(context.Context) error {
			renewCh <- struct{}{}
			return nil
		},
		logf: func(string, ...any) {},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		loop.run(ctx)
		close(done)
	}()

	select {
	case <-renewCh:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("renew was not invoked")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("renew loop did not stop after cancel")
	}
}

func TestLogExecLeaseGoneAfterChild(t *testing.T) {
	var buf bytes.Buffer
	logExecLeaseGoneAfterChild("lease-1", &rpcError{e: &ipc.Error{
		Code: broker.CodeLeaseNotFound, Message: "missing",
	}}, &buf)
	if !strings.Contains(buf.String(), "lease-1 is gone") {
		t.Fatalf("output = %q", buf.String())
	}
}

func TestMatchLeaseOwner(t *testing.T) {
	lease := broker.Lease{Owner: broker.Owner{PID: 42, Agent: "simurgh-cli", SessionID: "sess-1"}}
	if !matchLeaseOwner(lease, 42, "other", "other") {
		t.Fatal("pid match expected")
	}
	if !matchLeaseOwner(lease, 99, "simurgh-cli", "sess-1") {
		t.Fatal("agent+session match expected")
	}
	if matchLeaseOwner(lease, 99, "simurgh-cli", "other") {
		t.Fatal("partial agent/session should not match")
	}
}

func TestReleaseOwnerGuard(t *testing.T) {
	lease := broker.Lease{ID: "lease-1", Owner: broker.Owner{PID: os.Getpid(), Agent: cliAgentName, SessionID: cliSessionID()}}
	if err := assertReleaseOwner(lease, false); err != nil {
		t.Fatalf("owner should be allowed: %v", err)
	}
	other := broker.Lease{ID: "lease-2", Owner: broker.Owner{PID: 1, Agent: "other", SessionID: "other"}}
	if err := assertReleaseOwner(other, false); err == nil {
		t.Fatal("non-owner release should fail")
	}
	if err := assertReleaseOwner(other, true); err != nil {
		t.Fatalf("--force should bypass owner guard: %v", err)
	}
}
