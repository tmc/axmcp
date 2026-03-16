package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/screencapturekit"
)

// Manual AX Bindings
var (
	axCreateApplication           func(int32) uintptr
	axCopyAttributeValue          func(uintptr, uintptr, *uintptr) int32
	axCopyAttributeNames          func(uintptr, *uintptr) int32
	axPerformAction               func(uintptr, uintptr) int32
	axUIElementGetPid             func(uintptr, *int32) int32
	axIsProcessTrusted            func() bool
	axIsProcessTrustedWithOptions func(uintptr) bool
	axValueGetValue               func(uintptr, int32, unsafe.Pointer) bool
)

const (
	kAXValueTypeCGPoint = 1
	kAXValueTypeCGSize  = 2
)

// CoreFoundation Bindings
var (
	cfStringCreateWithCString func(uintptr, unsafe.Pointer, uint32) uintptr
)

const (
	kCFStringEncodingUTF8 = uint32(0x08000100)
)

func init() {
	lib, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", purego.RTLD_GLOBAL)
	if err != nil {
		fmt.Printf("Error loading ApplicationServices: %v\n", err)
		return
	}
	purego.RegisterLibFunc(&axCreateApplication, lib, "AXUIElementCreateApplication")
	purego.RegisterLibFunc(&axCopyAttributeValue, lib, "AXUIElementCopyAttributeValue")
	purego.RegisterLibFunc(&axCopyAttributeNames, lib, "AXUIElementCopyAttributeNames")
	purego.RegisterLibFunc(&axPerformAction, lib, "AXUIElementPerformAction")
	purego.RegisterLibFunc(&axUIElementGetPid, lib, "AXUIElementGetPid")
	purego.RegisterLibFunc(&axIsProcessTrusted, lib, "AXIsProcessTrusted")
	purego.RegisterLibFunc(&axIsProcessTrustedWithOptions, lib, "AXIsProcessTrustedWithOptions")
	purego.RegisterLibFunc(&axValueGetValue, lib, "AXValueGetValue")

	libCF, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_GLOBAL)
	if err != nil {
		fmt.Printf("Error loading CoreFoundation: %v\n", err)
	} else {
		purego.RegisterLibFunc(&cfStringCreateWithCString, libCF, "CFStringCreateWithCString")
	}
}

// ... (App struct etc) ...

func MkString(s string) uintptr {
	b := make([]byte, len(s)+1)
	copy(b, s)
	b[len(s)] = 0

	if cfStringCreateWithCString != nil {
		return cfStringCreateWithCString(0, unsafe.Pointer(&b[0]), kCFStringEncodingUTF8)
	}

	// Fallback (unsafe/leaky without autorelease pool)
	cls := objc.GetClass("NSString")
	return objc.Send[uintptr](objc.ID(cls), objc.Sel("stringWithUTF8String:"), unsafe.Pointer(&b[0]))
}

// (Inside Element)

func (e *Element) getFrame() corefoundation.CGRect {
	var rect corefoundation.CGRect

	// Get Position
	var ptrPos uintptr
	keyPos := MkString("AXPosition")
	if axCopyAttributeValue(e.ax, keyPos, &ptrPos) == 0 {
		var pt corefoundation.CGPoint
		if axValueGetValue(ptrPos, kAXValueTypeCGPoint, unsafe.Pointer(&pt)) {
			rect.Origin = pt
		}
	}

	// Get Size
	var ptrSize uintptr
	keySize := MkString("AXSize")
	if axCopyAttributeValue(e.ax, keySize, &ptrSize) == 0 {
		var sz corefoundation.CGSize
		if axValueGetValue(ptrSize, kAXValueTypeCGSize, unsafe.Pointer(&sz)) {
			rect.Size = sz
		}
	}

	return rect
}

func (e *Element) Attributes() Attributes {
	return Attributes{
		Label:      e.getStringAttr("AXDescription"),
		Identifier: e.getStringAttr("AXIdentifier"),
		Title:      e.getStringAttr("AXTitle"),
		Value:      e.getStringAttr("AXValue"),
		Frame:      e.getFrame(),
	}
}

