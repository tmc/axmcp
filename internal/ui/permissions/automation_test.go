package permissions

import (
	"fmt"
	"testing"
)

func TestPermissionItemName(t *testing.T) {
	tests := []struct {
		name       string
		title      string
		desc       string
		identifier string
		want       string
	}{
		{name: "title", title: "axcdp", desc: "ignored", identifier: "ignored", want: "axcdp"},
		{name: "description", desc: "axcdp", identifier: "ignored", want: "axcdp"},
		{name: "trim title", title: " axcdp\x00ignored ", desc: "ignored", identifier: "ignored", want: "axcdp"},
		{name: "toggle identifier", identifier: "axcdp.app_Toggle", want: "axcdp.app"},
		{name: "plain identifier", identifier: "axcdp", want: "axcdp"},
		{name: "null suffix", identifier: "axcdp.app_Toggle\x00junk", want: "axcdp.app"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := permissionItemName(tt.title, tt.desc, tt.identifier); got != tt.want {
				t.Fatalf("permissionItemName = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPermissionRoleClassifiers(t *testing.T) {
	for _, role := range []string{"AXCheckBox", "AXSwitch", "AXToggle", "AXSwitch\x00junk"} {
		if !isToggleRole(role) {
			t.Fatalf("isToggleRole(%q) = false, want true", role)
		}
	}
	for _, role := range []string{"AXRow", "AXCell", "AXGroup", "AXGroup\x00junk"} {
		if !isContainerRole(role) {
			t.Fatalf("isContainerRole(%q) = false, want true", role)
		}
	}
	if isToggleRole("AXButton") {
		t.Fatal("isToggleRole(AXButton) = true, want false")
	}
	if isContainerRole("AXButton") {
		t.Fatal("isContainerRole(AXButton) = true, want false")
	}
}

func TestMatchesPermissionApp(t *testing.T) {
	tests := []struct {
		name   string
		item   string
		title  string
		desc   string
		needle string
		want   bool
	}{
		{name: "name", item: "axcdp.app", needle: "axcdp", want: true},
		{name: "app needle", item: "axcdp", needle: "axcdp.app", want: true},
		{name: "title", title: "axcdp", needle: "axcdp", want: true},
		{name: "description", desc: "AXCDP Helper", needle: "axcdp", want: true},
		{name: "dotted helper", desc: "AXCDP.Helper", needle: "axcdp", want: true},
		{name: "substring guard", item: "Terminal", needle: "term", want: false},
		{name: "suffix guard", item: "my-axcdp", needle: "axcdp", want: false},
		{name: "missing", item: "Terminal", title: "Terminal", desc: "", needle: "axcdp", want: false},
		{name: "empty needle", item: "Terminal", title: "Terminal", desc: "", needle: "", want: false},
		{name: "space needle", item: "Terminal", title: "Terminal", desc: "", needle: "  ", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesPermissionApp(tt.item, tt.title, tt.desc, tt.needle); got != tt.want {
				t.Fatalf("matchesPermissionApp = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNormalizePermissionText(t *testing.T) {
	if got := normalizePermissionText(" AXCDP.app\x00ignored "); got != "axcdp.app" {
		t.Fatalf("normalizePermissionText = %q, want axcdp.app", got)
	}
}

func TestIsNotFoundAllowsWrappedError(t *testing.T) {
	err := fmt.Errorf("reenable app: %w", permissionAppNotFoundError{AppName: "axcdp"})
	if !isNotFound(err) {
		t.Fatal("isNotFound returned false for wrapped permissionAppNotFoundError")
	}
	if isNotFound(fmt.Errorf("other error")) {
		t.Fatal("isNotFound returned true for unrelated error")
	}
}
