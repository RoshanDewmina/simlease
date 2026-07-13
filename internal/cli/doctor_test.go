package cli

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// shortTempDir returns a short path under /tmp. macOS sun_path is ~104 bytes;
// t.TempDir() paths are often too long for Unix sockets.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "smg-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func mustSocket0600(t *testing.T, dir, name string) string {
	t.Helper()
	sock := filepath.Join(dir, name)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	if err := os.Chmod(sock, 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	return sock
}

func TestRunChecksAllGreen(t *testing.T) {
	sock := mustSocket0600(t, shortTempDir(t), "s.sock")

	checks := RunChecks(context.Background(), DoctorOptions{
		XcodeVersion: func(context.Context) (string, error) {
			return "Xcode 27.0\nBuild version 27A5194q", nil
		},
		SimctlRuntimes: func(context.Context) ([]string, error) {
			return []string{"iOS 27.0", "watchOS 27.0"}, nil
		},
		SocketPath: sock,
		DialPing:   func(context.Context) error { return nil },
	})

	if len(checks) != 5 {
		t.Fatalf("got %d checks, want 5", len(checks))
	}
	for _, c := range checks {
		if !c.OK {
			t.Fatalf("check %q not OK: code=%q message=%q fix=%q", c.Name, c.Code, c.Message, c.Fix)
		}
	}
	if ExitCode(checks) != 0 {
		t.Fatalf("ExitCode = %d, want 0", ExitCode(checks))
	}
}

func TestRunChecksCollectAllFailures(t *testing.T) {
	dir := shortTempDir(t)
	goodSock := mustSocket0600(t, dir, "good.sock")

	cases := []struct {
		name        string
		opts        DoctorOptions
		wantFailed  []string
		wantOKCount int
	}{
		{
			name: "xcode alone",
			opts: DoctorOptions{
				XcodeVersion: func(context.Context) (string, error) {
					return "", errors.New("xcodebuild: command not found")
				},
				SimctlRuntimes: func(context.Context) ([]string, error) {
					return []string{"iOS 27.0"}, nil
				},
				SocketPath: goodSock,
				DialPing:   func(context.Context) error { return nil },
			},
			wantFailed:  []string{"xcode"},
			wantOKCount: 4,
		},
		{
			name: "simctl probe fails both simctl and runtimes",
			opts: DoctorOptions{
				XcodeVersion: func(context.Context) (string, error) {
					return "Xcode 27.0", nil
				},
				SimctlRuntimes: func(context.Context) ([]string, error) {
					return nil, errors.New("xcrun: error: unable to find utility \"simctl\"")
				},
				SocketPath: goodSock,
				DialPing:   func(context.Context) error { return nil },
			},
			// Shared probe: one shell-out; both checks report the failure.
			wantFailed:  []string{"simctl", "runtimes"},
			wantOKCount: 3,
		},
		{
			name: "runtimes alone",
			opts: DoctorOptions{
				XcodeVersion: func(context.Context) (string, error) {
					return "Xcode 27.0", nil
				},
				SimctlRuntimes: func(context.Context) ([]string, error) {
					return []string{"watchOS 27.0"}, nil
				},
				SocketPath: goodSock,
				DialPing:   func(context.Context) error { return nil },
			},
			wantFailed:  []string{"runtimes"},
			wantOKCount: 4,
		},
		{
			name: "socket alone",
			opts: DoctorOptions{
				XcodeVersion: func(context.Context) (string, error) {
					return "Xcode 27.0", nil
				},
				SimctlRuntimes: func(context.Context) ([]string, error) {
					return []string{"iOS 27.0"}, nil
				},
				SocketPath: filepath.Join(dir, "missing.sock"),
				DialPing:   func(context.Context) error { return nil },
			},
			wantFailed:  []string{"socket"},
			wantOKCount: 4,
		},
		{
			name: "daemon alone",
			opts: DoctorOptions{
				XcodeVersion: func(context.Context) (string, error) {
					return "Xcode 27.0", nil
				},
				SimctlRuntimes: func(context.Context) ([]string, error) {
					return []string{"iOS 27.0"}, nil
				},
				SocketPath: goodSock,
				DialPing: func(context.Context) error {
					return errors.New("connection refused")
				},
			},
			wantFailed:  []string{"daemon"},
			wantOKCount: 4,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checks := RunChecks(context.Background(), tc.opts)
			if len(checks) != 5 {
				t.Fatalf("got %d checks, want 5 (collect-all)", len(checks))
			}
			failed := map[string]Check{}
			okCount := 0
			for _, c := range checks {
				if c.OK {
					okCount++
					continue
				}
				failed[c.Name] = c
				if c.Code == "" || c.Message == "" || c.Fix == "" {
					t.Fatalf("failure %q missing triple: %+v", c.Name, c)
				}
			}
			if okCount != tc.wantOKCount {
				t.Fatalf("ok count = %d, want %d; failed=%v", okCount, tc.wantOKCount, failedKeys(failed))
			}
			if len(failed) != len(tc.wantFailed) {
				t.Fatalf("failed checks = %v, want %v", failedKeys(failed), tc.wantFailed)
			}
			for _, name := range tc.wantFailed {
				if _, ok := failed[name]; !ok {
					t.Fatalf("missing failure for %q; failed=%v", name, failedKeys(failed))
				}
			}
			if ExitCode(checks) != 4 {
				t.Fatalf("ExitCode = %d, want 4", ExitCode(checks))
			}
		})
	}
}

