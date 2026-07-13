package safety

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/quick"
)

func TestValidateLeasePath_AcceptsValidID(t *testing.T) {
	root := t.TempDir()
	leaseDir := filepath.Join(root, "lease-1")
	if err := os.Mkdir(leaseDir, 0o700); err != nil {
		t.Fatal(err)
	}

	got, err := ValidateLeasePath(root, "lease-1")
	if err != nil {
		t.Fatalf("ValidateLeasePath: %v", err)
	}
	want, err := filepath.EvalSymlinks(leaseDir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestValidateLeasePath_RejectsAdversarialIDs(t *testing.T) {
	root := t.TempDir()
	cases := []string{
		"",
		".",
		"..",
		"../..",
		"../../../etc",
		"lease-1/../../evil",
		"lease-1\x00evil",
		"lease-",
		"lease-abc",
		"LEASE-1",
		"lease-1/",
		"/lease-1",
		"lease-1/evil",
		"xlease-1",
		"lease-1x",
		"lease 1",
		"lease-1.0",
	}
	for _, id := range cases {
		t.Run(sanitizeName(id), func(t *testing.T) {
			got, err := ValidateLeasePath(root, id)
			if err == nil {
				t.Fatalf("expected error, got path %q", got)
			}
			var inv *InvalidLeaseIDError
			if !errors.As(err, &inv) {
				t.Fatalf("want InvalidLeaseIDError, got %T: %v", err, err)
			}
			if got != "" {
				t.Fatalf("path must be empty on error, got %q", got)
			}
		})
	}
}

func TestValidateLeasePath_RejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	escapeTarget := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(escapeTarget, []byte("do-not-delete"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Plant a lease-looking symlink that points outside the leases root.
	link := filepath.Join(root, "lease-99")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	got, err := ValidateLeasePath(root, "lease-99")
	if err == nil {
		t.Fatalf("expected refusal for symlink escape, got %q", got)
	}
	var outsideErr *PathOutsideLeasesRootError
	if !errors.As(err, &outsideErr) {
		t.Fatalf("want PathOutsideLeasesRootError, got %T: %v", err, err)
	}

	// Confirm delete also refuses and does not touch the outside tree.
	if err := DeleteLeaseDir(root, "lease-99"); err == nil {
		t.Fatal("DeleteLeaseDir should refuse symlink escape")
	}
	if _, err := os.Stat(escapeTarget); err != nil {
		t.Fatalf("outside file was touched/removed: %v", err)
	}
}

func TestValidateLeasePath_RejectsResolvedEqualToRoot(t *testing.T) {
	root := t.TempDir()
	// Symlink whose resolved target is exactly the leases root.
	link := filepath.Join(root, "lease-42")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}

	got, err := ValidateLeasePath(root, "lease-42")
	if err == nil {
		t.Fatalf("must refuse path equal to leasesRoot, got %q", got)
	}
	var outsideErr *PathOutsideLeasesRootError
	if !errors.As(err, &outsideErr) {
		t.Fatalf("want PathOutsideLeasesRootError, got %T: %v", err, err)
	}
}

func TestValidateLeasePath_RejectsStringPrefixSibling(t *testing.T) {
	// leasesRoot=/a/leases must NOT accept /a/leases-evil
	parent := t.TempDir()
	root := filepath.Join(parent, "leases")
	evil := filepath.Join(parent, "leases-evil")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(evil, 0o700); err != nil {
		t.Fatal(err)
	}

	// Symlink lease-1 -> ../leases-evil (sibling that shares string prefix)
	link := filepath.Join(root, "lease-1")
	if err := os.Symlink(evil, link); err != nil {
		t.Fatal(err)
	}

	got, err := ValidateLeasePath(root, "lease-1")
	if err == nil {
		t.Fatalf("must refuse sibling prefix escape, got %q", got)
	}
	var outsideErr *PathOutsideLeasesRootError
	if !errors.As(err, &outsideErr) {
		t.Fatalf("want PathOutsideLeasesRootError, got %T: %v", err, err)
	}
}

func TestValidateLeasePath_MissingLeaseDirStillUnderRoot(t *testing.T) {
	root := t.TempDir()
	got, err := ValidateLeasePath(root, "lease-7")
	if err != nil {
		t.Fatalf("missing lease dir should still validate: %v", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(resolvedRoot, "lease-7")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if !strings.HasPrefix(got, resolvedRoot+string(os.PathSeparator)) {
		t.Fatalf("%q is not under %q", got, resolvedRoot)
	}
}

func TestDeleteLeaseDir_IdempotentAndScoped(t *testing.T) {
	root := t.TempDir()
	leaseDir := filepath.Join(root, "lease-3")
	if err := os.Mkdir(leaseDir, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(leaseDir, "marker.txt")
	if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(root, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := DeleteLeaseDir(root, "lease-3"); err != nil {
		t.Fatalf("DeleteLeaseDir: %v", err)
	}
	if _, err := os.Stat(leaseDir); !os.IsNotExist(err) {
		t.Fatalf("lease dir still present: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("sibling sentinel was touched: %v", err)
	}

	// Idempotent: missing path is not an error.
	if err := DeleteLeaseDir(root, "lease-3"); err != nil {
		t.Fatalf("second DeleteLeaseDir: %v", err)
	}
}

func TestValidateLeasePath_NeverEscapesOrPanics_Property(t *testing.T) {
	root := t.TempDir()
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	prefix := resolvedRoot + string(os.PathSeparator)

	f := func(id string) bool {
		defer func() {
			if rec := recover(); rec != nil {
				t.Errorf("panic on leaseID %q: %v", id, rec)
			}
		}()
		got, err := ValidateLeasePath(root, id)
		if err != nil {
			if got != "" {
				t.Errorf("on error path must be empty, got %q for id %q", got, id)
				return false
			}
			return true
		}
		if got == resolvedRoot {
			t.Errorf("returned leasesRoot itself for id %q", id)
			return false
		}
		if !strings.HasPrefix(got, prefix) {
			t.Errorf("escaped root: id=%q path=%q root=%q", id, got, resolvedRoot)
			return false
		}
		return true
	}

	cfg := &quick.Config{MaxCount: 500}
	if err := quick.Check(f, cfg); err != nil {
		t.Fatal(err)
	}
}

func sanitizeName(s string) string {
	if s == "" {
		return "empty"
	}
	var b strings.Builder
	for _, r := range s {
		if r < 32 || r > 126 {
			b.WriteByte('_')
			continue
		}
		switch r {
		case '/', '\\', ' ', ':':
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
