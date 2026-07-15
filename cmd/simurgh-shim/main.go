// Command simurgh-shim is the audit-mode PATH shim binary for
// xcodebuild/xcrun (R-WP5 stage 1 of 3). It is never invoked by its own
// name in normal use — `simurgh shell <session-id>` creates two symlinks
// named exactly "xcodebuild" and "xcrun" that both point at this one
// binary; behavior is selected by filepath.Base(os.Args[0]). See
// internal/integrations/pathshim's package doc for the full design
// rationale (why one binary, why argv[0] dispatch, why absolute-path
// resolution, why the recursion guard).
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/RoshanDewmina/simurgh/internal/integrations/pathshim"
)

func main() {
	os.Exit(run(os.Args, os.Environ()))
}

func run(args, environ []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "simurgh-shim: empty argv")
		return 2
	}
	name := filepath.Base(args[0])
	switch name {
	case "xcodebuild", "xcrun":
	default:
		// Invoked under an unexpected name (e.g. run directly as
		// "simurgh-shim" rather than via one of the two session symlinks).
		// Refuse rather than guessing which real tool was meant — audit mode
		// promises transparent passthrough for exactly xcodebuild/xcrun,
		// nothing else.
		fmt.Fprintf(os.Stderr,
			"simurgh-shim: invoked as %q — expected to be invoked via a symlink "+
				"named exactly \"xcodebuild\" or \"xcrun\" (see `simurgh shell -h`)\n", name)
		return 2
	}

	// Build the Resolver's Getenv from the SAME environ slice passed in
	// (and set as RunOptions.Environ below), not the real process
	// os.Getenv, so this function has exactly one source of truth for
	// "what environment is this invocation running under" — the argument,
	// not ambient global state. In production this is a no-op (main()
	// passes os.Environ(), which os.Getenv already reflects), but it keeps
	// run() itself fully deterministic and testable without a subprocess.
	defaultResolver := pathshim.DefaultResolver()
	resolver := pathshim.Resolver{
		Getenv:          func(key string) string { return pathshim.LookupEnv(environ, key) },
		XcodeSelectPath: defaultResolver.XcodeSelectPath,
		Stat:            defaultResolver.Stat,
	}

	opts := pathshim.RunOptions{
		ShimName: name,
		Args:     args[1:],
		Environ:  environ,
		Resolver: resolver,
		Exec:     pathshim.DefaultExecFunc(context.Background()),
	}
	return pathshim.Run(opts)
}
