package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/tmc/apple/coregraphics"
	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/input"
)

func TestNativeKeyCancellationPairsOwnDown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	target := nativeTarget{App: computeruse.AppInfo{PID: 42}, Start: nativeStart{Seconds: 1}}
	var events []bool
	var targets []nativeTarget
	post := func(_ context.Context, got nativeTarget, down bool, _ uint16, _ coregraphics.CGEventFlags, _ []uint16) (bool, error) {
		events = append(events, down)
		targets = append(targets, got)
		if down {
			cancel()
		}
		return true, nil
	}
	attempted, err := nativeKeySequence(ctx, target, "ab", nil, func() error { return nil }, post)
	if !attempted || !errors.Is(err, context.Canceled) || !reflect.DeepEqual(events, []bool{true, false}) {
		t.Fatalf("attempted=%v err=%v events=%v", attempted, err, events)
	}
	for _, got := range targets {
		if got != target {
			t.Fatalf("target=%+v", got)
		}
	}
}
func TestNativeKeyFailure(t *testing.T) {
	blocked := errors.New("blocked")
	for _, tt := range []struct {
		name           string
		cancel         bool
		guardErr       error
		downSent       bool
		downErr, upErr error
		wantEvents     []bool
		wantAttempt    bool
	}{
		{name: "pre-canceled", cancel: true},
		{name: "focus guard", guardErr: blocked},
		{name: "unsent down", downErr: blocked, wantEvents: []bool{true}},
		{name: "uncertain down", downSent: true, downErr: blocked, wantEvents: []bool{true, false}, wantAttempt: true},
		{name: "failed cleanup", downSent: true, upErr: blocked, wantEvents: []bool{true, false}, wantAttempt: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.cancel {
				cancel()
			}
			var events []bool
			post := func(_ context.Context, _ nativeTarget, down bool, _ uint16, _ coregraphics.CGEventFlags, _ []uint16) (bool, error) {
				events = append(events, down)
				if down {
					return tt.downSent, tt.downErr
				}
				return tt.upErr == nil, tt.upErr
			}
			attempted, err := nativeKeySequence(ctx, nativeTarget{}, "ab", nil, func() error { return tt.guardErr }, post)
			if err == nil || attempted != tt.wantAttempt || !reflect.DeepEqual(events, tt.wantEvents) {
				t.Fatalf("attempted=%v err=%v events=%v", attempted, err, events)
			}
		})
	}
}
func TestNativeKeyUnicodeAndCombination(t *testing.T) {
	type event struct {
		down  bool
		code  uint16
		flags coregraphics.CGEventFlags
		units []uint16
	}
	var events []event
	post := func(_ context.Context, _ nativeTarget, down bool, code uint16, flags coregraphics.CGEventFlags, units []uint16) (bool, error) {
		events = append(events, event{down, code, flags, append([]uint16(nil), units...)})
		return true, nil
	}
	guard := func() error { return nil }
	attempted, err := nativeKeySequence(context.Background(), nativeTarget{}, "A😀", nil, guard, post)
	want := []event{{true, 0, 0, []uint16{65}}, {false, 0, 0, []uint16{65}}, {true, 0, 0, []uint16{0xd83d, 0xde00}}, {false, 0, 0, []uint16{0xd83d, 0xde00}}}
	if !attempted || err != nil || !reflect.DeepEqual(events, want) {
		t.Fatalf("attempted=%v err=%v events=%v", attempted, err, events)
	}
	events = nil
	combo := input.KeyCombo{KeyCode: 12, Command: true, Shift: true}
	attempted, err = nativeKeySequence(context.Background(), nativeTarget{}, "", &combo, guard, post)
	flags := coregraphics.KCGEventFlagMaskCommand | coregraphics.KCGEventFlagMaskShift
	want = []event{{down: true, code: 12, flags: flags}, {down: false, code: 12, flags: flags}}
	if !attempted || err != nil || !reflect.DeepEqual(events, want) {
		t.Fatalf("attempted=%v err=%v events=%v", attempted, err, events)
	}
}
func TestExactNativeApp(t *testing.T) {
	apps := []computeruse.AppInfo{{PID: 42, Name: "Fixture", BundleID: "test.fixture"}, {PID: 43, Name: "Fixture Helper", BundleID: "test.helper"}, {PID: 44, Name: "Duplicate", BundleID: "test.dup"}, {PID: 45, Name: "Duplicate", BundleID: "test.dup"}}
	for _, tt := range []struct {
		selector string
		pid      int
	}{
		{"42", 42}, {" FIXTURE ", 42}, {"test.helper", 43}, {"Fixt", 0}, {"", 0}, {"Duplicate", 0}, {"test.dup", 0}, {"99", 0},
	} {
		t.Run(tt.selector, func(t *testing.T) {
			got, err := exactNativeApp(apps, tt.selector)
			if tt.pid == 0 {
				if err == nil {
					t.Fatalf("accepted %+v", got)
				}
			} else if err != nil || got.PID != tt.pid {
				t.Fatalf("app=%+v err=%v", got, err)
			}
		})
	}
}
