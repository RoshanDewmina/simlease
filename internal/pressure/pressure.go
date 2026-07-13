// Package pressure checks host RAM, thermal state, and disk free space before
// the broker grants a lease (ARCHITECTURE §4; ROADMAP M4).
//
// Design constraint: fail-open. If an individual signal cannot be read or
// parsed, that signal is treated as OK/non-blocking and the issue is logged.
// A detection bug must never silently block all real usage.
package pressure

import (
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

const (
	defaultMinRAMBytes  = 4 << 30 // 4 GiB — ARCHITECTURE §4
	defaultMinDiskBytes = 5 << 30 // 5 GiB — ROADMAP M4 disk addition
)

// Signal is one pressure check (RAM, thermal, or disk) for observability.
type Signal struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// Report combines all pressure signals. OK is the AND of signals that were
// determinable; Reason names failing signal(s) (empty when OK).
type Report struct {
	OK      bool     `json:"ok"`
	Reason  string   `json:"reason"`
	Signals []Signal `json:"signals"`
}

// runner executes a command and returns its stdout. Tests inject fakes.
type runner func(name string, args ...string) ([]byte, error)

var commandRunner runner = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

// CheckRAM reports whether free+inactive pages exceed minFreeBytes.
// A minFreeBytes of 0 uses the 4 GiB architecture default.
// Fail-open on exec/parse errors.
func CheckRAM(minFreeBytes uint64) Signal {
	if minFreeBytes == 0 {
		minFreeBytes = defaultMinRAMBytes
	}
	out, err := commandRunner("vm_stat")
	if err != nil {
		return failOpen("ram", err)
	}
	avail, err := parseVMStatAvailable(out)
	if err != nil {
		return failOpen("ram", err)
	}
	if avail < minFreeBytes {
		return Signal{
			Name:   "ram",
			OK:     false,
			Detail: fmt.Sprintf("available %d bytes (free+inactive) below minimum %d", avail, minFreeBytes),
		}
	}
	return Signal{
		Name:   "ram",
		OK:     true,
		Detail: fmt.Sprintf("available %d bytes (free+inactive); minimum %d", avail, minFreeBytes),
	}
}

// CheckThermal reports whether pmset therm indicates a nominal thermal state.
// Fail-open on exec/parse errors.
func CheckThermal() Signal {
	out, err := commandRunner("pmset", "-g", "therm")
	if err != nil {
		return failOpen("thermal", err)
	}
	ok, detail, err := parseTherm(out)
	if err != nil {
		return failOpen("thermal", err)
	}
	return Signal{Name: "thermal", OK: ok, Detail: detail}
}

// CheckDisk reports whether the filesystem containing path has at least
// minFreeBytes available. A minFreeBytes of 0 uses the 5 GiB default.
// Fail-open if Statfs itself errors (e.g. path missing on fresh install).
func CheckDisk(path string, minFreeBytes uint64) Signal {
	if minFreeBytes == 0 {
		minFreeBytes = defaultMinDiskBytes
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return failOpen("disk", err)
	}
	// Bavail * Bsize is available to non-root; portable enough on darwin.
	avail := uint64(st.Bavail) * uint64(st.Bsize)
	if avail < minFreeBytes {
		return Signal{
			Name:   "disk",
			OK:     false,
			Detail: fmt.Sprintf("available %d bytes below minimum %d", avail, minFreeBytes),
		}
	}
	return Signal{
		Name:   "disk",
		OK:     true,
		Detail: fmt.Sprintf("available %d bytes; minimum %d", avail, minFreeBytes),
	}
}

// Check runs RAM, thermal, and disk checks against leasesRoot's filesystem.
func Check(leasesRoot string) Report {
	signals := []Signal{
		CheckRAM(defaultMinRAMBytes),
		CheckThermal(),
		CheckDisk(leasesRoot, defaultMinDiskBytes),
	}
	ok := true
	var failing []string
	for _, s := range signals {
		if !s.OK {
			ok = false
			failing = append(failing, s.Name)
		}
	}
	reason := ""
	if !ok {
		reason = strings.Join(failing, ", ")
	}
	return Report{OK: ok, Reason: reason, Signals: signals}
}

func failOpen(name string, err error) Signal {
	detail := fmt.Sprintf("could not determine (treated as OK): %v", err)
	log.Printf("simurgh pressure: %s: %s", name, detail)
	return Signal{Name: name, OK: true, Detail: detail}
}

var (
	vmPageSizeRe = regexp.MustCompile(`page size of (\d+) bytes`)
	vmPagesRe    = regexp.MustCompile(`(?m)^Pages (free|inactive):\s+(\d+)\.?$`)
	thermLimitRe = regexp.MustCompile(`(?i)CPU_(?:Speed|Scheduler)_Limit\s*=\s*(\d+)`)
)

func parseVMStatAvailable(out []byte) (uint64, error) {
	text := string(out)
	if strings.TrimSpace(text) == "" {
		return 0, fmt.Errorf("empty vm_stat output")
	}
	m := vmPageSizeRe.FindStringSubmatch(text)
	if m == nil {
		return 0, fmt.Errorf("page size not found in vm_stat output")
	}
	pageSize, err := strconv.ParseUint(m[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("page size: %w", err)
	}
	var free, inactive uint64
	var sawFree, sawInactive bool
	for _, sm := range vmPagesRe.FindAllStringSubmatch(text, -1) {
		n, err := strconv.ParseUint(sm[2], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("pages %s: %w", sm[1], err)
		}
		switch sm[1] {
		case "free":
			free = n
			sawFree = true
		case "inactive":
			inactive = n
			sawInactive = true
		}
	}
	if !sawFree || !sawInactive {
		return 0, fmt.Errorf("missing Pages free/inactive in vm_stat output")
	}
	return (free + inactive) * pageSize, nil
}

func parseTherm(out []byte) (ok bool, detail string, err error) {
	text := string(out)
	if strings.TrimSpace(text) == "" {
		return false, "", fmt.Errorf("empty pmset therm output")
	}

	// Explicit speed/scheduler limits below 100 => non-nominal.
	for _, sm := range thermLimitRe.FindAllStringSubmatch(text, -1) {
		n, perr := strconv.Atoi(sm[1])
		if perr != nil {
			return false, "", fmt.Errorf("thermal limit: %w", perr)
		}
		if n < 100 {
			return false, fmt.Sprintf("CPU limit %d below 100 (thermal throttling)", n), nil
		}
	}

	// Nominal when Apple reports no thermal warning has been recorded.
	if strings.Contains(text, "No thermal warning level has been recorded") {
		return true, "thermal state nominal (no warning level recorded)", nil
	}

	// Limits all at 100 but no "No thermal warning..." line — still treat as
	// nominal if we saw at least one limit at 100.
	if thermLimitRe.MatchString(text) {
		return true, "thermal state nominal (CPU limits at 100)", nil
	}

	return false, "", fmt.Errorf("unrecognized pmset therm output")
}
