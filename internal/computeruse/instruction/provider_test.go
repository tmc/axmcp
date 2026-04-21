package instruction

import (
	"strings"
	"testing"

	"github.com/tmc/axmcp/internal/computeruse"
)

func TestInstructionsByBundleID(t *testing.T) {
	provider := New()

	text := provider.Instructions(computeruse.AppInfo{
		Name:     "Whatever",
		BundleID: "com.apple.Music",
	})
	if !strings.Contains(text, "sidebar") {
		t.Fatalf("Instructions(Music) = %q, want music guidance", text)
	}
}

func TestInstructionsByNameFallback(t *testing.T) {
	provider := New()

	text := provider.Instructions(computeruse.AppInfo{Name: "Spotify"})
	if !strings.Contains(text, "Playback changes can lag") {
		t.Fatalf("Instructions(Spotify) = %q, want spotify guidance", text)
	}
}

func TestInstructionsBundleIDWins(t *testing.T) {
	provider := New()

	text := provider.Instructions(computeruse.AppInfo{
		Name:     "Clock",
		BundleID: "com.apple.Safari",
	})
	if !strings.Contains(text, "page before the chrome around it") {
		t.Fatalf("Instructions(bundle override) = %q, want browser guidance", text)
	}
}

func TestInstructionsGenericBrowserFallback(t *testing.T) {
	provider := New()

	text := provider.Instructions(computeruse.AppInfo{Name: "Arc Browser"})
	if !strings.Contains(text, "use scroll rather than drag") {
		t.Fatalf("Instructions(browser fallback) = %q, want browser guidance", text)
	}
}

func TestInstructionsUnknownApp(t *testing.T) {
	provider := New()

	text := provider.Instructions(computeruse.AppInfo{Name: "Preview"})
	if text != "" {
		t.Fatalf("Instructions(Preview) = %q, want empty string", text)
	}
}
