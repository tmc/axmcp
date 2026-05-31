package skylightinput

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/tmc/apple/applicationservices"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	"github.com/tmc/apple/private/skylight"
)

// ErrUnavailable is returned when one or more required private SPIs
// could not be resolved on the current macOS. Callers are expected to
// fall back to a public-API code path (CGEventPost on kCGHIDEventTap).
var ErrUnavailable = errors.New("skylightinput: required SPI unavailable")

// Point is a screen point in Quartz convention (top-left origin, y-down).
type Point struct {
	X float64
	Y float64
}

// init probes the generated private bindings used by this package. SkyLight
// event posting/stamping functions come from tmc/apple/private/skylight.
// GetProcessForPID comes from tmc/apple/applicationservices.
var (
	resolveOnce sync.Once

	getProcessForPIDErr error
	windowPSNErr        error
	authMessageErr      error

	// resolveStatus is a one-line summary of which SPIs are usable on the
	// current OS, suitable for logging at startup. Populated even on
	// success so callers can surface it for forensics.
	resolveStatus string
	resolveErr    error
)

func resolve() {
	resolveOnce.Do(func() {
		var diag []string

		diag = append(diag, "CGEventSetWindowLocation=bound")
		diag = append(diag, "SLEventSetIntegerValueField=bound")
		if getProcessForPIDErr = probeGetProcessForPID(); getProcessForPIDErr == nil {
			diag = append(diag, "GetProcessForPID=ok")
		} else {
			diag = append(diag, "GetProcessForPID=miss")
		}
		if windowPSNErr = probeWindowPSNBindings(); windowPSNErr == nil {
			diag = append(diag, "SLSWindowPSN=ok")
		} else {
			diag = append(diag, "SLSWindowPSN=miss")
		}
		if authMessageErr = probeAuthMessageBinding(); authMessageErr == nil {
			diag = append(diag, "SLSEventAuthenticationMessage=ok")
		} else {
			diag = append(diag, "SLSEventAuthenticationMessage=miss")
		}
		// Probe SLEventPostToPid via the tmc/apple binding (it loads on
		// package init; if it's missing we can't post anything).
		if _, err := skylight.SLEventPostToPid(-1, 0); err != nil && isSymbolUnavailable(err) {
			diag = append(diag, "SLEventPostToPid=miss")
		} else {
			diag = append(diag, "SLEventPostToPid=ok")
		}
		resolveStatus = strings.Join(diag, " ")
	})
}

func probeAuthMessageBinding() error {
	class := skylight.GetSLSEventAuthenticationMessageClass().Class()
	if class == 0 {
		return fmt.Errorf("SLSEventAuthenticationMessage class unavailable")
	}
	return nil
}

func probeWindowPSNBindings() error {
	cid, err := skylight.CGSMainConnectionID()
	if err != nil {
		return fmt.Errorf("CGSMainConnectionID: %w", err)
	}
	var owner skylight.CGSConnectionID
	if _, err := skylight.SLSGetWindowOwner(cid, 0, &owner); err != nil {
		return fmt.Errorf("SLSGetWindowOwner: %w", err)
	}
	var psn skylight.ProcessSerialNumber
	if _, err := skylight.SLSGetConnectionPSN(owner, &psn); err != nil {
		return fmt.Errorf("SLSGetConnectionPSN: %w", err)
	}
	return nil
}

func attachAuthenticationMessage(event coregraphics.CGEventRef, pid int32) error {
	if authMessageErr != nil {
		return authMessageErr
	}
	record := eventRecord(event)
	if record == nil {
		return fmt.Errorf("event record unavailable")
	}
	class := skylight.GetSLSEventAuthenticationMessageClass().Class()
	msg := objc.Send[objc.ID](
		objc.ID(class),
		objc.Sel("messageWithEventRecord:pid:version:"),
		record,
		pid,
		uint32(0),
	)
	if msg == 0 {
		return fmt.Errorf("SLSEventAuthenticationMessage factory returned nil")
	}
	return skylight.SLEventSetAuthenticationMessage(event, objectivec.Object{ID: msg})
}

func eventRecord(event coregraphics.CGEventRef) unsafe.Pointer {
	base := unsafe.Pointer(uintptr(event))
	if base == nil {
		return nil
	}
	for _, offset := range []uintptr{24, 32, 16} {
		if p := *(*unsafe.Pointer)(unsafe.Add(base, offset)); p != nil {
			return p
		}
	}
	return nil
}

