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

func TestSendKeyComboToPIDRejectsInvalidPID(t *testing.T) {
	if err := SendKeyComboToPID(0, "cmd+a"); err == nil {
		t.Fatalf("SendKeyComboToPID should reject invalid pid")
	}
}

func TestNormalizeClickCount(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int
	}{
		{name: "negative", in: -1, want: 1},
		{name: "zero", in: 0, want: 1},
		{name: "one", in: 1, want: 1},
		{name: "double", in: 2, want: 2},
	}
	for _, tt := range tests {
		if got := normalizeClickCount(tt.in); got != tt.want {
			t.Fatalf("%s: normalizeClickCount(%d) = %d, want %d", tt.name, tt.in, got, tt.want)
		}
	}
}
