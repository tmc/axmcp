package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTreeTextIndexedIncludesIndexAndActions(t *testing.T) {
	checked := true
	nodes := []axTreeNode{
		{Index: 0, ParentIndex: -1, Role: "AXWindow", Title: "Browser", Width: 800, Height: 600},
		{Index: 1, ParentIndex: 0, Depth: 1, Role: "AXSwitch", Value: "1", Checked: &checked, State: "on", SecondaryActions: []string{"AXPress"}},
	}

	got := treeTextIndexed(nodes)
	for _, want := range []string{
		`[0] AXWindow title="Browser"`,
		`  [1] AXSwitch state="on" value="1" actions="AXPress"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("treeTextIndexed missing %q in %q", want, got)
		}
	}
}

func TestCheckedStateFromValue(t *testing.T) {
	tests := []struct {
		role        string
		value       string
		wantChecked *bool
		wantState   string
	}{
		{role: "AXSwitch", value: "1", wantChecked: boolPtr(true), wantState: "on"},
		{role: "AXCheckBox", value: "0", wantChecked: boolPtr(false), wantState: "off"},
		{role: "AXButton", value: "1"},
	}
	for _, tt := range tests {
		gotChecked, gotState := checkedStateFromValue(tt.role, tt.value)
		if gotState != tt.wantState {
			t.Fatalf("checkedStateFromValue(%q, %q) state = %q, want %q", tt.role, tt.value, gotState, tt.wantState)
		}
		switch {
		case tt.wantChecked == nil && gotChecked != nil:
			t.Fatalf("checkedStateFromValue(%q, %q) checked = %v, want nil", tt.role, tt.value, *gotChecked)
		case tt.wantChecked != nil && gotChecked == nil:
			t.Fatalf("checkedStateFromValue(%q, %q) checked = nil, want %v", tt.role, tt.value, *tt.wantChecked)
		case tt.wantChecked != nil && *gotChecked != *tt.wantChecked:
			t.Fatalf("checkedStateFromValue(%q, %q) checked = %v, want %v", tt.role, tt.value, *gotChecked, *tt.wantChecked)
		}
	}
}

func TestFindInstalledApplicationPathInDirs(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"Calculator.app", "TextEdit.app"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0755); err != nil {
			t.Fatalf("Mkdir(%q): %v", name, err)
		}
	}

	tests := []struct {
		name  string
		query string
		want  string
	}{
		{name: "exact app suffix", query: "Calculator.app", want: filepath.Join(dir, "Calculator.app")},
		{name: "case insensitive exact", query: "calculator", want: filepath.Join(dir, "Calculator.app")},
		{name: "substring", query: "calc", want: filepath.Join(dir, "Calculator.app")},
		{name: "missing", query: "safari", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findInstalledApplicationPathInDirs(tt.query, []string{dir})
			if got != tt.want {
				t.Fatalf("findInstalledApplicationPathInDirs(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}

func boolPtr(v bool) *bool {
	return &v
}
