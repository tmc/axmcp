//go:build darwin

package intervention

import (
	"testing"
	"time"

	"github.com/tmc/apple/coregraphics"
)

func TestMonitorDisabledDoesNotBlock(t *testing.T) {
	m := New(Config{})
	now := time.Unix(10, 0)
	m.Record("KCGEventKeyDown", now)
	if _, blocked := m.Blocked(now); blocked {
		t.Fatalf("disabled monitor blocked action")
	}
}

func TestMonitorBlocksDuringQuietPeriod(t *testing.T) {
	m := New(Config{Enabled: true, QuietPeriod: time.Second})
	now := time.Unix(10, 0)
	m.RecordEvent("KCGEventKeyDown", "keyboard", 1234, now)

	status, blocked := m.Blocked(now.Add(500 * time.Millisecond))
	if !blocked {
		t.Fatalf("Blocked = false, want true")
	}
	if status.LastType != "KCGEventKeyDown" {
		t.Fatalf("LastType = %q, want KCGEventKeyDown", status.LastType)
	}
	if status.LastKind != "keyboard" {
		t.Fatalf("LastKind = %q, want keyboard", status.LastKind)
	}
	if status.LastPID != 1234 {
		t.Fatalf("LastPID = %d, want 1234", status.LastPID)
	}
	if _, blocked := m.Blocked(now.Add(2 * time.Second)); blocked {
		t.Fatalf("Blocked after quiet period = true, want false")
	}
}

func TestEventKind(t *testing.T) {
	tests := []struct {
		name string
		typ  coregraphics.CGEventType
		want string
	}{
		{name: "key", typ: coregraphics.KCGEventKeyDown, want: "keyboard"},
		{name: "mouse", typ: coregraphics.KCGEventLeftMouseDown, want: "mouse"},
		{name: "scroll", typ: coregraphics.KCGEventScrollWheel, want: "scroll"},
	}
	for _, tt := range tests {
		if got := eventKind(tt.typ); got != tt.want {
			t.Fatalf("%s: eventKind(%s) = %q, want %q", tt.name, tt.typ, got, tt.want)
		}
	}
}

func TestEventMask(t *testing.T) {
	mask := eventMask(
		coregraphics.KCGEventKeyDown,
		coregraphics.KCGEventLeftMouseDown,
		coregraphics.KCGEventScrollWheel,
	)
	tests := []struct {
		name string
		typ  coregraphics.CGEventType
		want bool
	}{
		{name: "key", typ: coregraphics.KCGEventKeyDown, want: true},
		{name: "mouse", typ: coregraphics.KCGEventLeftMouseDown, want: true},
		{name: "scroll", typ: coregraphics.KCGEventScrollWheel, want: true},
		{name: "key up", typ: coregraphics.KCGEventKeyUp, want: false},
	}
	for _, tt := range tests {
		got := mask&(1<<uint(tt.typ)) != 0
		if got != tt.want {
			t.Fatalf("%s: mask contains %s = %v, want %v", tt.name, tt.typ, got, tt.want)
		}
	}
}

func TestCallbackSignature(t *testing.T) {
	m := New(Config{Enabled: true})
	var _ coregraphics.CGEventTapCallBack = m.callback
}
