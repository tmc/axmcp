package ghostcursor

import (
	"github.com/ebitengine/purego"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
	"github.com/tmc/axmcp/internal/purego/cfhandle"
	"testing"
)

func TestInterventionTokenLifetime(t *testing.T) {
	a, b := &Controller{}, &Controller{}
	first := registerInterventionController(a)
	if first == 0 || interventionController(first) != a {
		t.Fatal("first registration missing")
	}
	unregisterInterventionController(first)
	second := registerInterventionController(b)
	defer unregisterInterventionController(second)
	if second == 0 || second == first || interventionController(second) != b {
		t.Fatal("token reused or second registration missing")
	}
	if interventionController(first) != nil {
		t.Fatal("retired token resolved")
	}
	// Enter the actual native callback trampoline with a retired registry token.
	callback := purego.NewCallback(ghostCursorInterventionCallback)
	var invoke func(coregraphics.CGEventTapProxy, coregraphics.CGEventType, coregraphics.CGEventRef, uintptr) coregraphics.CGEventRef
	purego.RegisterFunc(&invoke, callback)
	if got := invoke(0, coregraphics.KCGEventMouseMoved, 0, first); got != 0 {
		t.Fatalf("retired callback returned %d", got)
	}
}

func TestInterventionTokenABI(t *testing.T) {
	const token = uintptr(0x123456789abcdef)
	var got uintptr
	callback := purego.NewCallback(func(_ uintptr, _ uint32, event uintptr, user uintptr) uintptr { got = user; return event })
	var invoke func(uintptr, uint32, uintptr, uintptr) uintptr
	purego.RegisterFunc(&invoke, callback)
	if result := invoke(0, 5, 73, token); result != 73 || got != token {
		t.Fatalf("event=%d token=%x", result, got)
	}
}

func TestInterventionTapCreate(t *testing.T) {
	if !coregraphics.CGPreflightListenEventAccess() {
		t.Skip("input monitoring permission unavailable")
	}
	lib, err := cfhandle.Open()
	if err != nil {
		t.Fatal(err)
	}
	create, err := loadInterventionTap()
	if err != nil {
		t.Fatal(err)
	}
	c := &Controller{}
	token := registerInterventionController(c)
	defer unregisterInterventionController(token)
	tap := create(coregraphics.KCGSessionEventTap, coregraphics.KCGHeadInsertEventTap, eventTapOptionListenOnly, mouseInterventionMask(), token)
	if tap == 0 {
		t.Fatal("create listen-only native tap")
	}
	coregraphics.CGEventTapEnable(tap, false)
	corefoundation.CFMachPortInvalidate(tap)
	lib.Release(uintptr(tap))
}