func TestRunChecksDaemonFixIsDaemonRun(t *testing.T) {
	sock := mustSocket0600(t, shortTempDir(t), "s.sock")

	checks := RunChecks(context.Background(), DoctorOptions{
		XcodeVersion:   func(context.Context) (string, error) { return "Xcode 27.0", nil },
		SimctlRuntimes: func(context.Context) ([]string, error) { return []string{"iOS 27.0"}, nil },
		SocketPath:     sock,
		DialPing:       func(context.Context) error { return errors.New("dial unix: connection refused") },
	})
	var daemon Check
	for _, c := range checks {
		if c.Name == "daemon" {
			daemon = c
		}
	}
	if daemon.OK {
		t.Fatal("daemon check should fail")
	}
	if daemon.Code != CodeDaemonNotRunning {
		t.Fatalf("code = %q, want %q", daemon.Code, CodeDaemonNotRunning)
	}
	if daemon.Fix != FixDaemonNotRunning {
		t.Fatalf("fix = %q, want %q", daemon.Fix, FixDaemonNotRunning)
	}
}

func TestRunChecksRuntimesFixIsDownloadPlatform(t *testing.T) {
	sock := mustSocket0600(t, shortTempDir(t), "s.sock")

	checks := RunChecks(context.Background(), DoctorOptions{
		XcodeVersion:   func(context.Context) (string, error) { return "Xcode 27.0", nil },
		SimctlRuntimes: func(context.Context) ([]string, error) { return nil, nil },
		SocketPath:     sock,
		DialPing:       func(context.Context) error { return nil },
	})
	var runtimes Check
	for _, c := range checks {
		if c.Name == "runtimes" {
			runtimes = c
		}
	}
	if runtimes.OK {
		t.Fatal("runtimes check should fail")
	}
	if runtimes.Fix != FixIOSRuntimeMissing {
		t.Fatalf("fix = %q, want %q", runtimes.Fix, FixIOSRuntimeMissing)
	}
}

func TestRunChecksSimctlFailureStillReportsRuntimes(t *testing.T) {
	sock := mustSocket0600(t, shortTempDir(t), "s.sock")

	checks := RunChecks(context.Background(), DoctorOptions{
		XcodeVersion: func(context.Context) (string, error) { return "Xcode 27.0", nil },
		SimctlRuntimes: func(context.Context) ([]string, error) {
			return nil, errors.New("simctl boom")
		},
		SocketPath: sock,
		DialPing:   func(context.Context) error { return nil },
	})
	byName := map[string]Check{}
	for _, c := range checks {
		byName[c.Name] = c
	}
	if byName["simctl"].OK {
		t.Fatal("simctl should fail")
	}
	if byName["runtimes"].OK {
		t.Fatal("runtimes should still report failure when simctl list fails")
	}
	if !(byName["xcode"].OK && byName["socket"].OK && byName["daemon"].OK) {
		t.Fatalf("other checks should still run: %+v", checks)
	}
}

func TestExitCode(t *testing.T) {
	if got := ExitCode(nil); got != 0 {
		t.Fatalf("nil checks: ExitCode=%d want 0", got)
	}
	if got := ExitCode([]Check{{Name: "xcode", OK: true}}); got != 0 {
		t.Fatalf("all ok: ExitCode=%d want 0", got)
	}
	if got := ExitCode([]Check{
		{Name: "xcode", OK: true},
		{Name: "daemon", OK: false, Code: "X", Message: "m", Fix: "f"},
	}); got != 4 {
		t.Fatalf("one fail: ExitCode=%d want 4", got)
	}
}

func TestSocketWrongModeFails(t *testing.T) {
	dir := shortTempDir(t)
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	if err := os.Chmod(sock, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	checks := RunChecks(context.Background(), DoctorOptions{
		XcodeVersion:   func(context.Context) (string, error) { return "Xcode 27.0", nil },
		SimctlRuntimes: func(context.Context) ([]string, error) { return []string{"iOS 27.0"}, nil },
		SocketPath:     sock,
		DialPing:       func(context.Context) error { return nil },
	})
	var socket Check
	for _, c := range checks {
		if c.Name == "socket" {
			socket = c
		}
	}
	if socket.OK {
		t.Fatal("socket check should fail for mode != 0600")
	}
	if socket.Code == "" || socket.Fix == "" {
		t.Fatalf("socket failure missing triple: %+v", socket)
	}
}

func failedKeys(m map[string]Check) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