func (e *Element) Screenshot() ([]byte, error) {
	frame := e.getFrame()
	if frame.Size.Width == 0 || frame.Size.Height == 0 {
		return nil, fmt.Errorf("element has empty frame (likely missing Accessibility permissions for xcmcp.app or parent process)")
	}

	// screencapture -R x,y,w,h -t png <file>
	// -R captures a rect
	// We'll write to a temp file then read it

	f, err := os.CreateTemp("", "xc-screenshot-*.png")
	if err != nil {
		return nil, err
	}
	f.Close()
	defer os.Remove(f.Name())

	rectArg := fmt.Sprintf("%f,%f,%f,%f", frame.Origin.X, frame.Origin.Y, frame.Size.Width, frame.Size.Height)
	cmd := exec.Command("screencapture", "-R", rectArg, "-t", "png", f.Name())
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("screencapture failed: %v, output: %s", err, out)
	}

	return os.ReadFile(f.Name())
}

// App ...
type App struct {
	element *Element
	pid     int32
}

var trustOnce sync.Once

func uiExecName() string {
	exe, err := os.Executable()
	if err != nil {
		return "xcmcp"
	}
	name := filepath.Base(exe)
	name = strings.TrimSuffix(name, ".app")
	return name
}

func uiIsTrustedFresh() bool {
	if axIsProcessTrustedWithOptions == nil {
		return axIsProcessTrusted != nil && axIsProcessTrusted()
	}
	key := MkString("AXTrustedCheckOptionPrompt")
	val := objc.Send[uintptr](objc.ID(objc.GetClass("NSNumber")), objc.Sel("numberWithBool:"), false)
	dict := objc.Send[uintptr](objc.ID(objc.GetClass("NSDictionary")), objc.Sel("dictionaryWithObject:forKey:"), val, key)
	return axIsProcessTrustedWithOptions(dict)
}

// uiBundleID reads CFBundleIdentifier from the running app bundle's Info.plist,
// falling back to "dev.tmc.<execname>".
func uiBundleID() string {
	exe, err := os.Executable()
	if err == nil {
		plist := filepath.Join(filepath.Dir(filepath.Dir(exe)), "Info.plist")
		out, err := exec.Command("defaults", "read", plist, "CFBundleIdentifier").Output()
		if err == nil {
			if id := strings.TrimSpace(string(out)); id != "" {
				return id
			}
		}
	}
	return "dev.tmc." + uiExecName()
}

func uiRequestPermission() {
	// Reset stale TCC entry before re-prompting.
	exec.Command("tccutil", "reset", "Accessibility", uiBundleID()).Run()

	if axIsProcessTrustedWithOptions == nil {
		return
	}
	key := MkString("AXTrustedCheckOptionPrompt")
	val := objc.Send[uintptr](objc.ID(objc.GetClass("NSNumber")), objc.Sel("numberWithBool:"), true)
	dict := objc.Send[uintptr](objc.ID(objc.GetClass("NSDictionary")), objc.Sel("dictionaryWithObject:forKey:"), val, key)
	axIsProcessTrustedWithOptions(dict)
}



func uiBindButtonAction(btn appkit.NSButton, fn func()) {
	btn.SetActionHandler(fn)
}

func uiMakeButton(title string, frame corefoundation.CGRect, fn func()) appkit.NSButton {
	btn := appkit.NewButtonWithFrame(frame)
	btn.SetTitle(title)
	btn.SetBezelStyle(appkit.NSBezelStyleAccessoryBar)
	uiBindButtonAction(btn, fn)
	return btn
}

// IsTrusted reports whether the process has Accessibility permission.
func IsTrusted() bool {
	return axIsProcessTrusted != nil && axIsProcessTrusted()
}

func CheckTrust() {
	trustOnce.Do(func() {
		if axIsProcessTrusted != nil && axIsProcessTrusted() {
			return
		}
		if axIsProcessTrustedWithOptions == nil {
			fmt.Fprintln(os.Stderr, "Warning: Process is NOT trusted as an accessibility client. Grant Accessibility permissions in System Settings.")
			return
		}
		showWaitingForPermissionWindow()
	})
}

// IsScreenRecordingTrusted checks if screen recording permission is granted.
func IsScreenRecordingTrusted() bool {
	scClass := screencapturekit.GetSCShareableContentClass()
	done := make(chan bool)
	handler := func(content *screencapturekit.SCShareableContent, err error) {
		done <- content != nil && err == nil
	}
	scClass.GetShareableContentExcludingDesktopWindowsOnScreenWindowsOnlyCompletionHandler(true, true, handler)
	select {
	case result := <-done:
		return result
	case <-time.After(2 * time.Second):
		return false
	}
}

