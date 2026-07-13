package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// SchemaVersion is the only journal envelope version supported in M0.
const SchemaVersion = 1

// State is the daemon-owned journal envelope. M0 keeps the payload minimal:
// schema version only; lease/broker fields arrive in later milestones.
type State struct {
	SchemaVersion int `json:"schemaVersion"`
}

// Store persists State at an injected journal path. The daemon is the single
// writer; methods are mutex-guarded so concurrent access is race-safe.
type Store struct {
	path string
	mu   sync.Mutex
}

// New returns a Store rooted at path. Tests must inject a temp path so the
// real ~/.simurgh journal is never touched.
func New(path string) *Store {
	return &Store{path: path}
}

// Path returns the configured journal path.
func (s *Store) Path() string {
	return s.path
}

// Load reads the journal. A missing file is a valid empty schema-v1 state and
// does not create the file. Corrupt JSON and unsupported schema versions are
// rejected without modifying the existing file.
func (s *Store) Load() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return State{SchemaVersion: SchemaVersion}, nil
	}
	if err != nil {
		return State{}, err
	}

	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return State{}, fmt.Errorf("store: corrupt state journal: %w", err)
	}
	if st.SchemaVersion != SchemaVersion {
		return State{}, fmt.Errorf("store: unsupported schemaVersion %d (want %d)", st.SchemaVersion, SchemaVersion)
	}
	return st, nil
}

// Save atomically rewrites the journal: temp file in the destination
// directory, flush, close, then rename. The parent directory is created with
// owner-only permissions when missing.
func (s *Store) Save(st State) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if st.SchemaVersion != SchemaVersion {
		return fmt.Errorf("store: unsupported schemaVersion %d (want %d)", st.SchemaVersion, SchemaVersion)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, "state.json.*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return err
	}
	ok = true
	return nil
}

// Reconcile is the daemon startup seam that will later compare the journal
// against simctl reality. M0 reconciliation is a documented no-op and must
// not invoke Apple tooling.
func (s *Store) Reconcile() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return nil
}
