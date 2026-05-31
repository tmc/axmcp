package skylightinput

import (
	"os"
	"strings"
	"testing"

	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
)

func TestValidatePID(t *testing.T) {
	for _, pid := range []int32{0, -1} {
		if err := validatePID(pid); err == nil || !strings.Contains(err.Error(), "pid must be positive") {
			t.Fatalf("validatePID(%d) = %v, want positive pid error", pid, err)
		}
	}
	if err := validatePID(1); err != nil {
		t.Fatalf("validatePID(1): %v", err)
	}
}

func TestPublicAPIsRejectInvalidInputsBeforeResolve(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
		want string
	}{
		{
			name: "activate pid",
			run:  func() error { return ActivateWithoutRaise(0, 1) },
			want: "pid must be positive",
		},
		{
			name: "activate window",
			run:  func() error { return ActivateWithoutRaise(1, 0) },
			want: "window id is zero",
		},
		{
			name: "mouse pid",
			run:  func() error { return MouseClick(0, Point{}, Point{}, 0, 1) },
			want: "pid must be positive",
		},
		{
			name: "mouse click count",
			run:  func() error { return MouseClick(1, Point{}, Point{}, 0, 0) },
			want: "click count must be positive",
		},
		{
			name: "key pid",
			run:  func() error { return KeyPress(0, 0, 0, false) },
			want: "pid must be positive",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestKeyboardEventAuthenticationMessage(t *testing.T) {
	resolve()
	if authMessageErr != nil {
		t.Skipf("SLSEventAuthenticationMessage unavailable: %v", authMessageErr)
	}
	event := coregraphics.CGEventCreateKeyboardEvent(0, 0, true)
	if event == 0 {
		t.Skip("CGEventCreateKeyboardEvent returned nil")
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(event))
	if record := eventRecord(event); record == nil {
		t.Fatalf("eventRecord returned nil")
	}
	if err := attachAuthenticationMessage(event, int32(os.Getpid())); err != nil {
		t.Fatalf("attachAuthenticationMessage: %v", err)
	}
}

func TestStampMouseEventClickState(t *testing.T) {
	event := coregraphics.CGEventCreateMouseEvent(
		0,
		coregraphics.KCGEventLeftMouseDown,
		corefoundation.CGPoint{X: 10, Y: 20},
		coregraphics.KCGMouseButtonLeft,
	)
	if event == 0 {
		t.Skip("CGEventCreateMouseEvent returned nil")
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(event))
	stampMouseEvent(event, int32(os.Getpid()), 0, Point{X: 1, Y: 2}, 2)
	if got := coregraphics.CGEventGetIntegerValueField(event, coregraphics.KCGMouseEventClickState); got != 2 {
		t.Fatalf("click state = %d, want 2", got)
	}
}