func requestScreenCapture() {
	// Reset stale TCC entry before re-prompting.
	exec.Command("tccutil", "reset", "ScreenCapture", uiBundleID()).Run()
	coregraphics.CGRequestScreenCaptureAccess()
}

var screenCaptureOnce sync.Once

// CheckScreenCapture checks if the process has Screen Recording permission and,
// if not, shows a floating HIG-style window. It polls until permission is granted,
// then briefly shows a green checkmark before closing.
func CheckScreenCapture() {
	screenCaptureOnce.Do(func() {
		if IsScreenRecordingTrusted() {
			return
		}
		showScreenRecordingPermissionWindow()
	})
}

func showScreenRecordingPermissionWindow() {
	dispatch.MainQueue().Async(func() {
		app := appkit.GetNSApplicationClass().SharedApplication()
		app.SetActivationPolicy(appkit.NSApplicationActivationPolicyAccessory)

		const (
			w       = 420.0
			h       = 160.0
			pad     = 20.0
			iconSz  = 48.0
			iconPad = 14.0
			btnH    = 26.0
			btnW    = 155.0
			btnPadB = 14.0
		)

		name := uiExecName()
		fontClass := appkit.GetNSFontClass()

		win := appkit.NewWindowWithContentRectStyleMaskBackingDefer(
			corefoundation.CGRect{Size: corefoundation.CGSize{Width: w, Height: h}},
			appkit.NSWindowStyleMaskTitled,
			appkit.NSBackingStoreBuffered,
			false,
		)
		win.SetTitle("")
		win.SetLevel(appkit.FloatingWindowLevel)

		content := appkit.NSViewFromID(win.ContentView().GetID())

		// Icon — video SF Symbol, top-left, sized like a macOS alert icon.
		iconImg := appkit.NewImageWithSystemSymbolNameAccessibilityDescription(
			"rectangle.inset.filled.and.person.filled", "Screen recording permission",
		)
		iconCfg := appkit.NewImageSymbolConfigurationWithPointSizeWeight(iconSz*0.5, appkit.NSFontWeights.Light)
		iconImg = appkit.NSImageFromID(iconImg.ImageWithSymbolConfiguration(iconCfg).GetID())
		iconView := appkit.NewImageViewWithFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: pad, Y: h - pad - iconSz},
			Size:   corefoundation.CGSize{Width: iconSz, Height: iconSz},
		})
		iconView.SetImage(iconImg)
		content.AddSubview(iconView)

		// Text area to the right of the icon.
		textX := pad + iconSz + iconPad
		textW := w - textX - pad

		// Bold title.
		titleLabel := appkit.NewTextFieldLabelWithString(
			`"` + name + `.app" needs Screen Recording permission`,
		)
		titleLabel.SetFont(fontClass.BoldSystemFontOfSize(13))
		titleLabel.SetFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: textX, Y: h - pad - 20},
			Size:   corefoundation.CGSize{Width: textW, Height: 20},
		})
		content.AddSubview(titleLabel)

		// Informative text.
		bodyLabel := appkit.NewTextFieldLabelWithString(
			"Grant access in System Settings > Privacy & Security.",
		)
		bodyLabel.SetFont(fontClass.SystemFontOfSize(11))
		bodyLabel.SetFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: textX, Y: h - pad - 20 - 18},
			Size:   corefoundation.CGSize{Width: textW, Height: 16},
		})
		content.AddSubview(bodyLabel)

		// Spinner — small, inline, indicates waiting.
		spinSz := 14.0
		spinY := h - pad - 20 - 18 - 24
		spinner := appkit.NewProgressIndicatorWithFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: textX, Y: spinY},
			Size:   corefoundation.CGSize{Width: spinSz, Height: spinSz},
		})
		spinner.SetStyle(appkit.NSProgressIndicatorStyleSpinning)
		spinner.SetIndeterminate(true)
		spinner.StartAnimation(nil)
		content.AddSubview(spinner)

		waitLabel := appkit.NewTextFieldLabelWithString("Waiting for permission…")
		waitLabel.SetFont(fontClass.SystemFontOfSize(11))
		waitLabel.SetFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: textX + spinSz + 6, Y: spinY - 1},
			Size:   corefoundation.CGSize{Width: textW - spinSz - 6, Height: 16},
		})
		content.AddSubview(waitLabel)

		// Single default button, right-aligned at the bottom.
		btnY := btnPadB
		primaryX := w - pad - btnW

		requestBtn := uiMakeButton("Request Permission…", corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: primaryX, Y: btnY},
			Size:   corefoundation.CGSize{Width: btnW, Height: btnH},
		}, func() {
			win.SetLevel(appkit.NormalWindowLevel)
			requestScreenCapture()
		})
		requestBtn.SetBezelStyle(appkit.NSBezelStylePush)
		requestBtn.SetKeyEquivalent("\r")
		content.AddSubview(requestBtn)

		win.Center()
		win.MakeKeyAndOrderFront(nil)
		app.Activate()

		// Poll for trust on the main thread.
		var pollTimer *time.Timer
		var poll func()
		poll = func() {
			if !IsScreenRecordingTrusted() {
				pollTimer = time.AfterFunc(500*time.Millisecond, func() {
					dispatch.MainQueue().Async(poll)
				})
				return
			}
			// Permission granted — transition to success state.
			spinner.StopAnimation(nil)
			spinner.SetIsHidden(true)
			waitLabel.SetIsHidden(true)
			requestBtn.SetIsHidden(true)
			bodyLabel.SetIsHidden(true)

			// Resize window for compact success state.
			const successH = 100.0
			frame := win.Frame()
			dy := frame.Size.Height - successH
			frame.Origin.Y += dy
			frame.Size.Height = successH
			win.SetFrameDisplayAnimate(frame, true, true)

			const checkSz = 32.0
			checkBase := appkit.NewImageWithSystemSymbolNameAccessibilityDescription(
				"checkmark.circle.fill", "Permission granted",
			)
			checkCfg := appkit.NewImageSymbolConfigurationWithPointSizeWeight(checkSz*0.6, appkit.NSFontWeights.Medium)
			checkColorCfg := appkit.NewImageSymbolConfigurationWithHierarchicalColor(
				appkit.GetNSColorClass().SystemGreen(),
			)
			checkFinalCfg := checkCfg.ConfigurationByApplyingConfiguration(checkColorCfg)
			checkImg := appkit.NSImageFromID(checkBase.ImageWithSymbolConfiguration(checkFinalCfg).GetID())
			iconView.SetImage(checkImg)

			contentH := successH - 28.0
			midY := contentH / 2.0
			titleLabel.SetStringValue("Screen Recording permission granted.")
			titleLabel.SetFrame(corefoundation.CGRect{
				Origin: corefoundation.CGPoint{X: pad + checkSz + iconPad, Y: midY - 10},
				Size:   corefoundation.CGSize{Width: textW, Height: 20},
			})
			iconView.SetFrame(corefoundation.CGRect{
				Origin: corefoundation.CGPoint{X: pad, Y: midY - checkSz/2},
				Size:   corefoundation.CGSize{Width: checkSz, Height: checkSz},
			})

			time.AfterFunc(1200*time.Millisecond, func() {
				dispatch.MainQueue().Async(func() {
					win.Close()
				})
			})
		}

		pollTimer = time.AfterFunc(500*time.Millisecond, func() {
			dispatch.MainQueue().Async(poll)
		})
		_ = pollTimer
	})
}

