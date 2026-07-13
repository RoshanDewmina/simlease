// Package logrotate provides a size-based rotating io.Writer for daemon logs.
// No external dependencies — rotation is a bounded, well-understood problem.
package logrotate

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Default rotation knobs for the daemon log at DefaultLogPath().
const (
	DefaultMaxBytes   int64 = 10 << 20 // 10 MiB
	DefaultMaxBackups       = 3
)

// Writer is a mutex-guarded, size-based rotating file writer.
// Before each Write, if size+len(p) would exceed maxBytes, it rotates:
// path → path.1, path.1 → path.2, … up to maxBackups; the oldest is dropped.
type Writer struct {
	path       string
	maxBytes   int64
	maxBackups int

	mu   sync.Mutex
	file *os.File
	size int64
}

// New opens or creates path (mode 0600) after ensuring the parent directory
// exists (mode 0700). maxBytes and maxBackups must be positive.
func New(path string, maxBytes int64, maxBackups int) (*Writer, error) {
	if path == "" {
		return nil, fmt.Errorf("logrotate: path is empty")
	}
	if maxBytes <= 0 {
		return nil, fmt.Errorf("logrotate: maxBytes must be positive")
	}
	if maxBackups <= 0 {
		return nil, fmt.Errorf("logrotate: maxBackups must be positive")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("logrotate: mkdir %s: %w", dir, err)
	}
	w := &Writer{
		path:       path,
		maxBytes:   maxBytes,
		maxBackups: maxBackups,
	}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

// Write implements io.Writer. It is safe for concurrent use.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return 0, fmt.Errorf("logrotate: writer is closed")
	}
	if int64(len(p)) > 0 && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotateLocked(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// Close closes the underlying file. Further Writes return an error.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *Writer) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("logrotate: open %s: %w", w.path, err)
	}
	// Enforce 0600 even if the file already existed with looser perms.
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("logrotate: chmod %s: %w", w.path, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("logrotate: stat %s: %w", w.path, err)
	}
	w.file = f
	w.size = info.Size()
	return nil
}

func (w *Writer) rotateLocked() error {
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("logrotate: close before rotate: %w", err)
	}
	w.file = nil

	// Drop the oldest backup first, then shift N → N+1 downward.
	oldest := fmt.Sprintf("%s.%d", w.path, w.maxBackups)
	_ = os.Remove(oldest)
	for i := w.maxBackups - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", w.path, i)
		dst := fmt.Sprintf("%s.%d", w.path, i+1)
		if err := os.Rename(src, dst); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("logrotate: rename %s → %s: %w", src, dst, err)
		}
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("logrotate: rename %s → %s.1: %w", w.path, w.path, err)
	}
	return w.open()
}
