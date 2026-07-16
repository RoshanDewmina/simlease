// Package simurghhome resolves the on-disk Simurgh root (~/.simurgh or
// $SIMURGH_HOME) using passwd-home pinning with fail-closed HOME mismatch.
package simurghhome

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
)

// PasswdHomeDir returns the passwd database home directory.
func PasswdHomeDir() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	if u.HomeDir == "" {
		return "", fmt.Errorf("passwd entry has empty home directory")
	}
	return u.HomeDir, nil
}

// Dir resolves the on-disk root: $SIMURGH_HOME if set, else <passwd home>/.simurgh.
// When SIMURGH_HOME is unset and $HOME differs from the passwd home, resolution
// fails closed so state is not written under a polluted HOME.
func Dir() (string, error) {
	if h := os.Getenv("SIMURGH_HOME"); h != "" {
		return h, nil
	}
	passwdHome, err := PasswdHomeDir()
	if err != nil {
		return "", err
	}
	if envHome := os.Getenv("HOME"); envHome != "" && envHome != passwdHome {
		return "", fmt.Errorf(
			"HOME=%q differs from passwd home %q: export SIMURGH_HOME explicitly or unset HOME",
			envHome, passwdHome,
		)
	}
	return filepath.Join(passwdHome, ".simurgh"), nil
}
