package approval

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceAuthority(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "approvals.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	committed, err := replaceAuthority(path, []byte("new"))
	if !committed || err != nil {
		t.Fatalf("replace: committed=%v, err=%v", committed, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new" {
		t.Fatalf("read replacement: %q, %v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions: %o, want 600", info.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files remain: %v, %v", entries, err)
	}
}

func TestReplaceAuthorityBeforeRenameFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "approvals.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(path, "original")
	if err := os.WriteFile(marker, []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}
	committed, err := replaceAuthority(path, []byte("new"))
	if committed || err == nil {
		t.Fatalf("replace directory: committed=%v, err=%v", committed, err)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "preserved" {
		t.Fatalf("original changed: %q, %v", data, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files remain: %v, %v", entries, err)
	}
}

func TestReplaceAuthorityAfterRenameFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	fault := errors.New("directory sync fault")
	committed, err := replaceAuthorityWithSync(path, []byte("new"), func(string) error { return fault })
	if !committed || !errors.Is(err, fault) {
		t.Fatalf("commit boundary: %v, %v", committed, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new" {
		t.Fatalf("visible commit: %q, %v", data, err)
	}
}