func probeGetProcessForPID() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("GetProcessForPID: %v", r)
		}
	}()
	var psn applicationservices.ProcessSerialNumber
	_ = applicationservices.GetProcessForPID(-1, &psn)
	return nil
}

func processForPID(pid int32) (psn applicationservices.ProcessSerialNumber, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("GetProcessForPID: %v", r)
		}
	}()
	rc := applicationservices.GetProcessForPID(pid, &psn)
	if rc != 0 {
		return psn, fmt.Errorf("GetProcessForPID(%d) returned %d", pid, rc)
	}
	return psn, nil
}

func processForWindow(windowID uint32) (skylight.ProcessSerialNumber, error) {
	cid, err := skylight.CGSMainConnectionID()
	if err != nil {
		return skylight.ProcessSerialNumber{}, fmt.Errorf("CGSMainConnectionID: %w", err)
	}
	var owner skylight.CGSConnectionID
	if rc, err := skylight.SLSGetWindowOwner(cid, coregraphics.CGWindowID(windowID), &owner); err != nil {
		return skylight.ProcessSerialNumber{}, fmt.Errorf("SLSGetWindowOwner(%d): %w", windowID, err)
	} else if rc != coregraphics.KCGErrorSuccess {
		return skylight.ProcessSerialNumber{}, fmt.Errorf("SLSGetWindowOwner(%d) returned %s", windowID, rc)
	}
	var psn skylight.ProcessSerialNumber
	if rc, err := skylight.SLSGetConnectionPSN(owner, &psn); err != nil {
		return skylight.ProcessSerialNumber{}, fmt.Errorf("SLSGetConnectionPSN(%d): %w", owner, err)
	} else if rc != coregraphics.KCGErrorSuccess {
		return skylight.ProcessSerialNumber{}, fmt.Errorf("SLSGetConnectionPSN(%d) returned %s", owner, rc)
	}
	return psn, nil
}

func skylightPSN(psn applicationservices.ProcessSerialNumber) skylight.ProcessSerialNumber {
	return skylight.ProcessSerialNumber{
		HighLongOfPSN: psn.HighLongOfPSN,
		LowLongOfPSN:  psn.LowLongOfPSN,
	}
}

// Status returns a one-line human-readable summary of which SkyLight SPIs
// resolved at package init time. Useful for logging at MCP server startup
// so a silent fallback can be diagnosed without a debugger. Empty string
// before the first call to Available/MouseClick/ActivateWithoutRaise.
func Status() string {
	resolve()
	return resolveStatus
}

// Trace is an optional hook invoked with structured per-call diagnostics
// from MouseClick. Useful for tracing exactly which fields and PSNs the
// SkyLight path posted on a given call without forcing log.Printf into
// the package. Set to nil (the zero value) for silent operation.
//
// Each call to MouseClick produces between 4 and 10 trace events,
// depending on whether ActivateWithoutRaise was attempted. Event names
// are stable strings; values are caller-friendly types (int, int32,
// uint32, float64, bool, error, [4]uint32).
var Trace func(event string, fields map[string]any)

func trace(event string, fields map[string]any) {
	if Trace != nil {
		Trace(event, fields)
	}
}

// Available reports whether the SkyLight per-pid post path is usable.
// Returns nil when fully usable, or ErrUnavailable wrapping the missing
// symbol when the path cannot be taken on this OS.
func Available() error {
	resolve()
	if resolveErr != nil {
		return resolveErr
	}
	if _, err := skylight.SLEventPostToPid(-1, 0); err != nil && isSymbolUnavailable(err) {
		return fmt.Errorf("%w: SLEventPostToPid: %v", ErrUnavailable, err)
	}
	if getProcessForPIDErr != nil && windowPSNErr != nil {
		return fmt.Errorf("%w: GetProcessForPID: %v; SLS window PSN: %v", ErrUnavailable, getProcessForPIDErr, windowPSNErr)
	}
	return nil
}

// isSymbolUnavailable reports whether err is a tmc/apple-style "symbol
// unavailable" error. The concrete type lives in a sibling package's
// internal-only declaration, so we match on the error string instead of
// errors.As.
func isSymbolUnavailable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "skylight: symbol")
}

var _ = errors.Is // keep errors import; reserved for future sentinel matching