func showWaitingForPermissionWindow() {
	dispatch.MainQueue().Async(func() {
		app := appkit.GetNSApplicationClass().SharedApplication()
		app.SetActivationPolicy(appkit.NSApplicationActivationPolicyAccessory)

		const (
			w       = 420.0
			h       = 160.0
			pad     = 20.0
			iconSz  = 48.0
			iconPad = 14.0
			btnH    = 26.0
			btnW    = 155.0
			btnPadB = 14.0
		)

		name := uiExecName()
		fontClass := appkit.GetNSFontClass()

		win := appkit.NewWindowWithContentRectStyleMaskBackingDefer(
			corefoundation.CGRect{Size: corefoundation.CGSize{Width: w, Height: h}},
			appkit.NSWindowStyleMaskTitled,
			appkit.NSBackingStoreBuffered,
			false,
		)
		win.SetTitle("")
		win.SetLevel(appkit.FloatingWindowLevel)

		content := appkit.NSViewFromID(win.ContentView().GetID())

		// Icon — lock.shield SF Symbol, top-left, sized like a macOS alert icon.
		iconImg := appkit.NewImageWithSystemSymbolNameAccessibilityDescription(
			"lock.shield", "Accessibility permission",
		)
		iconCfg := appkit.NewImageSymbolConfigurationWithPointSizeWeight(iconSz*0.5, appkit.NSFontWeights.Light)
		iconImg = appkit.NSImageFromID(iconImg.ImageWithSymbolConfiguration(iconCfg).GetID())
		iconView := appkit.NewImageViewWithFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: pad, Y: h - pad - iconSz},
			Size:   corefoundation.CGSize{Width: iconSz, Height: iconSz},
		})
		iconView.SetImage(iconImg)
		content.AddSubview(iconView)

		// Text area to the right of the icon.
		textX := pad + iconSz + iconPad
		textW := w - textX - pad

		// Bold title.
		titleLabel := appkit.NewTextFieldLabelWithString(
			`"` + name + `.app" needs Accessibility permission`,
		)
		titleLabel.SetFont(fontClass.BoldSystemFontOfSize(13))
		titleLabel.SetFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: textX, Y: h - pad - 20},
			Size:   corefoundation.CGSize{Width: textW, Height: 20},
		})
		content.AddSubview(titleLabel)

		// Informative text.
		bodyLabel := appkit.NewTextFieldLabelWithString(
			"Grant access in System Settings > Privacy & Security.",
		)
		bodyLabel.SetFont(fontClass.SystemFontOfSize(11))
		bodyLabel.SetFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: textX, Y: h - pad - 20 - 18},
			Size:   corefoundation.CGSize{Width: textW, Height: 16},
		})
		content.AddSubview(bodyLabel)

		// Spinner — small, inline, indicates waiting.
		spinSz := 14.0
		spinY := h - pad - 20 - 18 - 24
		spinner := appkit.NewProgressIndicatorWithFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: textX, Y: spinY},
			Size:   corefoundation.CGSize{Width: spinSz, Height: spinSz},
		})
		spinner.SetStyle(appkit.NSProgressIndicatorStyleSpinning)
		spinner.SetIndeterminate(true)
		spinner.StartAnimation(nil)
		content.AddSubview(spinner)

		waitLabel := appkit.NewTextFieldLabelWithString("Waiting for permission…")
		waitLabel.SetFont(fontClass.SystemFontOfSize(11))
		waitLabel.SetFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: textX + spinSz + 6, Y: spinY - 1},
			Size:   corefoundation.CGSize{Width: textW - spinSz - 6, Height: 16},
		})
		content.AddSubview(waitLabel)

		// Single default button, right-aligned at the bottom.
		btnY := btnPadB
		primaryX := w - pad - btnW

		requestBtn := uiMakeButton("Request Permission…", corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: primaryX, Y: btnY},
			Size:   corefoundation.CGSize{Width: btnW, Height: btnH},
		}, func() {
			// Drop below the system TCC dialog before triggering it.
			win.SetLevel(appkit.NormalWindowLevel)
			uiRequestPermission()
		})
		requestBtn.SetBezelStyle(appkit.NSBezelStylePush)
		requestBtn.SetKeyEquivalent("\r")
		content.AddSubview(requestBtn)

		win.Center()
		win.MakeKeyAndOrderFront(nil)
		app.Activate()

		// Poll for trust on the main thread.
		var pollTimer *time.Timer
		var poll func()
		poll = func() {
			if !uiIsTrustedFresh() {
				pollTimer = time.AfterFunc(500*time.Millisecond, func() {
					dispatch.MainQueue().Async(poll)
				})
				return
			}
			// Permission granted — transition to success state.
			spinner.StopAnimation(nil)
			spinner.SetIsHidden(true)
			waitLabel.SetIsHidden(true)
			requestBtn.SetIsHidden(true)
			bodyLabel.SetIsHidden(true)

			// Resize window for compact success state.
			const successH = 100.0
			frame := win.Frame()
			dy := frame.Size.Height - successH
			frame.Origin.Y += dy
			frame.Size.Height = successH
			win.SetFrameDisplayAnimate(frame, true, true)

			const checkSz = 32.0
			checkBase := appkit.NewImageWithSystemSymbolNameAccessibilityDescription(
				"checkmark.circle.fill", "Permission granted",
			)
			checkCfg := appkit.NewImageSymbolConfigurationWithPointSizeWeight(checkSz*0.6, appkit.NSFontWeights.Medium)
			checkColorCfg := appkit.NewImageSymbolConfigurationWithHierarchicalColor(
				appkit.GetNSColorClass().SystemGreen(),
			)
			checkFinalCfg := checkCfg.ConfigurationByApplyingConfiguration(checkColorCfg)
			checkImg := appkit.NSImageFromID(checkBase.ImageWithSymbolConfiguration(checkFinalCfg).GetID())
			iconView.SetImage(checkImg)

			// Center icon and label in the smaller window. Content height
			// is successH minus the title bar (~28pt).
			contentH := successH - 28.0
			midY := contentH / 2.0
			titleLabel.SetStringValue("Accessibility permission granted.")
			titleLabel.SetFrame(corefoundation.CGRect{
				Origin: corefoundation.CGPoint{X: pad + checkSz + iconPad, Y: midY - 10},
				Size:   corefoundation.CGSize{Width: textW, Height: 20},
			})
			iconView.SetFrame(corefoundation.CGRect{
				Origin: corefoundation.CGPoint{X: pad, Y: midY - checkSz/2},
				Size:   corefoundation.CGSize{Width: checkSz, Height: checkSz},
			})

			time.AfterFunc(1200*time.Millisecond, func() {
				dispatch.MainQueue().Async(func() {
					win.Close()
				})
			})
		}

		pollTimer = time.AfterFunc(500*time.Millisecond, func() {
			dispatch.MainQueue().Async(poll)
		})
		_ = pollTimer
	})
}

