package xcodebuildmcp

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// RecordVersion probes the installed XcodeBuildMCP version string.
func RecordVersion(command []string) (string, error) {
	argv := command
	if len(argv) == 0 {
		argv = []string{"npx", "-y", "xcodebuildmcp@" + DefaultPinnedVersion, "--version"}
	} else {
		argv = append(append([]string(nil), argv...), "--version")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("xcodebuildmcp version: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	v := strings.TrimSpace(stdout.String())
	if v == "" {
		v = strings.TrimSpace(stderr.String())
	}
	// Prefer the first line / last semver-looking token.
	for _, line := range strings.Split(v, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line, nil
		}
	}
	return "", fmt.Errorf("xcodebuildmcp version: empty output")
}
