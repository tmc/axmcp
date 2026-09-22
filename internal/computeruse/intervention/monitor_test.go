package intervention

import (
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
	"testing"
	"time"
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
	m.Record("KCGEventKeyDown", now)

	status, blocked := m.Blocked(now.Add(500 * time.Millisecond))
	if !blocked {
		t.Fatalf("Blocked = false, want true")
	}
	if status.LastType != "KCGEventKeyDown" {
		t.Fatalf("LastType = %q, want KCGEventKeyDown", status.LastType)
	}
	if _, blocked := m.Blocked(now.Add(2 * time.Second)); blocked {
		t.Fatalf("Blocked after quiet period = true, want false")
	}
}

func TestMonitorNativeCleanup(t *testing.T) {
	if !coregraphics.CGPreflightListenEventAccess() {
		t.Skip("input monitoring unavailable")
	}
	m := New(Config{Enabled: true})
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	lib, tap, source := m.lib, m.tap, m.source
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	if m.tap != tap || m.source != source {
		t.Fatal("second Start replaced live resources")
	}
	lib.Retain(uintptr(tap))
	lib.Retain(uintptr(source))
	defer func() {
		m.Close()
		corefoundation.CFRunLoopRemoveSource(corefoundation.CFRunLoopGetMain(), source, corefoundation.KCFRunLoopCommonModes)
		corefoundation.CFMachPortInvalidate(tap)
		lib.Release(uintptr(source))
		lib.Release(uintptr(tap))
	}()
	if !corefoundation.CFRunLoopContainsSource(corefoundation.CFRunLoopGetMain(), source, corefoundation.KCFRunLoopCommonModes) {
		t.Fatal("positive source registration missing")
	}
	m.Close()
	if corefoundation.CFRunLoopContainsSource(corefoundation.CFRunLoopGetMain(), source, corefoundation.KCFRunLoopCommonModes) {
		t.Fatal("closed monitor remains registered in run loop")
	}
}
