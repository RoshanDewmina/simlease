package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RoshanDewmina/simurgh/internal/ipc"
)

func TestDefaultLastLeasePathHonorsSimurghHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SIMURGH_HOME", dir)
	got, err := DefaultLastLeasePath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "last-lease")
	if got != want {
		t.Fatalf("DefaultLastLeasePath() = %q want %q", got, want)
	}
}

func TestDefaultLastLeasePathDefaultHome(t *testing.T) {
	t.Setenv("SIMURGH_HOME", "")
	got, err := DefaultLastLeasePath()
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".simurgh", "last-lease")
	if got != want {
		t.Fatalf("DefaultLastLeasePath() = %q want %q", got, want)
	}
}

func TestExitCodeForIPCError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"capacity", &rpcError{e: &ipc.Error{Code: "CAPACITY_QUEUED", Message: "q", Fix: "f"}}, 2},
		{"not found", &rpcError{e: &ipc.Error{Code: "LEASE_NOT_FOUND", Message: "m", Fix: "f"}}, 3},
		{"invalid state", &rpcError{e: &ipc.Error{Code: "INVALID_STATE", Message: "m", Fix: "f"}}, 3},
		{"acquire cancelled", &rpcError{e: &ipc.Error{Code: "ACQUIRE_CANCELLED", Message: "m", Fix: "f"}}, 3},
		{"provision", &rpcError{e: &ipc.Error{Code: "PROVISION_FAILED", Message: "m", Fix: "f"}}, 4},
		{"daemon code", &rpcError{e: &ipc.Error{Code: "DAEMON_NOT_RUNNING", Message: "m", Fix: "f"}}, 4},
		{"daemon typed", &ipc.DaemonNotRunningError{ErrorEnvelope: &ipc.Error{Code: "DAEMON_NOT_RUNNING", Message: "m", Fix: "f"}}, 4},
		{"internal", &rpcError{e: &ipc.Error{Code: "INTERNAL", Message: "m", Fix: "f"}}, 5},
		{"unknown", errors.New("boom"), 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exitCodeForIPCError(tt.err); got != tt.want {
				t.Fatalf("exitCodeForIPCError(%v) = %d want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestParseKV(t *testing.T) {
	k, v, err := parseKV("task=paywall")
	if err != nil || k != "task" || v != "paywall" {
		t.Fatalf("parseKV = %q %q %v", k, v, err)
	}
	if _, _, err := parseKV("noequals"); err == nil {
		t.Fatal("want error for missing =")
	}
	if _, _, err := parseKV("=novalue"); err == nil {
		t.Fatal("want error for empty key")
	}
}

func TestParseLabelSelector(t *testing.T) {
	got, err := parseLabelSelector("task=paywall,env=ci")
	if err != nil {
		t.Fatal(err)
	}
	if got["task"] != "paywall" || got["env"] != "ci" || len(got) != 2 {
		t.Fatalf("got %#v", got)
	}
	if _, err := parseLabelSelector("bad"); err == nil {
		t.Fatal("want error")
	}
}

func TestParseAcquireFlags(t *testing.T) {
	f, err := parseAcquireFlags([]string{
		"--model", "iPhone 17 Pro",
		"--runtime", "27.0",
		"--label", "task=paywall",
		"--label", "env=ci",
		"--ttl", "30m",
		"--reuse-if-exists",
		"--wait",
		"--rm",
		"--inactivity-timeout", "10m",
		"--hard-timeout", "3h",
		"--json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.Model != "iPhone 17 Pro" || f.Runtime != "27.0" {
		t.Fatalf("model/runtime = %#v", f)
	}
	if f.Labels["task"] != "paywall" || f.Labels["env"] != "ci" {
		t.Fatalf("labels = %#v", f.Labels)
	}
	if f.TTL != 30*time.Minute || f.Inactivity != 10*time.Minute || f.Hard != 3*time.Hour {
		t.Fatalf("durations = %#v", f)
	}
	if !f.ReuseIfExists || !f.Wait || !f.RM || !f.JSON {
		t.Fatalf("bools = %#v", f)
	}
}

func TestParseAcquireFlagsRequiresModel(t *testing.T) {
	_, err := parseAcquireFlags([]string{"--ttl", "30m"})
	if err == nil {
		t.Fatal("want error for missing --model")
	}
}

func TestParseAcquireFlagsBadDuration(t *testing.T) {
	_, err := parseAcquireFlags([]string{"--model", "iPhone 17 Pro", "--ttl", "nope"})
	if err == nil {
		t.Fatal("want duration error")
	}
}
