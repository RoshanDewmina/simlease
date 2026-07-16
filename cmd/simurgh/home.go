package main

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
)

// passwdHomeDir returns the passwd database home directory.
func passwdHomeDir() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	if u.HomeDir == "" {
		return "", fmt.Errorf("passwd entry has empty home directory")
	}
	return u.HomeDir, nil
}

// simurghHomeDir resolves the on-disk root: $SIMURGH_HOME if set, else
// <passwd home>/.simurgh. When SIMURGH_HOME is unset and $HOME differs from
// the passwd home, resolution fails closed so state is not written under a
// polluted HOME.
func simurghHomeDir() (string, error) {
	if h := os.Getenv("SIMURGH_HOME"); h != "" {
		return h, nil
	}
	passwdHome, err := passwdHomeDir()
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
