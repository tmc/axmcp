package policy

import (
	"strings"
	"testing"

	"github.com/tmc/axmcp/internal/computeruse"
)

func TestURLPolicyBlocksBrowserDomain(t *testing.T) {
	p := NewURLPolicy([]string{"example.com"})
	state := computeruse.AppState{
		App: computeruse.AppInfo{Name: "Brave Browser", BundleID: "com.brave.Browser"},
		Tree: []computeruse.ElementNode{{
			Role:        "AXTextField",
			Description: "Address and search bar",
			Value:       "https://docs.example.com/path",
		}},
	}
	err := p.CheckState(state)
	if err == nil {
		t.Fatalf("CheckState = nil, want block")
	}
	if !strings.Contains(err.Error(), "docs.example.com") {
		t.Fatalf("error = %q, want host", err.Error())
	}
}

func TestURLPolicyIgnoresNonBrowser(t *testing.T) {
	p := NewURLPolicy([]string{"example.com"})
	state := computeruse.AppState{
		App: computeruse.AppInfo{Name: "Notes", BundleID: "com.apple.Notes"},
		Tree: []computeruse.ElementNode{{
			Role:        "AXTextField",
			Description: "Address",
			Value:       "https://example.com",
		}},
	}
	if err := p.CheckState(state); err != nil {
		t.Fatalf("CheckState = %v, want nil", err)
	}
}
