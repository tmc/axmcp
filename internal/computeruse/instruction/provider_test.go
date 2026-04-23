package instruction

import (
	"strings"
	"testing"

	"github.com/tmc/axmcp/internal/computeruse"
)

func TestInstructionsByBundleID(t *testing.T) {
	provider := New()

	tests := []struct {
		name string
		app  computeruse.AppInfo
		want string
	}{
		{
			name: "music",
			app: computeruse.AppInfo{
				Name:     "Whatever",
				BundleID: "com.apple.Music",
			},
			want: "sidebar",
		},
		{
			name: "clock",
			app: computeruse.AppInfo{
				BundleID: "com.apple.clock",
			},
			want: "23:59:59",
		},
		{
			name: "notion",
			app: computeruse.AppInfo{
				BundleID: "notion.id",
			},
			want: "block editor",
		},
		{
			name: "numbers",
			app: computeruse.AppInfo{
				BundleID: "com.apple.iwork.numbers",
			},
			want: "formula bar",
		},
		{
			name: "spotify",
			app: computeruse.AppInfo{
				BundleID: "com.spotify.client",
			},
			want: "Playback changes can lag",
		},
		{
			name: "iphone mirroring",
			app: computeruse.AppInfo{
				BundleID: "com.apple.iphonemirroring",
			},
			want: "remote device",
		},
		{
			name: "browser",
			app: computeruse.AppInfo{
				BundleID: "com.apple.safari",
			},
			want: "page before the chrome around it",
		},
	}

	for _, tt := range tests {
		text := provider.Instructions(tt.app)
		if !strings.Contains(text, tt.want) {
			t.Fatalf("Instructions(%s) = %q, want substring %q", tt.name, text, tt.want)
		}
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

func TestInstructionsGenericBrowserBundleFallback(t *testing.T) {
	provider := New()

	text := provider.Instructions(computeruse.AppInfo{BundleID: "com.example.bravebeta"})
	if !strings.Contains(text, "page before the chrome around it") {
		t.Fatalf("Instructions(browser bundle fallback) = %q, want browser guidance", text)
	}
}

func TestInstructionsUnknownApp(t *testing.T) {
	provider := New()

	text := provider.Instructions(computeruse.AppInfo{Name: "Preview"})
	if text != "" {
		t.Fatalf("Instructions(Preview) = %q, want empty string", text)
	}
}
