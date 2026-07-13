package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingJournalReturnsEmptySchemaV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := New(path)

	st, err := s.Load()
	if err != nil {
		t.Fatalf("Load missing journal: %v", err)
	}
	if st.SchemaVersion != SchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", st.SchemaVersion, SchemaVersion)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load must not create missing journal, stat err = %v", err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	s := New(path)

	want := State{SchemaVersion: SchemaVersion}
	if err := s.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.SchemaVersion != want.SchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", got.SchemaVersion, want.SchemaVersion)
	}
	if len(got.Leases) != 0 {
		t.Fatalf("Leases = %s, want empty/nil", got.Leases)
	}
}

func TestLeasesRoundTripOpaqueJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := New(path)

	payload := json.RawMessage(`{"lease-1":{"id":"lease-1","state":"active","device":{"udid":"AAAA"}}}`)
	if err := s.Save(State{SchemaVersion: SchemaVersion, Leases: payload}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.SchemaVersion != SchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", got.SchemaVersion, SchemaVersion)
	}
	var wantObj, gotObj any
	if err := json.Unmarshal(payload, &wantObj); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	if err := json.Unmarshal(got.Leases, &gotObj); err != nil {
		t.Fatalf("unmarshal got Leases: %v", err)
	}
	wantRaw, _ := json.Marshal(wantObj)
	gotRaw, _ := json.Marshal(gotObj)
	if string(wantRaw) != string(gotRaw) {
		t.Fatalf("Leases round-trip mismatch:\n got %s\nwant %s", got.Leases, payload)
	}
	// store must keep Leases opaque — typed as RawMessage, never interpreted.
	if got.Leases == nil {
		t.Fatal("Leases must be present after round-trip")
	}
}

func TestSaveReplacesExistingJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := New(path)

	if err := s.Save(State{SchemaVersion: SchemaVersion}); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"schemaVersion":1,"marker":"old"}`), 0o600); err != nil {
		t.Fatalf("seed old marker: %v", err)
	}
	if err := s.Save(State{SchemaVersion: SchemaVersion}); err != nil {
		t.Fatalf("replacement Save: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal replaced journal: %v", err)
	}
	if _, ok := payload["marker"]; ok {
		t.Fatalf("replacement left old marker in journal: %s", raw)
	}
	if int(payload["schemaVersion"].(float64)) != SchemaVersion {
		t.Fatalf("schemaVersion = %v, want %d", payload["schemaVersion"], SchemaVersion)
	}
}

func TestLoadCorruptJSONDoesNotOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	corrupt := []byte(`{"schemaVersion":`)
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatalf("seed corrupt journal: %v", err)
	}
	s := New(path)

	if _, err := s.Load(); err == nil {
		t.Fatal("Load corrupt journal: want error")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after Load: %v", err)
	}
	if string(got) != string(corrupt) {
		t.Fatalf("corrupt journal was modified:\n got %s\nwant %s", got, corrupt)
	}
}

func TestLoadUnsupportedSchemaDoesNotOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	unsupported := []byte(`{"schemaVersion":99}`)
	if err := os.WriteFile(path, unsupported, 0o600); err != nil {
		t.Fatalf("seed unsupported journal: %v", err)
	}
	s := New(path)

	if _, err := s.Load(); err == nil {
		t.Fatal("Load unsupported schema: want error")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after Load: %v", err)
	}
	if string(got) != string(unsupported) {
		t.Fatalf("unsupported journal was modified:\n got %s\nwant %s", got, unsupported)
	}
}

func TestReconcileIsNoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := New(path)
	if err := s.Save(State{SchemaVersion: SchemaVersion}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := s.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load after Reconcile: %v", err)
	}
	if got.SchemaVersion != SchemaVersion {
		t.Fatalf("Reconcile mutated state: %+v", got)
	}
}

func TestSaveCreatesOwnerOnlyPermissions(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "simurgh")
	path := filepath.Join(dir, "state.json")
	s := New(path)

	if err := s.Save(State{SchemaVersion: SchemaVersion}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("parent dir perm = %04o, want 0700", perm)
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Fatalf("state file perm = %04o, want 0600", perm)
	}
}

func TestFailedSavePreservesLastValidJournal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	s := New(path)

	if err := s.Save(State{SchemaVersion: SchemaVersion}); err != nil {
		t.Fatalf("initial Save: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile before failed Save: %v", err)
	}

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("Chmod read-only: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(dir, 0o700)
	})

	err = s.Save(State{SchemaVersion: SchemaVersion})
	if err == nil {
		t.Fatal("Save into read-only dir: want error")
	}

	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("Chmod restore: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after failed Save: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("failed Save mutated journal:\n got %s\nwant %s", after, before)
	}
}
