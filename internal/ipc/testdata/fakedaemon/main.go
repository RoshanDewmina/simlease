package main

import (
	"net"
	"os"
	"path/filepath"
	"time"
)

// fakedaemon is a simurgh-shaped helper used by client_test auto-start tests.
// It understands only `daemon run` and listens on $SIMURGH_HOME/simurghd.sock.
func main() {
	if len(os.Args) < 3 || os.Args[1] != "daemon" || os.Args[2] != "run" {
		os.Exit(2)
	}
	home := os.Getenv("SIMURGH_HOME")
	if home == "" {
		os.Exit(3)
	}
	if os.Getenv("FAKE_DAEMON_HANG") == "1" {
		// Spawn succeeds but never creates a socket — exercises retry failure.
		time.Sleep(5 * time.Second)
		return
	}
	path := filepath.Join(home, "simurghd.sock")
	_ = os.MkdirAll(home, 0o700)
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		os.Exit(4)
	}
	defer ln.Close()
	_ = os.Chmod(path, 0o600)

	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		_ = conn.Close()
	}
}