// ActivateWithoutRaise puts targetPID's window targetWindowID into AppKit-
// active state without asking WindowServer to raise the window or follow
// the user across Spaces. Recipe ported from yabai's
// window_manager_focus_window_without_raise:
//
//  1. _SLPSGetFrontProcess(&prev) - capture current front PSN.
//  2. SLSGetWindowOwner/SLSGetConnectionPSN, falling back to
//     applicationservices.GetProcessForPID(targetPID, &target).
//  3. SLPSPostEventRecordTo(prev, defocus-record).
//  4. SLPSPostEventRecordTo(target, focus-record-with-window-id).
//
// SLPSSetFrontProcessWithOptions is deliberately skipped: in yabai it raises
// the window; for our use case (driving an iPhone Mirroring window on
// possibly another Space) that's the foot-gun.
func ActivateWithoutRaise(targetPID int32, targetWindowID uint32) error {
	if err := validatePID(targetPID); err != nil {
		return err
	}
	resolve()
	if resolveErr != nil {
		return resolveErr
	}
	var prev skylight.ProcessSerialNumber
	if rc, err := skylight.SLPSGetFrontProcess(&prev); err != nil {
		return fmt.Errorf("SLPSGetFrontProcess: %w", err)
	} else if rc != 0 {
		return fmt.Errorf("SLPSGetFrontProcess returned %d", rc)
	}

	var target skylight.ProcessSerialNumber
	var targetErr error
	if targetWindowID != 0 && windowPSNErr == nil {
		target, targetErr = processForWindow(targetWindowID)
		trace("activate.window_psn", map[string]any{"target_wid": targetWindowID, "err": targetErr})
	}
	if targetErr != nil || targetWindowID == 0 || windowPSNErr != nil {
		if getProcessForPIDErr != nil {
			slsErr := targetErr
			if slsErr == nil {
				slsErr = windowPSNErr
			}
			return fmt.Errorf("%w: GetProcessForPID: %v; SLS window PSN: %v", ErrUnavailable, getProcessForPIDErr, slsErr)
		}
		targetAS, err := processForPID(targetPID)
		if err != nil {
			return err
		}
		target = skylightPSN(targetAS)
	}
	trace("activate.psn", map[string]any{
		"target_pid": targetPID,
		"target_wid": targetWindowID,
		"prev_psn":   [2]uint32{prev.HighLongOfPSN, prev.LowLongOfPSN},
		"target_psn": [2]uint32{target.HighLongOfPSN, target.LowLongOfPSN},
	})

	buf := buildFocusEventRecord(targetWindowID, false /*defocus*/)
	rc, err := skylight.SLPSPostEventRecordTo(&prev, buf)
	trace("activate.defocus", map[string]any{"rc": rc, "err": err})
	if err != nil {
		return fmt.Errorf("SLPSPostEventRecordTo defocus: %w", err)
	}
	if rc != 0 {
		return fmt.Errorf("SLPSPostEventRecordTo defocus returned %d", rc)
	}
	buf = buildFocusEventRecord(targetWindowID, true /*focus*/)
	rc, err = skylight.SLPSPostEventRecordTo(&target, buf)
	trace("activate.focus", map[string]any{"rc": rc, "err": err})
	if err != nil {
		return fmt.Errorf("SLPSPostEventRecordTo focus: %w", err)
	}
	if rc != 0 {
		return fmt.Errorf("SLPSPostEventRecordTo focus returned %d", rc)
	}
	return nil
}

// buildFocusEventRecord constructs the 248-byte synthetic event record
// SLPSPostEventRecordTo expects, per yabai's reverse-engineered layout:
//
//	bytes[0x04] = 0xf8     opcode high
//	bytes[0x08] = 0x0d     opcode low
//	bytes[0x3c..0x3f]      target CGWindowID, little-endian
//	bytes[0x8a]            0x01 = focus, 0x02 = defocus
//	all other bytes zero
func buildFocusEventRecord(windowID uint32, focus bool) []byte {
	buf := make([]byte, 0xF8)
	buf[0x04] = 0xF8
	buf[0x08] = 0x0D
	buf[0x3C] = byte(windowID & 0xFF)
	buf[0x3D] = byte((windowID >> 8) & 0xFF)
	buf[0x3E] = byte((windowID >> 16) & 0xFF)
	buf[0x3F] = byte((windowID >> 24) & 0xFF)
	if focus {
		buf[0x8A] = 0x01
	} else {
		buf[0x8A] = 0x02
	}
	return buf
}

