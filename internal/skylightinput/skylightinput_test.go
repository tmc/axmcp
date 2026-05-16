package skylightinput

import (
	"os"
	"testing"

	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
)

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
