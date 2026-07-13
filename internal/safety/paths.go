package safety

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// leaseIDPattern is the broker's lease ID shape: "lease-" + decimal digits.
// Anything else (path separators, "..", empty, unicode, null bytes) is rejected
// before any filesystem join — the allowlist is the first deletion gate.
var leaseIDPattern = regexp.MustCompile(`^lease-[0-9]+$`)

// ValidateLeasePath is the ONLY way to obtain a deletable filesystem path.
// It takes an injected leasesRoot (never resolves ~/.simurgh itself) and a
// leaseID, validates the ID against the broker allowlist, resolves symlinks,
// and requires the resolved target to be a strict child of the resolved
// leases root (path-separator-bounded prefix; never equal to the root).
func ValidateLeasePath(leasesRoot, leaseID string) (string, error) {
	if !leaseIDPattern.MatchString(leaseID) {
		return "", invalidLeaseID(leaseID)
	}

	absRoot, err := filepath.Abs(leasesRoot)
	if err != nil {
		return "", pathOutsideLeasesRoot(leasesRoot, "", "cannot absolutize leases root: "+err.Error())
	}
	resolvedRoot, err := resolvePath(absRoot)
	if err != nil {
		return "", pathOutsideLeasesRoot(absRoot, "", "cannot resolve leases root: "+err.Error())
	}

	joined := filepath.Join(absRoot, leaseID)
	// Defensive: Join should be a no-op clean for a validated ID, but never
	// trust that the joined path stayed a single path element.
	if filepath.Base(joined) != leaseID {
		return "", invalidLeaseID(leaseID)
	}

	resolvedTarget, err := resolvePath(joined)
	if err != nil {
		return "", pathOutsideLeasesRoot(resolvedRoot, joined, "cannot resolve lease path: "+err.Error())
	}

	if !isStrictChild(resolvedRoot, resolvedTarget) {
		return "", pathOutsideLeasesRoot(resolvedRoot, resolvedTarget,
			"resolved path is not a strict child of the leases root")
	}
	return resolvedTarget, nil
}

// DeleteLeaseDir deletes a single per-lease directory under leasesRoot.
// It always calls ValidateLeasePath first — there is no raw-path overload.
// A missing path is not an error (idempotent).
func DeleteLeaseDir(leasesRoot, leaseID string) error {
	target, err := ValidateLeasePath(leasesRoot, leaseID)
	if err != nil {
		return err
	}
	err = os.RemoveAll(target)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// isStrictChild reports whether target is strictly under root using a
// path-separator-bounded prefix. root itself is rejected; a sibling that
// merely shares a string prefix (e.g. /a/leases-evil vs /a/leases) is rejected.
func isStrictChild(root, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	if root == target {
		return false
	}
	prefix := root + string(os.PathSeparator)
	return strings.HasPrefix(target, prefix)
}

// resolvePath returns an absolute, symlink-resolved path. If the final path
// does not exist yet, existing parents are resolved and the missing suffix is
// re-joined (so ValidateLeasePath works for not-yet-created lease dirs).
func resolvePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	current := abs
	var missing []string
	for {
		fi, err := os.Lstat(current)
		if err == nil {
			_ = fi
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}
