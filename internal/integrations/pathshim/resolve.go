package pathshim

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Resolver resolves the real, absolute path to xcodebuild/xcrun. Every
// field is injectable so tests never depend on a real Xcode install; a
// nil field falls back to the real OS mechanism (see the fallbacks in
// Resolve). DefaultResolver wires the real thing.
type Resolver struct {
	// Getenv reads an environment variable. Defaults to os.Getenv.
	Getenv func(key string) string
	// XcodeSelectPath returns the active developer directory (what a real
	// `xcode-select -p` would print), used only when DEVELOPER_DIR is
	// unset/empty. Defaults to actually running `xcode-select -p`.
	XcodeSelectPath func() (string, error)
	// Stat checks the candidate real-binary path exists. Defaults to os.Stat.
	Stat func(path string) (os.FileInfo, error)
	// SystemBinDir is the absolute fallback directory tried when
	// <developer-dir>/usr/bin/<name> is missing. Defaults to "/usr/bin".
	// Apple ships /usr/bin/xcrun as the public entry point; some Xcode
	// trees (notably Xcode-beta) omit Developer/usr/bin/xcrun entirely.
	// Always an absolute directory — never a PATH lookup.
	SystemBinDir string
}

// DefaultResolver wires Resolver to the real environment and xcode-select.
func DefaultResolver() Resolver {
	return Resolver{
		Getenv: os.Getenv,
		XcodeSelectPath: func() (string, error) {
			out, err := exec.Command("xcode-select", "-p").Output()
			if err != nil {
				return "", fmt.Errorf("pathshim: xcode-select -p: %w", err)
			}
			return strings.TrimSpace(string(out)), nil
		},
		Stat:         os.Stat,
		SystemBinDir: "/usr/bin",
	}
}

// Resolve returns the absolute path to the real binary named name
// ("xcodebuild" or "xcrun"). It NEVER uses a bare-name PATH lookup — see
// the package doc for why that specific shortcut is the recursion risk
// this whole package exists to avoid. Resolution order: DEVELOPER_DIR env
// var if non-empty, else `xcode-select -p`; prefer
// "<developer-dir>/usr/bin/<name>", then fall back to
// "<SystemBinDir>/<name>" (default /usr/bin/<name>) when the developer-dir
// copy is absent.
func (r Resolver) Resolve(name string) (string, error) {
	if name != "xcodebuild" && name != "xcrun" {
		return "", fmt.Errorf("pathshim: unsupported shim name %q (must be xcodebuild or xcrun)", name)
	}

	getenv := r.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	stat := r.Stat
	if stat == nil {
		stat = os.Stat
	}

	devDir := getenv("DEVELOPER_DIR")
	if devDir == "" {
		xsp := r.XcodeSelectPath
		if xsp == nil {
			return "", fmt.Errorf("pathshim: DEVELOPER_DIR is unset and no xcode-select resolver is configured")
		}
		p, err := xsp()
		if err != nil {
			return "", fmt.Errorf("pathshim: resolve developer dir: %w", err)
		}
		devDir = p
	}
	if devDir == "" {
		return "", fmt.Errorf("pathshim: resolved developer directory is empty")
	}

	candidate := filepath.Join(devDir, "usr", "bin", name)
	if _, err := stat(candidate); err != nil {
		sysDir := r.SystemBinDir
		if sysDir == "" {
			sysDir = "/usr/bin"
		}
		fallback := filepath.Join(sysDir, name)
		if _, err2 := stat(fallback); err2 != nil {
			return "", fmt.Errorf("pathshim: real %s not found at %s: %w", name, candidate, err)
		}
		candidate = fallback
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("pathshim: absolutize %s: %w", candidate, err)
	}
	return abs, nil
}
