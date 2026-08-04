package main

import (
	"strings"
	"testing"
)

func TestFormatMenuItems(t *testing.T) {
	got := formatMenuItems([]menuItemState{
		{Title: "New", Enabled: true, HasSubmenu: true},
		{Separator: true},
		{Title: "Export…", Enabled: false},
		{Title: "Open…", Enabled: true},
	})
	for _, want := range []string{"New >", "---", "Export… (disabled)", "Open…\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("formatMenuItems(...) missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Open… (disabled)") {
		t.Errorf("formatMenuItems(...) marked an enabled item disabled:\n%s", got)
	}
}

func TestFormatMenuItemsEmpty(t *testing.T) {
	if got := formatMenuItems(nil); got != "(no items)\n" {
		t.Errorf("formatMenuItems(nil) = %q, want %q", got, "(no items)\n")
	}
}

func TestIsMenuContainerRole(t *testing.T) {
	for _, role := range []string{"AXMenuBar", "AXMenu"} {
		if !isMenuContainerRole(role) {
			t.Errorf("isMenuContainerRole(%q) = false, want true", role)
		}
	}
	for _, role := range []string{"AXMenuBarItem", "AXMenuItem", "AXWindow", ""} {
		if isMenuContainerRole(role) {
			t.Errorf("isMenuContainerRole(%q) = true, want false", role)
		}
	}
}