func NewApp(bundleID string) *App {
	CheckTrust()

	if bundleID == "" {
		bundleID = "com.apple.iphonesimulator"
	}

	wsClass := objc.GetClass("NSWorkspace")
	workspace := objc.Send[objc.ID](objc.ID(wsClass), objc.Sel("sharedWorkspace"))
	appsPtr := objc.Send[objc.ID](workspace, objc.Sel("runningApplications"))

	count := objc.Send[uint](appsPtr, objc.Sel("count"))

	var targetPid int32

	for i := uint(0); i < uint(count); i++ {
		app := objc.Send[objc.ID](appsPtr, objc.Sel("objectAtIndex:"), int(i))
		bidPtr := objc.Send[uintptr](app, objc.Sel("bundleIdentifier"))
		if bidPtr == 0 {
			continue
		}

		utf8 := objc.Send[uintptr](objc.ID(bidPtr), objc.Sel("UTF8String"))
		bid := BytePtrToString(utf8)

		if bid == bundleID {
			targetPid = objc.Send[int32](app, objc.Sel("processIdentifier"))
			break
		}
	}

	if targetPid == 0 {
		return &App{}
	}

	axRef := axCreateApplication(targetPid)
	return &App{
		pid:     targetPid,
		element: &Element{ax: axRef},
	}
}

