package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/RoshanDewmina/simurgh/internal/mcp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// runMCP starts the stdio MCP server and blocks until stdin closes or a signal.
func runMCP() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := mcp.NewServer()
	if err := srv.Run(ctx, &mcpsdk.StdioTransport{}); err != nil {
		fmt.Fprintf(os.Stderr, "simurgh mcp: %v\n", err)
		return 1
	}
	return 0
}
