package pathshim

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// InstallSessionShims creates <paths.BinDir>/xcodebuild and .../xcrun symlinks
// that both point at shimBinary. Idempotent: replaces existing symlinks.
// Refuses to clobber a non-symlink file at either link path.
func InstallSessionShims(paths SessionPaths, shimBinary string) error {
	if shimBinary == "" {
		return fmt.Errorf("pathshim: shim binary path is required")
	}
	absShim, err := filepath.Abs(shimBinary)
	if err != nil {
		return fmt.Errorf("pathshim: absolutize shim binary %q: %w", shimBinary, err)
	}
	st, err := os.Stat(absShim)
	if err != nil {
		return fmt.Errorf("pathshim: shim binary %q: %w", absShim, err)
	}
	if st.IsDir() {
		return fmt.Errorf("pathshim: shim binary %q is a directory", absShim)
	}
	if err := os.MkdirAll(paths.BinDir, 0o700); err != nil {
		return fmt.Errorf("pathshim: mkdir %s: %w", paths.BinDir, err)
	}
	for _, name := range []string{"xcodebuild", "xcrun"} {
		link := filepath.Join(paths.BinDir, name)
		if err := replaceWithSymlink(link, absShim); err != nil {
			return err
		}
	}
	return nil
}

func replaceWithSymlink(link, target string) error {
	fi, err := os.Lstat(link)
	if err == nil {
		if fi.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("pathshim: refuse to replace %s — not a symlink", link)
		}
		if err := os.Remove(link); err != nil {
			return fmt.Errorf("pathshim: remove old symlink %s: %w", link, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("pathshim: lstat %s: %w", link, err)
	}
	if err := os.Symlink(target, link); err != nil {
		return fmt.Errorf("pathshim: symlink %s -> %s: %w", link, target, err)
	}
	return nil
}

// PrependPathToEnv returns a copy of environ with dir prepended to PATH.
// If PATH is absent, sets PATH=dir.
func PrependPathToEnv(environ []string, dir string) []string {
	if dir == "" {
		return append([]string(nil), environ...)
	}
	out := make([]string, 0, len(environ)+1)
	found := false
	for _, kv := range environ {
		if strings.HasPrefix(kv, "PATH=") {
			found = true
			rest := kv[len("PATH="):]
			if rest == "" {
				out = append(out, "PATH="+dir)
			} else {
				out = append(out, "PATH="+dir+string(os.PathListSeparator)+rest)
			}
			continue
		}
		out = append(out, kv)
	}
	if !found {
		out = append(out, "PATH="+dir)
	}
	return out
}