func ApplicationWithBundleID(bid string) *App {
	return NewApp(bid)
}

func Application() *App {
	return NewApp("com.apple.iphonesimulator")
}

func (a *App) Exists() bool {
	return a.pid != 0
}

func (a *App) Terminate() {
	if a.pid != 0 {
		// Skip
	}
}

func (a *App) Activate() {
	if a.pid != 0 {
		// Skip
	}
}

func (a *App) Launch() {
	// Not implemented
}

func (a *App) Element() *Element {
	return a.element
}

func (a *App) Tree() string {
	if a.element == nil {
		return ""
	}
	return a.element.Tree()
}

// Element
type Element struct {
	ax uintptr // AXUIElementRef
}

func ElementByID(id string) *Element {
	return Application().Element().ElementByID(id)
}

func (e *Element) ElementByID(id string) *Element {
	res := e.Query(QueryParams{Identifier: id})
	if len(res) > 0 {
		return res[0]
	}
	return nil
}

func (e *Element) Tap() {
	e.PerformAction("AXPress")
}

func (e *Element) PerformAction(action string) {
	if axPerformAction == nil {
		return
	}
	key := MkString(action)
	axPerformAction(e.ax, key)
}

func (e *Element) Exists() bool {
	return e.ax != 0
}

func (e *Element) Tree() string {
	var sb strings.Builder
	e.dump(&sb, 0)
	return sb.String()
}

