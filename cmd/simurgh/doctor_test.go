package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RoshanDewmina/simurgh/internal/ipc"
)

func TestDialPingDaemonDisconnectOnEOF(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "smg-doctor-eof-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
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
		// Drop after doctor.ping request so the response read sees EOF.
	}()

	err = dialPing(socketPath)(context.Background())
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
	if strings.TrimSpace(err.Error()) == "EOF" {
		t.Fatalf("error must not be bare EOF: %v", err)
	}
}
