package simurghhome

import (
	"path/filepath"
	"testing"
)

func TestDirHonorsSimurghHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SIMURGH_HOME", dir)
	t.Setenv("HOME", "/polluted/home")
	got, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("Dir() = %q want %q", got, dir)
	}
}

func TestDirUsesPasswdHome(t *testing.T) {
	passwd, err := PasswdHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SIMURGH_HOME", "")
	t.Setenv("HOME", passwd)
	got, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(passwd, ".simurgh")
	if got != want {
		t.Fatalf("Dir() = %q want %q", got, want)
	}
}

func TestDirFailsClosedOnHomeMismatch(t *testing.T) {
	passwd, err := PasswdHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if passwd == "/tmp/polluted" {
		t.Skip("cannot construct mismatch against passwd home")
	}
	t.Setenv("SIMURGH_HOME", "")
	t.Setenv("HOME", "/tmp/polluted")
	_, err = Dir()
	if err == nil {
		t.Fatal("expected error when HOME differs from passwd home")
	}
}
