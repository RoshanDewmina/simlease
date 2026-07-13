package pressure

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckDiskPassAndFailThresholds(t *testing.T) {
	dir := t.TempDir()

	ok := CheckDisk(dir, 1)
	if !ok.OK {
		t.Fatalf("1-byte threshold should pass: %+v", ok)
	}
	if ok.Name != "disk" {
		t.Fatalf("Name = %q want disk", ok.Name)
	}
	if !strings.Contains(ok.Detail, "available") {
		t.Fatalf("Detail should mention available bytes: %q", ok.Detail)
	}

	fail := CheckDisk(dir, 1<<60) // 1 exabyte — absurd on any real volume
	if fail.OK {
		t.Fatalf("1-exabyte threshold should fail: %+v", fail)
	}
	if fail.Name != "disk" {
		t.Fatalf("Name = %q want disk", fail.Name)
	}
}

func TestCheckDiskStatfsErrorFailsOpen(t *testing.T) {
	sig := CheckDisk("/no/such/simurgh/path/for/pressure/test", 1)
	if !sig.OK {
		t.Fatalf("Statfs error must fail open: %+v", sig)
	}
	if !strings.Contains(sig.Detail, "could not determine") {
		t.Fatalf("Detail = %q", sig.Detail)
	}
}

func TestCheckRAMParsesRealShapedOutput(t *testing.T) {
	defer setRunner(func(name string, args ...string) ([]byte, error) {
		if name != "vm_stat" {
			t.Fatalf("unexpected command %s %v", name, args)
		}
		return []byte(`Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                               300000.
Pages active:                             100000.
Pages inactive:                           200000.
Pages speculative:                           100.
Pages wired down:                         100000.
`), nil
	})()

	// (300000+200000)*16384 = 8_192_000_000 > 4GiB
	sig := CheckRAM(4 << 30)
	if !sig.OK {
		t.Fatalf("expected OK: %+v", sig)
	}
	if sig.Name != "ram" {
		t.Fatalf("Name = %q", sig.Name)
	}
	if !strings.Contains(sig.Detail, "8192000000") && !strings.Contains(sig.Detail, "8") {
		t.Fatalf("Detail should include available bytes: %q", sig.Detail)
	}
}

func TestCheckRAMBelowThreshold(t *testing.T) {
	defer setRunner(func(name string, args ...string) ([]byte, error) {
		return []byte(`Mach Virtual Memory Statistics: (page size of 4096 bytes)
Pages free:                                  100.
Pages inactive:                              100.
`), nil
	})()

	sig := CheckRAM(4 << 30)
	if sig.OK {
		t.Fatalf("expected not OK for tiny free+inactive: %+v", sig)
	}
}

func TestCheckRAMGarbageFailsOpen(t *testing.T) {
	defer setRunner(func(string, ...string) ([]byte, error) {
		return []byte("not vm_stat output at all"), nil
	})()

	sig := CheckRAM(4 << 30)
	if !sig.OK {
		t.Fatalf("garbage must fail open: %+v", sig)
	}
	if !strings.Contains(sig.Detail, "could not determine") {
		t.Fatalf("Detail = %q", sig.Detail)
	}
}

func TestCheckRAMEmptyFailsOpen(t *testing.T) {
	defer setRunner(func(string, ...string) ([]byte, error) {
		return []byte{}, nil
	})()

	sig := CheckRAM(4 << 30)
	if !sig.OK {
		t.Fatalf("empty must fail open: %+v", sig)
	}
}

func TestCheckRAMCommandErrorFailsOpen(t *testing.T) {
	defer setRunner(func(string, ...string) ([]byte, error) {
		return nil, errors.New("exec: \"vm_stat\": executable file not found in $PATH")
	})()

	sig := CheckRAM(4 << 30)
	if !sig.OK {
		t.Fatalf("command error must fail open: %+v", sig)
	}
	if !strings.Contains(sig.Detail, "could not determine") {
		t.Fatalf("Detail = %q", sig.Detail)
	}
}

