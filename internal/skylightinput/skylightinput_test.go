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
