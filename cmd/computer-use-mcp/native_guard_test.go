package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/tmc/apple/coregraphics"
	"github.com/tmc/axmcp/internal/computeruse"
)

type nativeGuardFixture struct {
	permissions    computeruse.PermissionState
	approval       computeruse.ApprovalState
	start          nativeStart
	window         computeruse.WindowInfo
	exists         bool
	focused        uint32
	intervention   error
	elementFocused bool
}

func newNativeGuardFixture() (*nativeGuardFixture, nativeTarget, nativeDispatchGuard) {
	target := nativeTarget{App: computeruse.AppInfo{PID: 42}, Start: nativeStart{Seconds: 1, Microseconds: 2}, Window: computeruse.WindowInfo{WindowID: 7, X: 100, Y: 200, Width: 400, Height: 300}}
	f := &nativeGuardFixture{approval: computeruse.ApprovalState{Approved: true}, start: target.Start, window: target.Window, exists: true, focused: 7, elementFocused: true}
	g := nativeDispatchGuard{
		authorize: func() (computeruse.PermissionState, computeruse.ApprovalState, error) {
			return f.permissions, f.approval, f.intervention
		},
		processStart:   func() (nativeStart, error) { return f.start, nil },
		window:         func() (computeruse.WindowInfo, bool, error) { return f.window, f.exists, nil },
		focusedWindow:  func() (uint32, error) { return f.focused, nil },
		elementFocused: func() (bool, error) { return f.elementFocused, nil },
	}
	return f, target, g
}

func TestNativeDispatchFaultsBetweenKeys(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*nativeGuardFixture)
		want   string
	}{
		{"permission", func(f *nativeGuardFixture) { f.permissions.Pending = true }, "authorization"},
		{"approval", func(f *nativeGuardFixture) { f.approval.Approved = false }, "authorization"},
		{"process birth", func(f *nativeGuardFixture) { f.start.Microseconds++ }, "process"},
		{"window replacement", func(f *nativeGuardFixture) { f.window.WindowID++ }, "window"},
		{"window move", func(f *nativeGuardFixture) { f.window.X++ }, "window"},
		{"window resize", func(f *nativeGuardFixture) { f.window.Height++ }, "window"},
		{"window disappearance", func(f *nativeGuardFixture) { f.exists = false }, "window"},
		{"element focus theft", func(f *nativeGuardFixture) { f.elementFocused = false }, "observed element"},
		{"focus theft", func(f *nativeGuardFixture) { f.focused++ }, "focused window"},
		{"user intervention", func(f *nativeGuardFixture) { f.intervention = errors.New("physical input") }, "physical input"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f, target, g := newNativeGuardFixture()
			var events []bool
			post := func(_ context.Context, got nativeTarget, down bool, _ uint16, _ coregraphics.CGEventFlags, _ []uint16) (bool, error) {
				if got != target {
					t.Fatalf("wrong target: %+v", got)
				}
				events = append(events, down)
				if !down {
					tt.mutate(f)
				}
				return true, nil
			}
			attempted, err := nativeKeySequence(context.Background(), target, "ab", nil, func() error { return g.check(context.Background(), target, "type_text") }, post)
			if !attempted || err == nil || !strings.Contains(err.Error(), tt.want) || !reflect.DeepEqual(events, []bool{true, false}) {
				t.Fatalf("attempted=%v err=%v events=%v", attempted, err, events)
			}
			// The same changed facts must also prevent the first event of a new sequence.
			events = nil
			attempted, err = nativeKeySequence(context.Background(), target, "c", nil, func() error { return g.check(context.Background(), target, "type_text") }, post)
			if attempted || err == nil || len(events) != 0 {
				t.Fatalf("preflight attempted=%v err=%v events=%v", attempted, err, events)
			}
		})
	}
}

func TestNativeDispatchGuardPositive(t *testing.T) {
	_, target, g := newNativeGuardFixture()
	var events []bool
	post := func(_ context.Context, _ nativeTarget, down bool, _ uint16, _ coregraphics.CGEventFlags, _ []uint16) (bool, error) {
		events = append(events, down)
		return true, nil
	}
	attempted, err := nativeKeySequence(context.Background(), target, "ab", nil, func() error { return g.check(context.Background(), target, "type_text") }, post)
	if !attempted || err != nil || !reflect.DeepEqual(events, []bool{true, false, true, false}) {
		t.Fatalf("attempted=%v err=%v events=%v", attempted, err, events)
	}
}

func TestNativeCleanupProcessInstance(t *testing.T) {
	for _, tt := range []struct {
		name                string
		exit, reuse, cancel bool
		want                []bool
		wantErr             bool
	}{
		{name: "same process cancellation", cancel: true, want: []bool{true, false}, wantErr: true},
		{name: "process exited", exit: true, want: []bool{true}, wantErr: true},
		{name: "PID reused", reuse: true, want: []bool{true}, wantErr: true},
		{name: "same process", want: []bool{true, false, true, false}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, target, _ := newNativeGuardFixture()
			current := target.Start
			exited := false
			var events []bool
			lookup := func(pid int) (nativeStart, error) {
				if pid != target.App.PID {
					t.Fatalf("lookup pid=%d", pid)
				}
				if exited {
					return nativeStart{}, errors.New("process exited")
				}
				return current, nil
			}
			emit := func(_ context.Context, got nativeTarget, down bool, _ uint16, _ coregraphics.CGEventFlags, _ []uint16) (bool, error) {
				if got != target {
					t.Fatalf("post target=%+v", got)
				}
				events = append(events, down)
				if down {
					if tt.exit {
						exited = true
					}
					if tt.reuse {
						current.Microseconds++
					}
					if tt.cancel {
						cancel()
					}
				}
				return true, nil
			}
			post := func(ctx context.Context, target nativeTarget, down bool, code uint16, flags coregraphics.CGEventFlags, units []uint16) (bool, error) {
				return postNativeKeyToInstance(ctx, target, down, code, flags, units, lookup, emit)
			}
			attempted, err := nativeKeySequence(ctx, target, "ab", nil, func() error { return nil }, post)
			if !attempted || (err != nil) != tt.wantErr || !reflect.DeepEqual(events, tt.want) {
				t.Fatalf("attempted=%v err=%v events=%v", attempted, err, events)
			}
		})
	}
}
