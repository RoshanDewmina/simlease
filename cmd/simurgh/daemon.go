package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/RoshanDewmina/simurgh/internal/ipc"
)

// simurghHomeDir resolves the on-disk root: $SIMURGH_HOME if set, else ~/.simurgh.
func simurghHomeDir() (string, error) {
	if h := os.Getenv("SIMURGH_HOME"); h != "" {
		return h, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".simurgh"), nil
}

// DefaultSocketPath returns the production Unix socket path.
func DefaultSocketPath() (string, error) {
	home, err := simurghHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "simurghd.sock"), nil
}

// DefaultLogPath returns the production daemon log path.
func DefaultLogPath() (string, error) {
	home, err := simurghHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "logs", "simurghd.log"), nil
}

func runDaemon() int {
	socketPath, err := DefaultSocketPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "simurgh daemon run: %v\n", err)
		return 1
	}

	ln, err := ipc.Listen(socketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "simurgh daemon run: %v\n", err)
		return 1
	}
	defer ln.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := ipc.NewServer(ln)
	if err := srv.Serve(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "simurgh daemon run: %v\n", err)
		return 1
	}
	return 0
}
