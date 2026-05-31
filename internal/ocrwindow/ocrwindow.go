//go:build darwin

// Package ocrwindow captures a macOS application window and runs Apple Vision
// OCR on the result. It is a small, self-contained subset of the helpers in
// cmd/axmcp; the goal is a stable internal surface that other commands
// (notably cmd/iphonemirror-mcp) can share without copying the full file.
package ocrwindow

import (
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"unsafe"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/vision"
)

// Window describes a single on-screen window owned by an application.
type Window struct {
	ID        uint32
	Title     string
	OwnerName string
	OwnerPID  int64
	X, Y      float64
	W, H      float64
}

// Hit is one piece of recognized text with its bounding box in pixel
// coordinates relative to the captured PNG (origin top-left).
type Hit struct {
	Text       string
	Confidence float32
	X, Y, W, H int
}

// FindWindow returns the first on-screen window whose owning process matches
// appIdentifier. The identifier is matched against the window's ownerName
// (case-insensitive substring) or the bundle name.
func FindWindow(appIdentifier string) (Window, error) {
	wins, err := listAppWindows(appIdentifier, coregraphics.KCGWindowListOptionOnScreenOnly)
	if err != nil || len(wins) == 0 {
		return Window{}, fmt.Errorf("no on-screen windows for %q", appIdentifier)
	}
	return freshWindowBounds(wins[0], appIdentifier)
}

// ListWindows returns the on-screen windows whose owning process matches
// appIdentifier.
func ListWindows(appIdentifier string) ([]Window, error) {
	return listAppWindows(appIdentifier, coregraphics.KCGWindowListOptionOnScreenOnly)
}

// FindWindowID returns the on-screen window with the given Core Graphics
// window id whose owning process matches appIdentifier.
func FindWindowID(appIdentifier string, id uint32) (Window, error) {
	wins, err := ListWindows(appIdentifier)
	if err != nil {
		return Window{}, err
	}
	for _, win := range wins {
		if win.ID == id {
			return win, nil
		}
	}
	return Window{}, fmt.Errorf("window %d not found for %q", id, appIdentifier)
}

// Capture returns a PNG-encoded screenshot of the given window, plus the
// pixel dimensions of the encoded image (which may exceed the logical
// window size on Retina displays).
func Capture(win Window) (png []byte, imgW, imgH int, err error) {
	rect, hasRect := win.cgRect()
	attempts := []struct {
		rect  corefoundation.CGRect
		valid bool
		opts  coregraphics.CGWindowImageOption
		name  string
	}{
		{
			opts:  coregraphics.KCGWindowImageBoundsIgnoreFraming | coregraphics.KCGWindowImageBestResolution,
			valid: true,
			name:  "default bounds, best resolution",
		},
		{
			rect:  rect,
			valid: hasRect,
			opts:  coregraphics.KCGWindowImageBoundsIgnoreFraming | coregraphics.KCGWindowImageBestResolution,
			name:  "explicit rect, best resolution",
		},
		{
			rect:  rect,
			valid: hasRect,
			opts:  coregraphics.KCGWindowImageBoundsIgnoreFraming | coregraphics.KCGWindowImageNominalResolution,
			name:  "explicit rect, nominal resolution",
		},
	}
	var failures []string
	for _, a := range attempts {
		if !a.valid {
			continue
		}
		img := coregraphics.CGWindowListCreateImage(
			a.rect,
			coregraphics.KCGWindowListOptionIncludingWindow,
			coregraphics.CGWindowID(win.ID),
			a.opts,
		)
		if img == 0 {
			failures = append(failures, a.name)
			continue
		}
		w := int(coregraphics.CGImageGetWidth(img))
		h := int(coregraphics.CGImageGetHeight(img))
		data, encErr := encodePNG(img)
		coregraphics.CGImageRelease(img)
		if encErr != nil {
			return nil, 0, 0, fmt.Errorf("encode png: %w", encErr)
		}
		return data, w, h, nil
	}
	return nil, 0, 0, fmt.Errorf("CGWindowListCreateImage returned nil for window %d (%s)",
		win.ID, strings.Join(failures, ", "))
}

