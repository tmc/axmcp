//go:build darwin

package focusless

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/applicationservices"
	"github.com/tmc/apple/corefoundation"
	privskylight "github.com/tmc/apple/private/skylight"
	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/apple/x/skylight"
)

// ErrUnavailable is wrapped into every error that stems from SkyLight being
// unreachable rather than from the request itself. Branch on it with
// errors.Is; the sentinel is always wrapped, so == will not match.
var ErrUnavailable = errors.New("focusless: SkyLight unavailable")

// Snapshot is the observable focus state at one instant.
//
// FrontPID and WorkspacePID come from two different authorities and can
// disagree: the window server's frontmost process is what menu dispatch
// consults, while NSWorkspace reports what AppKit considers active.
type Snapshot struct {
	FrontPID     int    `json:"front_pid"`
	WorkspacePID int    `json:"workspace_pid"`
	ActiveSpace  uint64 `json:"active_space"`
}

// Capture reads the current focus state.
func Capture() (Snapshot, error) {
	var s Snapshot

	space, err := skylight.ActiveSpace()
	if err != nil {
		return s, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	s.ActiveSpace = uint64(space)

	var psn applicationservices.ProcessSerialNumber
	status, err := privskylight.SLPSGetFrontProcess(&psn)
	if err != nil {
		return s, fmt.Errorf("%w: SLPSGetFrontProcess: %v", ErrUnavailable, err)
	}
	if status != 0 {
		return s, fmt.Errorf("focusless: SLPSGetFrontProcess returned status %d", status)
	}
	// GetProcessPID is deprecated but has no replacement that accepts a PSN,
	// and SLPSGetFrontProcess only speaks PSNs.
	var pid int32
	if status := applicationservices.GetProcessPID(&psn, &pid); status != 0 {
		return s, fmt.Errorf("focusless: GetProcessPID returned status %d", status)
	}
	s.FrontPID = int(pid)

	if front := appkit.GetNSWorkspaceClass().SharedWorkspace().FrontmostApplication(); front != nil {
		s.WorkspacePID = int(front.ProcessIdentifier())
	}
	return s, nil
}

// FocusedWindow returns the CGWindowID that accessibility reports as the
// focused window of pid, or 0 when the process reports none.
func FocusedWindow(pid int) uint32 {
	app := axuiautomation.AXUIElementCreateApplication(int32(pid))
	if app == 0 {
		return 0
	}
	defer corefoundation.CFRelease(cfPointer(uintptr(app)))

	attr := corefoundation.CFStringCreateWithCString(0, "AXFocusedWindow", kCFStringEncodingUTF8)
	defer corefoundation.CFRelease(cfPointer(uintptr(attr)))

	var window uintptr
	if axuiautomation.AXUIElementCopyAttributeValue(axuiautomation.AXUIElementRef(app), uintptr(attr), &window) != 0 || window == 0 {
		return 0
	}
	defer corefoundation.CFRelease(cfPointer(window))

	var wid uint32
	if axuiautomation.AXUIElementGetWindow(axuiautomation.AXUIElementRef(window), &wid) != 0 {
		return 0
	}
	return wid
}

const kCFStringEncodingUTF8 = 0x08000100

// cfPointer reinterprets a CF handle as the unsafe.Pointer the CF APIs take.
// The conversion sits behind a call boundary because vet's unsafeptr check
// cannot tell a CF handle from an integer address, and these are handles.
func cfPointer(ref uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&ref))
}

// IsOffSpace reports whether wid lives on a Space other than the active one.
//
// A window the window server has no Space membership for — a stale id, or a
// transient system window — is an error rather than a verdict. This is not what
// skylight.IsWindowOffSpace does: it reports empty membership as off-Space, so
// IsOffSpace(0) comes back (true, nil) there. Callers use the answer to decide
// whether input will be misrouted, and "unknown" must not read as "yes".
func IsOffSpace(wid uint32) (bool, error) {
	active, err := skylight.ActiveSpace()
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	spaces, err := skylight.SpacesForWindow(skylight.Window(wid))
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if len(spaces) == 0 {
		return false, fmt.Errorf("focusless: window %d has no Space membership", wid)
	}
	for _, s := range spaces {
		if s == active {
			return false, nil
		}
	}
	return true, nil
}
