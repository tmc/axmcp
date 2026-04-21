package input

import (
	"testing"

	"github.com/tmc/axmcp/internal/computeruse"
)

func TestScreenshotPointToWindowLocal(t *testing.T) {
	window := computeruse.WindowInfo{
		Width:            400,
		Height:           200,
		ScreenshotWidth:  800,
		ScreenshotHeight: 400,
	}
	point, err := ScreenshotPointToWindowLocal(window, 200, 100)
	if err != nil {
		t.Fatalf("ScreenshotPointToWindowLocal: %v", err)
	}
	if point.X != 100 || point.Y != 50 {
		t.Fatalf("point = %+v, want {100 50}", point)
	}
}

func TestParseKeyCombo(t *testing.T) {
	tests := []struct {
		spec        string
		wantKey     string
		wantCommand bool
		wantShift   bool
		wantOption  bool
	}{
		{spec: "cmd+a", wantKey: "a", wantCommand: true},
		{spec: "command+shift+=", wantKey: "=", wantCommand: true, wantShift: true},
		{spec: "alt+left", wantKey: "left", wantOption: true},
	}
	for _, tt := range tests {
		combo, err := ParseKeyCombo(tt.spec)
		if err != nil {
			t.Fatalf("ParseKeyCombo(%q): %v", tt.spec, err)
		}
		if combo.Label != tt.wantKey || combo.Command != tt.wantCommand || combo.Shift != tt.wantShift || combo.Option != tt.wantOption {
			t.Fatalf("ParseKeyCombo(%q) = %+v", tt.spec, combo)
		}
	}
}