// Recognize runs Apple Vision OCR on PNG-encoded image data. Returned hits
// have pixel coordinates relative to the image (origin top-left).
func Recognize(pngData []byte, imgW, imgH int) ([]Hit, error) {
	nsData := foundation.NewDataWithBytesLength(pngData)
	handler := vision.NewImageRequestHandlerWithDataOptions(nsData, nil)

	request := vision.NewVNRecognizeTextRequest()
	request.SetRecognitionLevel(vision.VNRequestTextRecognitionLevelAccurate)
	request.SetUsesLanguageCorrection(true)

	ok, err := handler.PerformRequestsError([]vision.VNRequest{request.VNImageBasedRequest.VNRequest})
	if err != nil {
		return nil, fmt.Errorf("vision OCR: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("vision OCR: request failed")
	}

	observations := request.VNImageBasedRequest.VNRequest.Results()
	var hits []Hit
	seen := map[string]bool{}
	for _, obs := range observations {
		textObs := vision.VNRecognizedTextObservationFromID(obs.ID)
		bb := textObs.BoundingBox()
		for _, c := range textObs.TopCandidates(3) {
			// Vision boxes: normalized 0-1, origin bottom-left. Convert to
			// pixel coords with origin top-left.
			px := int(math.Round(bb.Origin.X * float64(imgW)))
			py := int(math.Round((1 - bb.Origin.Y - bb.Size.Height) * float64(imgH)))
			pw := int(math.Round(bb.Size.Width * float64(imgW)))
			ph := int(math.Round(bb.Size.Height * float64(imgH)))
			key := fmt.Sprintf("%s|%d|%d|%d|%d", c.String(), px, py, pw, ph)
			if seen[key] {
				continue
			}
			seen[key] = true
			hits = append(hits, Hit{
				Text:       c.String(),
				Confidence: float32(c.Confidence()),
				X:          px, Y: py, W: pw, H: ph,
			})
		}
	}
	return hits, nil
}

func (w Window) cgRect() (corefoundation.CGRect, bool) {
	if w.W <= 0 || w.H <= 0 {
		return corefoundation.CGRect{}, false
	}
	return corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: w.X, Y: w.Y},
		Size:   corefoundation.CGSize{Width: w.W, Height: w.H},
	}, true
}

func listAppWindows(appIdentifier string, option coregraphics.CGWindowListOption) ([]Window, error) {
	list := coregraphics.CGWindowListCopyWindowInfo(option, 0)
	if list == 0 {
		return nil, fmt.Errorf("CGWindowListCopyWindowInfo returned nil")
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(list))

	count := corefoundation.CFArrayGetCount(list)
	var out []Window
	for i := range count {
		dictPtr := corefoundation.CFArrayGetValueAtIndex(list, i)
		dict := corefoundation.CFDictionaryRef(uintptr(dictPtr))

		owner := dictString(dict, coregraphics.KCGWindowOwnerName)
		title := dictString(dict, coregraphics.KCGWindowName)
		pid, _ := dictNumber(dict, coregraphics.KCGWindowOwnerPID)
		id, _ := dictNumber(dict, coregraphics.KCGWindowNumber)
		bounds, _ := dictRect(dict, coregraphics.KCGWindowBounds)

		win := Window{
			ID:        uint32(id),
			Title:     title,
			OwnerName: owner,
			OwnerPID:  pid,
			X:         bounds.Origin.X,
			Y:         bounds.Origin.Y,
			W:         bounds.Size.Width,
			H:         bounds.Size.Height,
		}
		if !ownerMatches(win, appIdentifier) {
			continue
		}
		out = append(out, win)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no windows for %q", appIdentifier)
	}
	return out, nil
}

func freshWindowBounds(win Window, appIdentifier string) (Window, error) {
	ax, err := axWindowBounds(win.OwnerPID)
	if err != nil {
		return win, nil
	}
	if sameBounds(win, ax) {
		return withBounds(win, ax), nil
	}
	wins, err := listAppWindows(appIdentifier, coregraphics.KCGWindowListOptionOnScreenOnly)
	if err != nil {
		return Window{}, fmt.Errorf("refresh window bounds: %w", err)
	}
	for _, refreshed := range wins {
		if refreshed.ID == win.ID || refreshed.OwnerPID == win.OwnerPID {
			if sameBounds(refreshed, ax) {
				return withBounds(refreshed, ax), nil
			}
			return Window{}, fmt.Errorf("stale iPhone Mirroring window bounds: CG=(%.0f,%.0f %.0fx%.0f) AX=(%.0f,%.0f %.0fx%.0f)",
				refreshed.X, refreshed.Y, refreshed.W, refreshed.H, ax.X, ax.Y, ax.W, ax.H)
		}
	}
	return Window{}, fmt.Errorf("refresh window bounds: window %d disappeared", win.ID)
}

