package skylightinput

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ebitengine/purego"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
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

// init resolves private SPIs that are not part of the SkyLight binding slice.
// The SkyLight event posting/stamping functions come from tmc/apple. The
// remaining hand-resolved symbol is GetProcessForPID, a deprecated Carbon SPI
// that lives in ApplicationServices.framework, not SkyLight.
var (
	resolveOnce sync.Once

	getProcessForPID func(pid int32, psn *skylight.ProcessSerialNumber) int32

	// resolveStatus is a one-line summary of which SPIs are usable on the
	// current OS, suitable for logging at startup. Populated even on
	// success so callers can surface it for forensics.
	resolveStatus string
	resolveErr    error
)

func resolve() {
	resolveOnce.Do(func() {
		var diag []string
		hAS, err := purego.Dlopen(
			"/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices",
			purego.RTLD_LAZY|purego.RTLD_GLOBAL,
		)
		if err != nil {
			resolveErr = fmt.Errorf("dlopen ApplicationServices: %w", err)
			resolveStatus = resolveErr.Error()
			return
		}

		diag = append(diag, "CGEventSetWindowLocation=bound")
		diag = append(diag, "SLEventSetIntegerValueField=bound")
		// GetProcessForPID: deprecated Carbon, lives in ApplicationServices.
		if sym, _ := purego.Dlsym(hAS, "GetProcessForPID"); sym != 0 {
			purego.RegisterFunc(&getProcessForPID, sym)
			diag = append(diag, "GetProcessForPID=ok")
		} else {
			diag = append(diag, "GetProcessForPID=miss")
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
	if getProcessForPID == nil {
		return fmt.Errorf("%w: GetProcessForPID", ErrUnavailable)
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
//  2. GetProcessForPID(targetPID, &target).
//  3. SLPSPostEventRecordTo(prev, defocus-record).
//  4. SLPSPostEventRecordTo(target, focus-record-with-window-id).
//
// SLPSSetFrontProcessWithOptions is deliberately skipped: in yabai it raises
// the window; for our use case (driving an iPhone Mirroring window on
// possibly another Space) that's the foot-gun.
func ActivateWithoutRaise(targetPID int32, targetWindowID uint32) error {
	resolve()
	if resolveErr != nil {
		return resolveErr
	}
	if getProcessForPID == nil {
		return fmt.Errorf("%w: GetProcessForPID", ErrUnavailable)
	}
	var prev, target skylight.ProcessSerialNumber
	if rc, err := skylight.SLPSGetFrontProcess(&prev); err != nil {
		return fmt.Errorf("SLPSGetFrontProcess: %w", err)
	} else if rc != 0 {
		return fmt.Errorf("SLPSGetFrontProcess returned %d", rc)
	}
	if rc := getProcessForPID(targetPID, &target); rc != 0 {
		return fmt.Errorf("GetProcessForPID(%d) returned %d", targetPID, rc)
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

// MouseClick synthesises a single left-click at screenPt and posts it to
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
//   - kCGMouseEventClickState = 1
//   - kCGMouseEventWindowUnderMousePointer = windowID
//   - kCGMouseEventWindowUnderMousePointerThatCanHandleThisEvent = windowID
//   - CGEventSetWindowLocation(windowLocalPt) [private SPI]
//   - SkyLight raw field 40 = pid (Chromium-required; iPhone Mirroring
//     suspected to require it too)
//
// Sequence: mouseMoved at target, 15ms gap, leftMouseDown, 1ms gap,
// leftMouseUp. Off-screen primer click is omitted - that's a Chromium
// user-activation-gate workaround unrelated to iPhone Mirroring.
func MouseClick(pid int32, screenPt, windowLocalPt Point, windowID uint32) error {
	resolve()
	if resolveErr != nil {
		return resolveErr
	}
	trace("click.begin", map[string]any{
		"pid":      pid,
		"wid":      windowID,
		"screen_x": screenPt.X,
		"screen_y": screenPt.Y,
		"local_x":  windowLocalPt.X,
		"local_y":  windowLocalPt.Y,
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

	down, err := makeMouseEvent(coregraphics.KCGEventLeftMouseDown, screenPt)
	if err != nil {
		return err
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(down))
	stampMouseEvent(down, pid, windowID, windowLocalPt, 1)

	up, err := makeMouseEvent(coregraphics.KCGEventLeftMouseUp, screenPt)
	if err != nil {
		return err
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(up))
	stampMouseEvent(up, pid, windowID, windowLocalPt, 1)

	rc, err := skylight.SLEventPostToPid(pid, move)
	trace("click.post.moved", map[string]any{"rc": rc, "err": err})
	if err != nil {
		return fmt.Errorf("SLEventPostToPid mouseMoved: %w", err)
	}
	time.Sleep(15 * time.Millisecond)
	rc, err = skylight.SLEventPostToPid(pid, down)
	trace("click.post.down", map[string]any{"rc": rc, "err": err})
	if err != nil {
		return fmt.Errorf("SLEventPostToPid down: %w", err)
	}
	time.Sleep(time.Millisecond)
	rc, err = skylight.SLEventPostToPid(pid, up)
	trace("click.post.up", map[string]any{"rc": rc, "err": err})
	if err != nil {
		return fmt.Errorf("SLEventPostToPid up: %w", err)
	}
	return nil
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
