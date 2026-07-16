package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/RoshanDewmina/simurgh/internal/broker"
)

func matchLeaseOwner(lease broker.Lease, pid int, agent, sessionID string) bool {
	if lease.Owner.PID != 0 && lease.Owner.PID == pid {
		return true
	}
	if lease.Owner.Agent != "" && lease.Owner.SessionID != "" &&
		lease.Owner.Agent == agent && lease.Owner.SessionID == sessionID {
		return true
	}
	return false
}

func assertReleaseOwner(lease broker.Lease, force bool) error {
	if force || matchLeaseOwner(lease, os.Getpid(), cliAgentName, cliSessionID()) {
		return nil
	}
	return fmt.Errorf("lease %s owned by pid=%d agent=%q sessionId=%q",
		lease.ID, lease.Owner.PID, lease.Owner.Agent, lease.Owner.SessionID)
}

func verifyReleaseOwner(leaseID string, force, asJSON bool) int {
	if force {
		return 0
	}
	raw, err := dialAndCall(context.Background(), "lease.get", map[string]any{"id": leaseID})
	if err != nil {
		printIPCError(err, asJSON)
		return exitCodeForIPCError(err)
	}
	var wrap struct {
		Lease broker.Lease `json:"lease"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		fmt.Fprintf(os.Stderr, "simurgh release: decode: %v\n", err)
		return 5
	}
	if err := assertReleaseOwner(wrap.Lease, false); err != nil {
		printErrorTriple(
			"INVALID_INPUT",
			err.Error(),
			"pass --force to release a lease owned by another process/session",
			asJSON,
		)
		return 3
	}
	return 0
}