// MouseClick synthesises one or more left-clicks at screenPt and posts them to
// pid via SLEventPostToPid. windowID is the target CGWindowID (zero if
// unknown - the click still posts, but field stamps that depend on the
// window are skipped). When windowID is non-zero, ActivateWithoutRaise is
// called first so the receiver's AppKit-active state is correct for the
// click.
//
// The recipe stamps each event with:
//
//   - kCGMouseEventButtonNumber = 0
//   - kCGMouseEventSubtype = 3 (NSEventSubtypeMouseEvent)
//   - kCGMouseEventClickState = click sequence number
//   - kCGMouseEventWindowUnderMousePointer = windowID
//   - kCGMouseEventWindowUnderMousePointerThatCanHandleThisEvent = windowID
//   - CGEventSetWindowLocation(windowLocalPt) [private SPI]
//   - SkyLight raw field 40 = pid (Chromium-required; iPhone Mirroring
//     suspected to require it too)
//
// Sequence: mouseMoved at target, then one leftMouseDown/leftMouseUp pair per
// click. Off-screen primer click is omitted - that's a Chromium
// user-activation-gate workaround unrelated to iPhone Mirroring.
func MouseClick(pid int32, screenPt, windowLocalPt Point, windowID uint32, clickCount int) error {
	if err := validatePID(pid); err != nil {
		return err
	}
	if clickCount < 1 {
		return fmt.Errorf("click count must be positive")
	}
	resolve()
	if resolveErr != nil {
		return resolveErr
	}
	trace("click.begin", map[string]any{
		"pid":         pid,
		"wid":         windowID,
		"click_count": clickCount,
		"screen_x":    screenPt.X,
		"screen_y":    screenPt.Y,
		"local_x":     windowLocalPt.X,
		"local_y":     windowLocalPt.Y,
	})
	if windowID != 0 {
		if err := ActivateWithoutRaise(pid, windowID); err != nil {
			return fmt.Errorf("activate: %w", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	move, err := makeMouseEvent(coregraphics.KCGEventMouseMoved, screenPt)
	if err != nil {
		return err
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(move))
	stampMouseEvent(move, pid, windowID, windowLocalPt, 1)

	rc, err := skylight.SLEventPostToPid(pid, move)
	trace("click.post.moved", map[string]any{"rc": rc, "err": err})
	if err != nil {
		return fmt.Errorf("SLEventPostToPid mouseMoved: %w", err)
	}
	time.Sleep(15 * time.Millisecond)

	for i := 1; i <= clickCount; i++ {
		if err := postMouseClickPair(pid, screenPt, windowLocalPt, windowID, int64(i)); err != nil {
			return err
		}
		if i < clickCount {
			time.Sleep(50 * time.Millisecond)
		}
	}
	return nil
}

func postMouseClickPair(pid int32, screenPt, windowLocalPt Point, windowID uint32, clickState int64) error {
	down, err := makeMouseEvent(coregraphics.KCGEventLeftMouseDown, screenPt)
	if err != nil {
		return err
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(down))
	stampMouseEvent(down, pid, windowID, windowLocalPt, clickState)

	up, err := makeMouseEvent(coregraphics.KCGEventLeftMouseUp, screenPt)
	if err != nil {
		return err
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(up))
	stampMouseEvent(up, pid, windowID, windowLocalPt, clickState)

	rc, err := skylight.SLEventPostToPid(pid, down)
	trace("click.post.down", map[string]any{"click_state": clickState, "rc": rc, "err": err})
	if err != nil {
		return fmt.Errorf("SLEventPostToPid down: %w", err)
	}
	time.Sleep(time.Millisecond)
	rc, err = skylight.SLEventPostToPid(pid, up)
	trace("click.post.up", map[string]any{"click_state": clickState, "rc": rc, "err": err})
	if err != nil {
		return fmt.Errorf("SLEventPostToPid up: %w", err)
	}
	return nil
}

// KeyPress synthesizes a keyboard down/up pair and posts it to pid through
// SkyLight. When attachAuthMessage is true, each event gets the
// SLSEventAuthenticationMessage envelope Chromium-family apps require for
// background keyboard input.
func KeyPress(pid int32, keyCode uint16, flags coregraphics.CGEventFlags, attachAuthMessage bool) error {
	if err := validatePID(pid); err != nil {
		return err
	}
	resolve()
	if resolveErr != nil {
		return resolveErr
	}
	down, err := makeKeyboardEvent(keyCode, true, flags)
	if err != nil {
		return err
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(down))
	up, err := makeKeyboardEvent(keyCode, false, flags)
	if err != nil {
		return err
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(up))
	if attachAuthMessage {
		if err := attachAuthenticationMessage(down, pid); err != nil {
			return fmt.Errorf("auth message down: %w", err)
		}
		if err := attachAuthenticationMessage(up, pid); err != nil {
			return fmt.Errorf("auth message up: %w", err)
		}
	}
	rc, err := skylight.SLEventPostToPid(pid, down)
	trace("key.post.down", map[string]any{"pid": pid, "keycode": keyCode, "rc": rc, "err": err})
	if err != nil {
		return fmt.Errorf("SLEventPostToPid keyDown: %w", err)
	}
	time.Sleep(10 * time.Millisecond)
	rc, err = skylight.SLEventPostToPid(pid, up)
	trace("key.post.up", map[string]any{"pid": pid, "keycode": keyCode, "rc": rc, "err": err})
	if err != nil {
		return fmt.Errorf("SLEventPostToPid keyUp: %w", err)
	}
	return nil
}

func validatePID(pid int32) error {
	if pid <= 0 {
		return fmt.Errorf("pid must be positive")
	}
	return nil
}

func makeKeyboardEvent(keyCode uint16, down bool, flags coregraphics.CGEventFlags) (coregraphics.CGEventRef, error) {
	ev := coregraphics.CGEventCreateKeyboardEvent(0, keyCode, down)
	if ev == 0 {
		return 0, fmt.Errorf("CGEventCreateKeyboardEvent failed")
	}
	coregraphics.CGEventSetFlags(ev, flags)
	return ev, nil
}

// makeMouseEvent builds a CGEvent at screenPt using the HIDSystemState
// source so receivers that gate on event source identity (some Continuity
// targets) accept it as hardware-originated.
func makeMouseEvent(eventType coregraphics.CGEventType, screenPt Point) (coregraphics.CGEventRef, error) {
	src := coregraphics.CGEventSourceCreate(coregraphics.KCGEventSourceStateHIDSystemState)
	if src == 0 {
		return 0, fmt.Errorf("CGEventSourceCreate failed")
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(src))
	cgPt := corefoundation.CGPoint{X: screenPt.X, Y: screenPt.Y}
	ev := coregraphics.CGEventCreateMouseEvent(
		src,
		eventType,
		cgPt,
		coregraphics.KCGMouseButtonLeft,
	)
	if ev == 0 {
		return 0, fmt.Errorf("CGEventCreateMouseEvent failed")
	}
	return ev, nil
}

func stampMouseEvent(event coregraphics.CGEventRef, pid int32, windowID uint32, windowLocalPt Point, clickState int64) {
	coregraphics.CGEventSetIntegerValueField(event, coregraphics.KCGMouseEventButtonNumber, 0)
	coregraphics.CGEventSetIntegerValueField(event, coregraphics.KCGMouseEventSubtype, 3)
	coregraphics.CGEventSetIntegerValueField(event, coregraphics.KCGMouseEventClickState, clickState)
	if windowID != 0 {
		coregraphics.CGEventSetIntegerValueField(event,
			coregraphics.KCGMouseEventWindowUnderMousePointer, int64(windowID))
		coregraphics.CGEventSetIntegerValueField(event,
			coregraphics.KCGMouseEventWindowUnderMousePointerThatCanHandleThisEvent, int64(windowID))
	}
	if err := skylight.CGEventSetWindowLocation(event, corefoundation.CGPoint{X: windowLocalPt.X, Y: windowLocalPt.Y}); err != nil {
		trace("click.stamp.window_location", map[string]any{"err": err})
	}
	// Field 40 = kCGEventTargetUnixProcessID. Chromium's renderer-side
	// filter latches onto this; iPhone Mirroring is suspected to do the
	// same. Use the raw-field SPI rather than the public one so we can
	// stamp it on events the public field-id table doesn't accept.
	if err := skylight.SLEventSetIntegerValueField(event, coregraphics.KCGEventTargetUnixProcessID, int64(pid)); err != nil {
		trace("click.stamp.target_pid", map[string]any{"err": err})
	}
}
