package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	home, err := passwdHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	got, err := DefaultLastLeasePath()
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

func TestCLISessionIDExplicitAndStableFallback(t *testing.T) {
	t.Setenv("SIMURGH_SESSION_ID", "explicit-cli-session")
	if first, second := cliSessionID(), cliSessionID(); first != "explicit-cli-session" || second != first {
		t.Fatalf("explicit session IDs=%q/%q", first, second)
	}

	t.Setenv("SIMURGH_SESSION_ID", "")
	first := cliSessionID()
	second := cliSessionID()
	if first == "" || second != first || !strings.HasPrefix(first, "cli:") {
		t.Fatalf("process fallback IDs=%q/%q", first, second)
	}
}

func TestNewRandomSessionIDDiffersAcrossFreshGeneration(t *testing.T) {
	first := newRandomSessionID("cli")
	second := newRandomSessionID("cli")
	if first == second {
		t.Fatalf("fresh random session IDs collided: %q", first)
	}
}

func TestPrintLeaseResultSurfacesReused(t *testing.T) {
	raw := []byte(`{"reused":true,"lease":{"id":"lease-1","state":"active","spec":{"model":"iPhone 17 Pro"},"ttl":""}}`)
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = old })

	if err := printLeaseResult(raw, false); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = old
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "reused: true") {
		t.Fatalf("human acquire output missing reused=true:\n%s", out)
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

func TestExitCodeForIPCErrorDaemonDisconnected(t *testing.T) {
	err := &ipc.DaemonDisconnectedError{
		ErrorEnvelope: &ipc.Error{
			Code:    ipc.CodeDaemonDisconnected,
			Message: "connection to daemon lost",
			Fix:     ipc.FixDaemonDisconnected,
		},
	}
	if got := exitCodeForIPCError(err); got != 4 {
		t.Fatalf("exitCodeForIPCError(DAEMON_DISCONNECTED) = %d want 4", got)
	}
}

func TestDialAndCallDaemonDisconnectOnEOF(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "smg-eof-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("SIMURGH_HOME", home)
	socketPath := filepath.Join(home, "simurghd.sock")
	ln, err := ipc.Listen(socketPath)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		if _, err := ipc.NewDecoder(c).DecodeRequest(); err != nil {
			return
		}
		// Drop the connection after the request was received so the client
		// sees EOF on the response read — the W18 / issue #2 failure mode.
	}()

	_, err = dialAndCall(context.Background(), "lease.list", map[string]any{})
	if err == nil {
		t.Fatal("want daemon-disconnected error, got nil")
	}
	var dd *ipc.DaemonDisconnectedError
	if !errors.As(err, &dd) || dd.ErrorEnvelope == nil {
		t.Fatalf("err = %v (%T), want DaemonDisconnectedError", err, err)
	}
	if dd.ErrorEnvelope.Code != ipc.CodeDaemonDisconnected {
		t.Fatalf("code=%q want %s", dd.ErrorEnvelope.Code, ipc.CodeDaemonDisconnected)
	}
	if dd.ErrorEnvelope.Fix == "" || !strings.Contains(dd.ErrorEnvelope.Fix, "simurgh daemon status") {
		t.Fatalf("fix=%q want actionable daemon status fix", dd.ErrorEnvelope.Fix)
	}
	if exitCodeForIPCError(err) != 4 {
		t.Fatalf("exit code = %d want 4", exitCodeForIPCError(err))
	}
}

func TestPrintIPCErrorDaemonDisconnect(t *testing.T) {
	err := &ipc.DaemonDisconnectedError{
		ErrorEnvelope: &ipc.Error{
			Code:    ipc.CodeDaemonDisconnected,
			Message: "connection to daemon lost after request was sent",
			Fix:     ipc.FixDaemonDisconnected,
		},
	}
	old := os.Stdout
	r, w, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatal(pipeErr)
	}
	os.Stdout = w
	printIPCError(err, false)
	_ = w.Close()
	os.Stdout = old
	out, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatal(readErr)
	}
	text := string(out)
	if !strings.Contains(text, ipc.CodeDaemonDisconnected) || !strings.Contains(text, "fix:") {
		t.Fatalf("printIPCError output missing structured triple:\n%s", text)
	}
}

func TestPrintIPCErrorDaemonDisconnectJSON(t *testing.T) {
	err := &ipc.DaemonDisconnectedError{
		ErrorEnvelope: &ipc.Error{
			Code:    ipc.CodeDaemonDisconnected,
			Message: "connection to daemon lost after request was sent",
			Fix:     ipc.FixDaemonDisconnected,
		},
	}
	old := os.Stdout
	r, w, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatal(pipeErr)
	}
	os.Stdout = w
	printIPCError(err, true)
	_ = w.Close()
	os.Stdout = old
	out, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatal(readErr)
	}
	var got ipc.Error
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("printIPCError JSON: %v\n%s", err, out)
	}
	if got.Code != ipc.CodeDaemonDisconnected {
		t.Fatalf("code=%q want %s", got.Code, ipc.CodeDaemonDisconnected)
	}
	if got.Message == "" || got.Fix == "" {
		t.Fatalf("want non-empty message/fix, got %+v", got)
	}
	if got.Fix != ipc.FixDaemonDisconnected {
		t.Fatalf("fix=%q want %q", got.Fix, ipc.FixDaemonDisconnected)
	}
	if strings.TrimSpace(got.Message) == "EOF" || strings.EqualFold(got.Message, "EOF") {
		t.Fatalf("message must not be bare EOF: %q", got.Message)
	}
}
