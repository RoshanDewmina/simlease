package logrotate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestWriteRotatesWhenExceedingMaxBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "simurghd.log")

	w, err := New(path, 64, 3)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close()

	// First write fits under maxBytes.
	if _, err := w.Write([]byte("aaaaaaaaaa")); err != nil { // 10 bytes
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("backup .1 should not exist yet: %v", err)
	}

	// Write that would push past maxBytes must rotate first.
	payload := []byte(strings.Repeat("b", 60))
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("Write (rotate): %v", err)
	}

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected rotated backup .1: %v", err)
	}
	cur, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read current: %v", err)
	}
	if string(cur) != string(payload) {
		t.Fatalf("current log = %q, want payload after rotation", cur)
	}
	old, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("read .1: %v", err)
	}
	if string(old) != "aaaaaaaaaa" {
		t.Fatalf(".1 content = %q, want pre-rotation bytes", old)
	}
}

func TestWriteDropsOldestBeyondMaxBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "simurghd.log")

	w, err := New(path, 10, 2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close()

	for i := 0; i < 4; i++ {
		msg := []byte(fmt.Sprintf("round-%d!!", i)) // 9 bytes each; next write always rotates
		if _, err := w.Write(msg); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
		// Force next write to rotate by filling to max.
		pad := make([]byte, 10-len(msg))
		for j := range pad {
			pad[j] = 'x'
		}
		if _, err := w.Write(pad); err != nil {
			t.Fatalf("pad %d: %v", i, err)
		}
	}

	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("backup .3 must be dropped when maxBackups=2; err=%v", err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected .1: %v", err)
	}
	if _, err := os.Stat(path + ".2"); err != nil {
		t.Fatalf("expected .2: %v", err)
	}
}

func TestConcurrentWritesRaceClean(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "simurghd.log")

	w, err := New(path, 1<<20, 3) // 1 MiB — no rotation needed for this test
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close()

	const goroutines = 8
	const writesPer = 200
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			line := []byte(fmt.Sprintf("g%d-xxxxxxxx\n", id))
			for i := 0; i < writesPer; i++ {
				if _, err := w.Write(line); err != nil {
					t.Errorf("Write: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	wantBytes := goroutines * writesPer * len([]byte("g0-xxxxxxxx\n"))
	if len(data) != wantBytes {
		t.Fatalf("wrote %d bytes, want %d (corruption or lost writes)", len(data), wantBytes)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != goroutines*writesPer {
		t.Fatalf("got %d lines, want %d (interleaved/corrupt lines)", len(lines), goroutines*writesPer)
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, "g") || !strings.HasSuffix(line, "xxxxxxxx") {
			t.Fatalf("line %d corrupted: %q", i, line)
		}
	}
}

func TestNewCreatesParentDirAndFileMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "logs", "simurghd.log")

	w, err := New(path, DefaultMaxBytes, DefaultMaxBackups)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close()

	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("parent dir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("parent dir perm = %04o, want 0700", perm)
	}
	finfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("log file: %v", err)
	}
	if perm := finfo.Mode().Perm(); perm != 0o600 {
		t.Fatalf("log file perm = %04o, want 0600", perm)
	}
}