func TestCheckThermalNominal(t *testing.T) {
	defer setRunner(func(name string, args ...string) ([]byte, error) {
		if name != "pmset" || len(args) != 2 || args[0] != "-g" || args[1] != "therm" {
			t.Fatalf("unexpected command %s %v", name, args)
		}
		return []byte(`Note: No thermal warning level has been recorded
Note: No performance warning level has been recorded
Note: No CPU power status has been recorded
`), nil
	})()

	sig := CheckThermal()
	if !sig.OK {
		t.Fatalf("nominal therm must be OK: %+v", sig)
	}
	if sig.Name != "thermal" {
		t.Fatalf("Name = %q", sig.Name)
	}
}

func TestCheckThermalSpeedLimitBelow100(t *testing.T) {
	defer setRunner(func(string, ...string) ([]byte, error) {
		return []byte(`Note: No thermal warning level has been recorded
Note: No performance warning level has been recorded
2021-09-03 13:22:05 +0900 CPU Power notify
	CPU_Scheduler_Limit 	= 100
	CPU_Available_CPUs 	= 16
	CPU_Speed_Limit 	= 34
`), nil
	})()

	sig := CheckThermal()
	if sig.OK {
		t.Fatalf("CPU_Speed_Limit 34 must be non-nominal: %+v", sig)
	}
	if !strings.Contains(sig.Detail, "34") {
		t.Fatalf("Detail should cite limit: %q", sig.Detail)
	}
}

func TestCheckThermalIgnoresAvailableCPUsCount(t *testing.T) {
	defer setRunner(func(string, ...string) ([]byte, error) {
		return []byte(`Note: No thermal warning level has been recorded
	CPU_Scheduler_Limit 	= 100
	CPU_Available_CPUs 	= 8
	CPU_Speed_Limit 	= 100
`), nil
	})()

	sig := CheckThermal()
	if !sig.OK {
		t.Fatalf("CPU_Available_CPUs=8 must not trip limit check: %+v", sig)
	}
}

func TestCheckThermalGarbageFailsOpen(t *testing.T) {
	defer setRunner(func(string, ...string) ([]byte, error) {
		return []byte("???"), nil
	})()

	sig := CheckThermal()
	if !sig.OK {
		t.Fatalf("garbage must fail open: %+v", sig)
	}
}

func TestCheckThermalCommandErrorFailsOpen(t *testing.T) {
	defer setRunner(func(string, ...string) ([]byte, error) {
		return nil, errors.New("exec: \"pmset\": executable file not found in $PATH")
	})()

	sig := CheckThermal()
	if !sig.OK {
		t.Fatalf("command error must fail open: %+v", sig)
	}
}

func TestCheckCombinesSignals(t *testing.T) {
	dir := t.TempDir()
	defer setRunner(func(name string, args ...string) ([]byte, error) {
		switch name {
		case "vm_stat":
			return []byte(`Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                               300000.
Pages inactive:                           200000.
`), nil
		case "pmset":
			return []byte("Note: No thermal warning level has been recorded\n"), nil
		default:
			return nil, errors.New("unexpected " + name)
		}
	})()

	r := Check(dir)
	if !r.OK {
		t.Fatalf("expected OK report: %+v", r)
	}
	if r.Reason != "" {
		t.Fatalf("Reason = %q want empty", r.Reason)
	}
	if len(r.Signals) != 3 {
		t.Fatalf("Signals len = %d want 3", len(r.Signals))
	}
}

func TestCheckReasonNamesFailingSignals(t *testing.T) {
	dir := t.TempDir()
	defer setRunner(func(name string, args ...string) ([]byte, error) {
		switch name {
		case "vm_stat":
			return []byte(`Mach Virtual Memory Statistics: (page size of 4096 bytes)
Pages free:                                    1.
Pages inactive:                                1.
`), nil
		case "pmset":
			return []byte(`CPU_Speed_Limit = 50
`), nil
		default:
			return nil, errors.New("unexpected " + name)
		}
	})()

	r := Check(dir)
	if r.OK {
		t.Fatalf("expected not OK: %+v", r)
	}
	if !strings.Contains(r.Reason, "ram") {
		t.Fatalf("Reason should name ram: %q", r.Reason)
	}
	if !strings.Contains(r.Reason, "thermal") {
		t.Fatalf("Reason should name thermal: %q", r.Reason)
	}
}

// setRunner installs r as the package command runner and returns a restore func.
func setRunner(r runner) func() {
	prev := commandRunner
	commandRunner = r
	return func() { commandRunner = prev }
}
