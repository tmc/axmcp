package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/screencapturekit"
	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/xcmcp/internal/ui"
)

type windowInfo struct {
	WindowID  uint32  `json:"window_id"`
	Title     string  `json:"title"`
	OwnerName string  `json:"owner_name"`
	OwnerPID  int64   `json:"owner_pid"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	Width     float64 `json:"width"`
	Height    float64 `json:"height"`
}

func (w windowInfo) rect() (corefoundation.CGRect, bool) {
	if w.Width <= 0 || w.Height <= 0 {
		return corefoundation.CGRect{}, false
	}
	return corefoundation.CGRect{
		Origin: corefoundation.CGPoint{
			X: w.X,
			Y: w.Y,
		},
		Size: corefoundation.CGSize{
			Width:  w.Width,
			Height: w.Height,
		},
	}, true
}

// cfStringToGo extracts a Go string from a CFStringRef.
func cfStringToGo(ref corefoundation.CFStringRef) string {
	if ref == 0 {
		return ""
	}
	buf := make([]byte, 1024)
	if corefoundation.CFStringGetCString(ref, &buf[0], len(buf), 0x08000100) {
		for i, b := range buf {
			if b == 0 {
				return string(buf[:i])
			}
		}
	}
	return ""
}

func makeCFString(s string) corefoundation.CFStringRef {
	return corefoundation.CFStringCreateWithCString(0, s, 0x08000100)
}

// cfPointer converts a CF reference (stored as uintptr) to unsafe.Pointer
// for use with CoreFoundation dictionary APIs.
func cfPointer(ref uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&ref))
}

func dictGetString(dict corefoundation.CFDictionaryRef, key string) string {
	k := makeCFString(key)
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(k))
	v := corefoundation.CFDictionaryGetValue(dict, cfPointer(uintptr(k)))
	if v == nil {
		return ""
	}
	return cfStringToGo(corefoundation.CFStringRef(uintptr(v)))
}

func dictGetNumber(dict corefoundation.CFDictionaryRef, key string) (int64, bool) {
	k := makeCFString(key)
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(k))
	v := corefoundation.CFDictionaryGetValue(dict, cfPointer(uintptr(k)))
	if v == nil {
		return 0, false
	}
	ref := corefoundation.CFNumberRef(uintptr(v))
	var n int64
	if corefoundation.CFNumberGetValue(ref, corefoundation.KCFNumberSInt64Type, unsafe.Pointer(&n)) {
		return n, true
	}
	var n32 int32
	if corefoundation.CFNumberGetValue(ref, corefoundation.KCFNumberSInt32Type, unsafe.Pointer(&n32)) {
		return int64(n32), true
	}
	return 0, false
}

func dictGetDictionary(dict corefoundation.CFDictionaryRef, key string) (corefoundation.CFDictionaryRef, bool) {
	k := makeCFString(key)
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(k))
	v := corefoundation.CFDictionaryGetValue(dict, cfPointer(uintptr(k)))
	if v == nil {
		return 0, false
	}
	return corefoundation.CFDictionaryRef(uintptr(v)), true
}

func dictGetRect(dict corefoundation.CFDictionaryRef, key string) (corefoundation.CGRect, bool) {
	bounds, ok := dictGetDictionary(dict, key)
	if !ok {
		return corefoundation.CGRect{}, false
	}
	var rect corefoundation.CGRect
	if !coregraphics.CGRectMakeWithDictionaryRepresentation(bounds, &rect) {
		return corefoundation.CGRect{}, false
	}
	return rect, true
}

func windowOwnerMatchesIdentifier(win windowInfo, appIdentifier string) bool {
	appIdentifier = strings.TrimSpace(appIdentifier)
	if appIdentifier == "" {
		return false
	}
	if pid, err := strconv.ParseInt(appIdentifier, 10, 64); err == nil {
		return win.OwnerPID == pid
	}

	ownerLower := strings.ToLower(win.OwnerName)
	queryLower := strings.ToLower(appIdentifier)
	return strings.EqualFold(win.OwnerName, appIdentifier) ||
		strings.Contains(ownerLower, queryLower) ||
		strings.Contains(queryLower, ownerLower)
}

// listAppWindows returns on-screen windows matching the given app identifier.
func listAppWindows(appIdentifier string) ([]windowInfo, error) {
	windowList := coregraphics.CGWindowListCopyWindowInfo(
		coregraphics.KCGWindowListOptionOnScreenOnly,
		0,
	)
	if windowList == 0 {
		return nil, fmt.Errorf("CGWindowListCopyWindowInfo returned nil")
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(windowList))

	count := corefoundation.CFArrayGetCount(windowList)
	var windows []windowInfo
	for i := range count {
		dictPtr := corefoundation.CFArrayGetValueAtIndex(windowList, i)
		dict := corefoundation.CFDictionaryRef(uintptr(dictPtr))

		ownerName := dictGetString(dict, coregraphics.KCGWindowOwnerName)
		windowName := dictGetString(dict, coregraphics.KCGWindowName)
		ownerPID, _ := dictGetNumber(dict, coregraphics.KCGWindowOwnerPID)
		windowID, _ := dictGetNumber(dict, coregraphics.KCGWindowNumber)
		bounds, _ := dictGetRect(dict, coregraphics.KCGWindowBounds)

		win := windowInfo{
			WindowID:  uint32(windowID),
			Title:     windowName,
			OwnerName: ownerName,
			OwnerPID:  ownerPID,
			X:         bounds.Origin.X,
			Y:         bounds.Origin.Y,
			Width:     bounds.Size.Width,
			Height:    bounds.Size.Height,
		}
		if !windowOwnerMatchesIdentifier(win, appIdentifier) {
			continue
		}

		windows = append(windows, win)
	}
	if len(windows) == 0 {
		return nil, fmt.Errorf("no windows found for %q", appIdentifier)
	}
	return windows, nil
}

func matchWindowInfo(windows []windowInfo, titles ...string) (windowInfo, bool) {
	for _, title := range titles {
		title = strings.TrimSpace(title)
		if title == "" {
			continue
		}
		for _, win := range windows {
			if strings.EqualFold(strings.TrimSpace(win.Title), title) {
				return win, true
			}
		}
	}
	for _, title := range titles {
		title = strings.TrimSpace(title)
		if title == "" {
			continue
		}
		lower := strings.ToLower(title)
		for _, win := range windows {
			if strings.Contains(strings.ToLower(win.Title), lower) {
				return win, true
			}
		}
	}
	return windowInfo{}, false
}

func captureWindow(win windowInfo) ([]byte, error) {
	diagf("captureWindow: title=%q owner=%q id=%d\n", win.Title, win.OwnerName, win.WindowID)

	var errs []string
	appendErr := func(label string, err error) {
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", label, err))
		}
	}

	if png, err := captureWindowCG(win); err == nil {
		return png, nil
	} else {
		diagf("captureWindow: CG failed: %v\n", err)
		appendErr("CGWindowListCreateImage", err)
	}

	if !ui.IsScreenRecordingTrusted() {
		if !ui.WaitForScreenRecording(30 * time.Second) {
			return nil, fmt.Errorf("screenshot failed: Screen Recording permission required — grant access in System Settings > Privacy & Security")
		}
	}

	// Fall back to screencapture on the window's bounds rather than
	// ScreenCaptureKit. The latter can terminate the app from inside AppKit's
	// run loop, which takes down the stdio transport.
	if png, err := captureWindowRect(win); err == nil {
		return png, nil
	} else {
		diagf("captureWindow: rect fallback failed: %v\n", err)
		appendErr("screencapture -R", err)
	}

	if len(errs) == 0 {
		return nil, fmt.Errorf("capture window %q (id=%d): failed", win.Title, win.WindowID)
	}
	return nil, fmt.Errorf("capture window %q (id=%d): %s", win.Title, win.WindowID, strings.Join(errs, "; "))
}

// captureWindowCG captures a window screenshot using CGWindowListCreateImage.
// This uses the legacy CoreGraphics API which runs synchronously on the calling
// thread, avoiding the ScreenCaptureKit dispatch that causes process termination.
func captureWindowCG(win windowInfo) ([]byte, error) {
	diagf("captureWindowCG: start windowID=%d\n", win.WindowID)

	type attempt struct {
		name  string
		rect  corefoundation.CGRect
		valid bool
		opts  coregraphics.CGWindowImageOption
	}

	rect, hasRect := win.rect()
	attempts := []attempt{
		{
			name:  "window bounds ignore framing, best resolution",
			opts:  coregraphics.KCGWindowImageBoundsIgnoreFraming | coregraphics.KCGWindowImageBestResolution,
			valid: true,
		},
		{
			name:  "window bounds explicit rect",
			rect:  rect,
			valid: hasRect,
			opts:  coregraphics.KCGWindowImageBoundsIgnoreFraming | coregraphics.KCGWindowImageBestResolution,
		},
		{
			name:  "window bounds nominal resolution",
			rect:  rect,
			valid: hasRect,
			opts:  coregraphics.KCGWindowImageBoundsIgnoreFraming | coregraphics.KCGWindowImageNominalResolution,
		},
	}
	var errs []string
	for _, attempt := range attempts {
		if !attempt.valid {
			continue
		}
		for retry := 0; retry < 3; retry++ {
			img := coregraphics.CGWindowListCreateImage(
				attempt.rect,
				coregraphics.KCGWindowListOptionIncludingWindow,
				coregraphics.CGWindowID(win.WindowID),
				attempt.opts,
			)
			if img != 0 {
				defer coregraphics.CGImageRelease(img)
				diagf("captureWindowCG: got image %dx%d via %s retry=%d\n",
					coregraphics.CGImageGetWidth(img), coregraphics.CGImageGetHeight(img), attempt.name, retry)
				return cgImageToPNG(img)
			}
			if retry < 2 {
				time.Sleep(50 * time.Millisecond)
			}
		}
		errs = append(errs, attempt.name)
	}
	return nil, fmt.Errorf("CGWindowListCreateImage returned nil for window %d (%s)", win.WindowID, strings.Join(errs, ", "))
}

func captureWindowRect(win windowInfo) ([]byte, error) {
	rect, ok := win.rect()
	if !ok {
		return nil, fmt.Errorf("window %d has empty bounds", win.WindowID)
	}
	return captureRect(rect)
}

func captureRect(rect corefoundation.CGRect) ([]byte, error) {
	if rect.Size.Width <= 0 || rect.Size.Height <= 0 {
		return nil, fmt.Errorf("empty capture rect")
	}
	f, err := os.CreateTemp("", "axmcp-window-*.png")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		os.Remove(name)
		return nil, fmt.Errorf("close temp file: %w", err)
	}
	defer os.Remove(name)

	rectArg := fmt.Sprintf("%d,%d,%d,%d",
		int(rect.Origin.X),
		int(rect.Origin.Y),
		int(rect.Size.Width),
		int(rect.Size.Height),
	)
	diagf("captureRect: screencapture -R %s\n", rectArg)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "screencapture", "-x", "-R", rectArg, "-t", "png", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("screencapture %s timed out", rectArg)
		}
		return nil, fmt.Errorf("screencapture %s: %w: %s", rectArg, err, strings.TrimSpace(string(out)))
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read temp screenshot: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("screencapture %s produced empty output", rectArg)
	}
	diagf("captureRect: got %d bytes\n", len(data))
	return data, nil
}

// captureElementWithPadding captures a screenshot of the area around an AX
// element, expanded by the given padding in pixels on all sides. Uses
// screencapture -R on the padded rect.
func captureElementWithPadding(el *axuiautomation.Element, padding int) ([]byte, error) {
	if el == nil {
		return nil, fmt.Errorf("nil element")
	}
	if !ui.IsScreenRecordingTrusted() {
		if !ui.WaitForScreenRecording(30 * time.Second) {
			return nil, fmt.Errorf("screenshot failed: Screen Recording permission required")
		}
	}
	frame := el.Frame()
	p := float64(padding)
	padded := corefoundation.CGRect{
		Origin: corefoundation.CGPoint{
			X: frame.Origin.X - p,
			Y: frame.Origin.Y - p,
		},
		Size: corefoundation.CGSize{
			Width:  frame.Size.Width + 2*p,
			Height: frame.Size.Height + 2*p,
		},
	}
	if padded.Origin.X < 0 {
		padded.Size.Width += padded.Origin.X
		padded.Origin.X = 0
	}
	if padded.Origin.Y < 0 {
		padded.Size.Height += padded.Origin.Y
		padded.Origin.Y = 0
	}
	return captureRect(padded)
}

// captureWindowSCK captures a window screenshot using ScreenCaptureKit.
// WARNING: SCK dispatches work to the main thread which can trigger process
// termination when NSApplication.run() is driving the event loop. Prefer
// captureWindowCG where possible.
func captureWindowSCK(ctx context.Context, windowID uint32) ([]byte, error) {
	diagf("captureWindowSCK: start windowID=%d\n", windowID)

	content, err := screencapturekit.GetSCShareableContentClass().GetShareableContent(ctx)
	if err != nil {
		return nil, fmt.Errorf("get shareable content: %w", err)
	}

	objc.Send[objc.ID](content.ID, objc.Sel("retain"))
	defer objc.Send[objc.ID](content.ID, objc.Sel("release"))

	windows := content.Windows()
	var target screencapturekit.SCWindow
	for _, w := range windows {
		if w.WindowID() == windowID {
			target = w
			break
		}
	}
	if target.GetID() == 0 {
		return nil, fmt.Errorf("window %d not found in shareable content", windowID)
	}

	filter := screencapturekit.NewContentFilterWithDesktopIndependentWindow(&target)
	config := screencapturekit.NewSCStreamConfiguration()
	frame := target.Frame()
	config.SetWidth(uintptr(frame.Size.Width * 2))
	config.SetHeight(uintptr(frame.Size.Height * 2))

	img, err := screencapturekit.GetSCScreenshotManagerClass().CaptureImageWithFilterConfiguration(ctx, &filter, &config)
	if err != nil {
		return nil, fmt.Errorf("capture image: %w", err)
	}
	defer coregraphics.CGImageRelease(img)

	return cgImageToPNG(img)
}

// captureFullScreen captures the entire main display using ScreenCaptureKit.
// Requires explicit opt-in because the resulting image is large.
func captureFullScreen() ([]byte, error) {
	if !ui.IsScreenRecordingTrusted() {
		if !ui.WaitForScreenRecording(30 * time.Second) {
			return nil, fmt.Errorf("screenshot failed: Screen Recording permission required — grant access in System Settings > Privacy & Security")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	content, err := screencapturekit.GetSCShareableContentClass().GetShareableContent(ctx)
	if err != nil {
		return nil, fmt.Errorf("get shareable content: %w", err)
	}

	displays := content.Displays()
	if len(displays) == 0 {
		return nil, fmt.Errorf("no displays found")
	}

	display := displays[0]
	filter := screencapturekit.NewContentFilterWithDisplayExcludingWindows(&display, nil)
	config := screencapturekit.NewSCStreamConfiguration()
	config.SetWidth(uintptr(display.Width() * 2))
	config.SetHeight(uintptr(display.Height() * 2))

	img, err := screencapturekit.GetSCScreenshotManagerClass().CaptureImageWithFilterConfiguration(ctx, &filter, &config)
	if err != nil {
		return nil, fmt.Errorf("capture display: %w", err)
	}
	defer coregraphics.CGImageRelease(img)

	return cgImageToPNG(img)
}

// cgImageToPNG converts a CGImageRef to PNG-encoded bytes.
func cgImageToPNG(img coregraphics.CGImageRef) ([]byte, error) {
	if img == 0 {
		return nil, fmt.Errorf("nil CGImage")
	}
	rep := appkit.NewBitmapImageRepWithCGImage(img)
	if rep.GetID() == 0 {
		return nil, fmt.Errorf("failed to create NSBitmapImageRep")
	}
	data := rep.RepresentationUsingTypeProperties(appkit.NSBitmapImageFileTypePNG, nil)
	if data == nil {
		return nil, fmt.Errorf("failed to create PNG representation")
	}
	length := data.Length()
	if length == 0 {
		return nil, fmt.Errorf("empty PNG data")
	}
	raw := unsafe.Slice((*byte)(data.Bytes()), length)
	result := make([]byte, length)
	copy(result, raw)
	return result, nil
}