func (e *Element) dump(sb *strings.Builder, depth int) {
	indent := strings.Repeat("  ", depth)
	role := e.Role()
	title := e.Title()
	sb.WriteString(fmt.Sprintf("%s%s \"%s\"\n", indent, role, title))

	children := e.Children()
	for _, child := range children {
		child.dump(sb, depth+1)
	}
}

func (e *Element) Role() string {
	return e.getStringAttr("AXRole")
}

func (e *Element) Title() string {
	return e.getStringAttr("AXTitle")
}

func (e *Element) Label() string {
	return e.getStringAttr("AXDescription")
}

func (e *Element) Identifier() string {
	return e.getStringAttr("AXIdentifier")
}

func (e *Element) Frame() corefoundation.CGRect {
	return e.getFrame()
}

func (e *Element) Children() []*Element {
	var ptr uintptr
	key := MkString("AXChildren")
	if axCopyAttributeValue != nil && axCopyAttributeValue(e.ax, key, &ptr) == 0 {
		// ptr is CFArrayRef (NSArray)
		count := objc.Send[uint](objc.ID(ptr), objc.Sel("count"))
		res := make([]*Element, count)
		for i := uint(0); i < uint(count); i++ {
			itemPtr := objc.Send[uintptr](objc.ID(ptr), objc.Sel("objectAtIndex:"), int(i))
			res[i] = &Element{ax: itemPtr}
		}
		return res
	}
	return nil
}

// Helper filter functions
func (e *Element) FindChildren(role string) []*Element {
	var res []*Element
	children := e.Children()
	for _, child := range children {
		if child.Role() == role {
			res = append(res, child)
		}
	}
	return res
}

func (e *Element) Windows() []*Element {
	return e.FindChildren("AXWindow")
}

func (e *Element) Buttons() []*Element {
	// Buttons can be nested deeper.
	// This is a naive implementation recursively searching.
	var res []*Element
	var visit func(*Element)
	visit = func(el *Element) {
		if el.Role() == "AXButton" {
			res = append(res, el)
		}
		for _, child := range el.Children() {
			visit(child)
		}
	}
	visit(e)
	return res
}

type QueryParams struct {
	Role       string
	Title      string // Contains match
	Identifier string // Exact match
	Label      string // Contains match
}

func (e *Element) Query(p QueryParams) []*Element {
	var res []*Element
	var visit func(*Element)
	visit = func(el *Element) {
		match := true

		if p.Role != "" && el.Role() != p.Role {
			match = false
		}

		if p.Identifier != "" && el.Attributes().Identifier != p.Identifier {
			match = false
		}

		if p.Title != "" && !strings.Contains(el.Title(), p.Title) {
			match = false
		}

		if p.Label != "" && !strings.Contains(el.Attributes().Label, p.Label) {
			match = false
		}

		if match {
			res = append(res, el)
		}

		for _, child := range el.Children() {
			visit(child)
		}
	}
	visit(e)
	return res
}

func BytePtrToString(ptr uintptr) string {
	if ptr == 0 {
		return ""
	}
	var s strings.Builder
	for {
		b := *(*byte)(unsafe.Pointer(ptr))
		if b == 0 {
			break
		}
		s.WriteByte(b)
		ptr++
	}
	return s.String()
}

func (e *Element) getStringAttr(attr string) string {
	var ptr uintptr
	key := MkString(attr)
	if axCopyAttributeValue != nil {
		err := axCopyAttributeValue(e.ax, key, &ptr)
		if err == 0 {
			// ptr is NSString
			utf8 := objc.Send[uintptr](objc.ID(ptr), objc.Sel("UTF8String"))
			return BytePtrToString(utf8)
		}
	}
	return ""
}

// Attributes struct for Inspect
type Attributes struct {
	Label      string
	Identifier string
	Title      string
	Value      string
	Frame      corefoundation.CGRect
	Enabled    bool
	Selected   bool
	HasFocus   bool
}
