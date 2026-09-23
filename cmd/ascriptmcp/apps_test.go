package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindApp(t *testing.T) {
	root := t.TempDir()
	user := filepath.Join(root, "Applications")
	system := filepath.Join(root, "System")
	for _, p := range []string{
		filepath.Join(user, "Xcode.app"),
		filepath.Join(system, "TextEdit.app"),
		filepath.Join(system, "Xcode.app"),
	} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dirs := []string{user, filepath.Join(root, "missing"), system}

	tests := []struct {
		name   string
		want   string
		wantOK bool
	}{
		{"Xcode", filepath.Join(user, "Xcode.app"), true},
		{"TextEdit", filepath.Join(system, "TextEdit.app"), true},
		{"textedit", filepath.Join(system, "TextEdit.app"), true},
		{"Finder", "", false},
	}
	for _, tt := range tests {
		got, ok := findApp(dirs, tt.name)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("findApp(%q) = %q, %v; want %q, %v", tt.name, got, ok, tt.want, tt.wantOK)
		}
	}
}