func axWindowBounds(pid int64) (Window, error) {
	if pid <= 0 {
		return Window{}, fmt.Errorf("invalid pid %d", pid)
	}
	script := fmt.Sprintf(`tell application "System Events"
  set p to first process whose unix id is %d
  set pos to position of window 1 of p
  set sz to size of window 1 of p
  return (item 1 of pos as text) & "," & (item 2 of pos as text) & "," & (item 1 of sz as text) & "," & (item 2 of sz as text)
end tell`, pid)
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		return Window{}, fmt.Errorf("osascript AX window bounds: %w: %s", err, strings.TrimSpace(string(out)))
	}
	fields := strings.Split(strings.TrimSpace(string(out)), ",")
	if len(fields) != 4 {
		return Window{}, fmt.Errorf("unexpected AX bounds %q", strings.TrimSpace(string(out)))
	}
	nums := [4]float64{}
	for i, field := range fields {
		n, err := strconv.ParseFloat(strings.TrimSpace(field), 64)
		if err != nil {
			return Window{}, fmt.Errorf("parse AX bounds %q: %w", field, err)
		}
		nums[i] = n
	}
	return Window{OwnerPID: pid, X: nums[0], Y: nums[1], W: nums[2], H: nums[3]}, nil
}

func sameBounds(a, b Window) bool {
	const tolerance = 4
	return math.Abs(a.X-b.X) <= tolerance &&
		math.Abs(a.Y-b.Y) <= tolerance &&
		math.Abs(a.W-b.W) <= tolerance &&
		math.Abs(a.H-b.H) <= tolerance
}

func withBounds(win, bounds Window) Window {
	win.X, win.Y, win.W, win.H = bounds.X, bounds.Y, bounds.W, bounds.H
	return win
}

func ownerMatches(w Window, id string) bool {
	if pid, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64); err == nil {
		return w.OwnerPID == pid
	}
	id = strings.ToLower(id)
	owner := strings.ToLower(w.OwnerName)
	return strings.Contains(owner, id) || strings.Contains(id, owner)
}

func dictString(dict corefoundation.CFDictionaryRef, key string) string {
	cfKey := makeCFString(key)
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(cfKey))
	val := corefoundation.CFDictionaryGetValue(dict, cfPointer(uintptr(cfKey)))
	if val == nil {
		return ""
	}
	return cfStringToGo(corefoundation.CFStringRef(uintptr(val)))
}

func dictNumber(dict corefoundation.CFDictionaryRef, key string) (int64, bool) {
	cfKey := makeCFString(key)
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(cfKey))
	val := corefoundation.CFDictionaryGetValue(dict, cfPointer(uintptr(cfKey)))
	if val == nil {
		return 0, false
	}
	num := corefoundation.CFNumberRef(uintptr(val))
	var n int64
	ok := corefoundation.CFNumberGetValue(num, corefoundation.KCFNumberSInt64Type, unsafe.Pointer(&n))
	return n, ok
}

func dictRect(dict corefoundation.CFDictionaryRef, key string) (corefoundation.CGRect, bool) {
	cfKey := makeCFString(key)
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(cfKey))
	val := corefoundation.CFDictionaryGetValue(dict, cfPointer(uintptr(cfKey)))
	if val == nil {
		return corefoundation.CGRect{}, false
	}
	var r corefoundation.CGRect
	ok := coregraphics.CGRectMakeWithDictionaryRepresentation(corefoundation.CFDictionaryRef(uintptr(val)), &r)
	return r, ok
}

func cfPointer(ref uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&ref))
}

// utf8Encoding is the kCFStringEncodingUTF8 constant. Inlined as a raw
// uint32 to avoid type-alias mismatches with the binding.
const utf8Encoding uint32 = 0x08000100

func makeCFString(s string) corefoundation.CFStringRef {
	return corefoundation.CFStringCreateWithCString(0, s, utf8Encoding)
}

func cfStringToGo(ref corefoundation.CFStringRef) string {
	if ref == 0 {
		return ""
	}
	buf := make([]byte, 1024)
	if !corefoundation.CFStringGetCString(ref, &buf[0], len(buf), utf8Encoding) {
		return ""
	}
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i])
		}
	}
	return ""
}

func encodePNG(img coregraphics.CGImageRef) ([]byte, error) {
	if img == 0 {
		return nil, fmt.Errorf("nil CGImage")
	}
	rep := appkit.NewBitmapImageRepWithCGImage(img)
	if rep.GetID() == 0 {
		return nil, fmt.Errorf("NewBitmapImageRepWithCGImage failed")
	}
	data := rep.RepresentationUsingTypeProperties(appkit.NSBitmapImageFileTypePNG, nil)
	if data.GetID() == 0 {
		return nil, fmt.Errorf("RepresentationUsingTypeProperties failed")
	}
	length := data.Length()
	if length == 0 {
		return nil, fmt.Errorf("empty PNG data")
	}
	raw := unsafe.Slice((*byte)(data.Bytes()), length)
	out := make([]byte, length)
	copy(out, raw)
	return out, nil
}
