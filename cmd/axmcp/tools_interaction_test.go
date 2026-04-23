package main

import (
	"testing"

	"github.com/tmc/apple/coregraphics"
)

func TestParseAXKeyCode(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want uint16
	}{
		{name: "letter", key: "A", want: 0x00},
		{name: "trimmed function key", key: "  F12  ", want: 0x6F},
		{name: "symbol", key: "-", want: 0x1B},
		{name: "arrow", key: "left", want: 0x7B},
	}

	for _, tt := range tests {
		got, err := parseAXKeyCode(tt.key)
		if err != nil {
			t.Fatalf("%s: parseAXKeyCode(%q) returned error: %v", tt.name, tt.key, err)
		}
		if got != tt.want {
			t.Fatalf("%s: parseAXKeyCode(%q) = %#x, want %#x", tt.name, tt.key, got, tt.want)
		}
	}
}

func TestParseAXKeyCodeUnknown(t *testing.T) {
	if _, err := parseAXKeyCode("capslock"); err == nil {
		t.Fatal("parseAXKeyCode(\"capslock\") returned nil error, want error")
	}
}

func TestKeyEventFlags(t *testing.T) {
	got := keyEventFlags(axKeyStrokeInput{
		Shift:   true,
		Control: true,
		Option:  true,
		Command: true,
	})
	want := coregraphics.KCGEventFlagMaskShift |
		coregraphics.KCGEventFlagMaskControl |
		coregraphics.KCGEventFlagMaskAlternate |
		coregraphics.KCGEventFlagMaskCommand
	if got != want {
		t.Fatalf("keyEventFlags(...) = %v, want %v", got, want)
	}
}

func TestDescribeAXKeyStroke(t *testing.T) {
	got := describeAXKeyStroke(axKeyStrokeInput{
		Key:     " s ",
		Shift:   true,
		Control: true,
		Option:  true,
		Command: true,
	})
	if got != "Cmd+Ctrl+Opt+Shift+s" {
		t.Fatalf("describeAXKeyStroke(...) = %q, want %q", got, "Cmd+Ctrl+Opt+Shift+s")
	}
}
