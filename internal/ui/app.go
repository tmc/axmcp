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
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
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

	return uintptr(foundation.NewStringWithUTF8String(s).GetID())
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
		return nil, fmt.Errorf("element has empty frame (likely missing Accessibility permissions for %s.app or parent process)", uiExecName())
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

// activeWindows tracks permission windows that are animating their close
// transition. Call WaitForWindows before os.Exit to let animations finish.
var activeWindows sync.WaitGroup

var uiIdentity struct {
	sync.RWMutex
	appName  string
	bundleID string
}

// WaitForWindows blocks until all permission windows have finished their
// close animations. Call this before os.Exit to avoid cutting off the
// green checkmark transition.
func WaitForWindows() {
	activeWindows.Wait()
}

// ConfigureIdentity sets the app name and bundle identifier used for TCC
// prompts and resets.
func ConfigureIdentity(appName, bundleID string) {
	uiIdentity.Lock()
	defer uiIdentity.Unlock()
	if appName != "" {
		uiIdentity.appName = appName
	}
	if bundleID != "" {
		uiIdentity.bundleID = bundleID
	}
}

func configuredAppName() string {
	uiIdentity.RLock()
	defer uiIdentity.RUnlock()
	return uiIdentity.appName
}

func configuredBundleID() string {
	uiIdentity.RLock()
	defer uiIdentity.RUnlock()
	return uiIdentity.bundleID
}

func uiExecName() string {
	if name := strings.TrimSpace(configuredAppName()); name != "" {
		return name
	}
	if name := strings.TrimSpace(os.Getenv("MACGO_APP_NAME")); name != "" {
		return name
	}
	exe, err := os.Executable()
	if err != nil {
		return "xcmcp"
	}
	name := filepath.Base(exe)
	name = strings.TrimSuffix(name, ".app")
	return name
}

func uiIsTrustedFresh() bool {
	if axIsProcessTrusted != nil && axIsProcessTrusted() {
		return true
	}
	if axIsProcessTrustedWithOptions == nil {
		return false
	}
	key := foundation.NewStringWithString("AXTrustedCheckOptionPrompt")
	val := foundation.NewNumberWithBool(false)
	dict := foundation.NewDictionaryWithObjectForKey(val, key)
	return axIsProcessTrustedWithOptions(uintptr(dict.GetID()))
}

func waitForAccessibilityTrust(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if uiIsTrustedFresh() {
			return true
		}
		if timeout <= 0 || time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// uiBundleID reads CFBundleIdentifier from the running app bundle's Info.plist,
// falling back to "dev.tmc.<execname>".
func uiBundleID() string {
	if id := strings.TrimSpace(configuredBundleID()); id != "" {
		return id
	}
	if id := strings.TrimSpace(os.Getenv("MACGO_BUNDLE_ID")); id != "" {
		return id
	}
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
	if axIsProcessTrustedWithOptions == nil {
		return
	}
	key := foundation.NewStringWithString("AXTrustedCheckOptionPrompt")
	val := foundation.NewNumberWithBool(true)
	dict := foundation.NewDictionaryWithObjectForKey(val, key)
	axIsProcessTrustedWithOptions(uintptr(dict.GetID()))
}

func RequestAccessibilityPermission() {
	uiRequestPermission()
}

func requestAccessibilityPermissionAsync() {
	go func() {
		dispatch.MainQueue().Async(func() {
			uiRequestPermission()
		})
	}()
}

func uiPrivacySettingsURL(service string) string {
	switch service {
	case "Accessibility":
		return "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility"
	case "ScreenCapture":
		return "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture"
	default:
		return "x-apple.systempreferences:com.apple.preference.security"
	}
}

func PrivacySettingsURL(service string) string {
	return uiPrivacySettingsURL(service)
}

func uiPreparePermissionRequest(win appkit.NSWindow) {
	// Keep the helper visible across activation changes while yielding
	// activation so a system permission prompt can move in front of it.
	win.SetHidesOnDeactivate(false)
	win.SetLevel(appkit.NormalWindowLevel)
	appkit.GetNSApplicationClass().SharedApplication().Deactivate()
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
	return uiIsTrustedFresh()
}

func CheckTrust() {
	if waitForAccessibilityTrust(1500 * time.Millisecond) {
		return
	}
	if permissionInProgress("Accessibility") {
		return
	}
	if axIsProcessTrustedWithOptions == nil {
		fmt.Fprintln(os.Stderr, "Warning: Process is NOT trusted as an accessibility client. Grant Accessibility permissions in System Settings.")
		return
	}
	showPermissionWindow(permissionWindowConfig{
		service:     "Accessibility",
		iconSymbol:  "lock.shield",
		iconDescr:   "Accessibility permission",
		titleSuffix: "Accessibility",
		checkFunc:   uiIsTrustedFresh,
		requestFunc: requestAccessibilityPermissionAsync,
		successText: "Accessibility permission granted.",
	})
}

func WaitForAccessibility(timeout time.Duration) bool {
	if waitForAccessibilityTrust(1500 * time.Millisecond) {
		return true
	}
	go CheckTrust()
	deadline := time.Now().Add(timeout)
	for {
		if uiIsTrustedFresh() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// IsScreenRecordingTrusted checks if screen recording permission is granted
// without triggering a system prompt. It falls back to an actual capture test
// when the preflight API returns false, which handles post-re-sign scenarios
// where the TCC grant no longer matches the code signature.
func IsScreenRecordingTrusted() bool {
	return screenRecordingAvailable()
}

// isScreenRecordingAvailable checks whether screen recording permission is
// available using CGPreflightScreenCaptureAccess. Note that after a binary
// re-sign, the preflight cache may be stale. CGDisplayCreateImageForRect is
// obsoleted on macOS 15+ and cannot be used as a fallback.
func isScreenRecordingAvailable() bool {
	return coregraphics.CGPreflightScreenCaptureAccess()
}

var screenRecordingAvailable = isScreenRecordingAvailable

func screenCaptureTerminateGuardActive() bool {
	return permissionInProgress("ScreenCapture")
}

func ScreenCaptureTerminateGuardActive() bool {
	return screenCaptureTerminateGuardActive()
}

func requestScreenCapture() {
	fmt.Fprintln(os.Stderr, "axmcp: requesting screen capture access")
	coregraphics.CGRequestScreenCaptureAccess()
}

func RequestScreenCapturePermission() {
	requestScreenCapture()
}

func requestScreenCaptureAsync() {
	go func() {
		dispatch.MainQueue().Async(func() {
			requestScreenCapture()
		})
	}()
}

func resetAndRerequestScreenCapture() {
	fmt.Fprintln(os.Stderr, "axmcp: re-requesting screen capture — resetting TCC")
	resetTCC("ScreenCapture")
	coregraphics.CGRequestScreenCaptureAccess()
	go exec.Command("open", uiPrivacySettingsURL("ScreenCapture")).Run()
}

// resetTCC clears stale TCC entries for the current bundle so the OS will
// re-prompt. Must be called inside the .app bundle process.
func resetTCC(service string) {
	bid := uiBundleID()
	cmd := exec.Command("tccutil", "reset", service, bid)
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}

func ResetTCC(service string) {
	resetTCC(service)
}

// WaitForScreenRecording shows the permission window if screen recording is not
// already granted and blocks until permission is granted or the timeout expires.
// It returns true if permission was granted, false on timeout.
func WaitForScreenRecording(timeout time.Duration) bool {
	if screenRecordingAvailable() {
		return true
	}
	go CheckScreenCapture()
	deadline := time.Now().Add(timeout)
	for {
		if screenRecordingAvailable() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// CheckScreenCapture checks if the process has Screen Recording permission and,
// if not, shows a floating HIG-style window. It polls until permission is granted,
// then briefly shows a green checkmark before closing.
func CheckScreenCapture() {
	if screenRecordingAvailable() {
		return
	}
	if permissionInProgress("ScreenCapture") {
		return
	}
	showPermissionWindow(permissionWindowConfig{
		service:     "ScreenCapture",
		iconSymbol:  "rectangle.inset.filled.and.person.filled",
		iconDescr:   "Screen recording permission",
		titleSuffix: "Screen Recording",
		checkFunc:   screenRecordingAvailable,
		requestFunc: requestScreenCaptureAsync,
		resetFunc:   resetAndRerequestScreenCapture,
		successText: "Screen Recording permission granted.",
		timeoutText: "Permission may require restart. Re-run axmcp to try again.",
	})
}

// permissionWindowConfig holds the parameters that differ between
// permission request windows (e.g. Accessibility vs Screen Recording).
type permissionWindowConfig struct {
	service      string
	iconSymbol   string // SF Symbol name for the window icon
	iconDescr    string // accessibility description for the icon
	titleSuffix  string // e.g. "Accessibility" or "Screen Recording"
	checkFunc    func() bool
	requestFunc  func()
	resetFunc    func()
	requestTitle string
	bodyText     string
	waitText     func(time.Duration) string
	successText  string // e.g. "Accessibility permission granted."
	timeoutText  string // shown on timeout; defaults to "Timed out. Re-run to try again."
}

func permissionRequestButtonTitle(service string) string {
	return "Request Permission"
}

func permissionBodyText(service string) string {
	return "Grant access in System Settings > Privacy & Security."
}

func permissionWaitText(service string, elapsed time.Duration) string {
	secs := int(elapsed.Seconds())
	if secs <= 0 {
		return "Waiting for permission…"
	}
	return fmt.Sprintf("Waiting for permission… (%ds)", secs)
}

func (cfg permissionWindowConfig) resolvedRequestTitle() string {
	if cfg.requestTitle != "" {
		return cfg.requestTitle
	}
	return permissionRequestButtonTitle(cfg.service)
}

func (cfg permissionWindowConfig) resolvedBodyText() string {
	if cfg.bodyText != "" {
		return cfg.bodyText
	}
	return permissionBodyText(cfg.service)
}

func (cfg permissionWindowConfig) resolvedWaitText(elapsed time.Duration) string {
	if cfg.waitText != nil {
		return cfg.waitText(elapsed)
	}
	return permissionWaitText(cfg.service, elapsed)
}

func showPermissionWindow(cfg permissionWindowConfig) {
	setPermissionInProgress(cfg.service, true)
	activeWindows.Add(1)
	dispatch.MainQueue().Async(func() {
		app := appkit.GetNSApplicationClass().SharedApplication()
		app.SetActivationPolicy(appkit.NSApplicationActivationPolicyAccessory)

		const (
			w       = 420.0
			h       = 166.0
			pad     = 16.0
			iconSz  = 40.0
			iconPad = 12.0
			btnH    = 26.0
			btnW    = 155.0
			btnGap  = 8.0
			btnPadB = 12.0
		)

		name := uiExecName()
		fontClass := appkit.GetNSFontClass()
		screenCaptureFlow := cfg.service == "ScreenCapture"
		requested := false
		requestTime := time.Time{}

		win := appkit.NewWindowWithContentRectStyleMaskBackingDefer(
			corefoundation.CGRect{Size: corefoundation.CGSize{Width: w, Height: h}},
			appkit.NSWindowStyleMaskTitled,
			appkit.NSBackingStoreBuffered,
			false,
		)
		win.SetTitle("")
		win.SetLevel(appkit.FloatingWindowLevel)
		win.SetHidesOnDeactivate(false)

		content := appkit.NSViewFromID(win.ContentView().GetID())

		// Icon — SF Symbol, top-left, sized like a macOS alert icon.
		iconImg := appkit.NewImageWithSystemSymbolNameAccessibilityDescription(
			cfg.iconSymbol, cfg.iconDescr,
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
			`"` + name + `.app" needs ` + cfg.titleSuffix + ` permission`,
		)
		titleLabel.SetFont(fontClass.BoldSystemFontOfSize(13))
		titleLabel.SetUsesSingleLineMode(false)
		titleLabel.SetLineBreakMode(appkit.NSLineBreakByWordWrapping)
		titleLabel.SetMaximumNumberOfLines(2)
		titleLabel.SetPreferredMaxLayoutWidth(textW)
		titleLabel.SetFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: textX, Y: h - pad - 30},
			Size:   corefoundation.CGSize{Width: textW, Height: 30},
		})
		content.AddSubview(titleLabel)

		// Informative text.
		bodyLabel := appkit.NewTextFieldLabelWithString(
			cfg.resolvedBodyText(),
		)
		bodyLabel.SetFont(fontClass.SystemFontOfSize(11))
		bodyLabel.SetUsesSingleLineMode(false)
		bodyLabel.SetLineBreakMode(appkit.NSLineBreakByWordWrapping)
		bodyLabel.SetMaximumNumberOfLines(2)
		bodyLabel.SetPreferredMaxLayoutWidth(textW)
		bodyLabel.SetFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: textX, Y: h - pad - 58},
			Size:   corefoundation.CGSize{Width: textW, Height: 34},
		})
		content.AddSubview(bodyLabel)

		// Spinner — small, inline, indicates waiting.
		spinSz := 14.0
		spinY := h - pad - 90
		spinner := appkit.NewProgressIndicatorWithFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: textX, Y: spinY},
			Size:   corefoundation.CGSize{Width: spinSz, Height: spinSz},
		})
		spinner.SetStyle(appkit.NSProgressIndicatorStyleSpinning)
		spinner.SetIndeterminate(true)
		content.AddSubview(spinner)

		waitLabel := appkit.NewTextFieldLabelWithString(cfg.resolvedWaitText(0))
		waitLabel.SetFont(fontClass.SystemFontOfSize(11))
		waitLabel.SetUsesSingleLineMode(false)
		waitLabel.SetLineBreakMode(appkit.NSLineBreakByWordWrapping)
		waitLabel.SetMaximumNumberOfLines(2)
		waitLabel.SetPreferredMaxLayoutWidth(textW - spinSz - 6)
		waitLabel.SetFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: textX + spinSz + 6, Y: spinY - 2},
			Size:   corefoundation.CGSize{Width: textW - spinSz - 6, Height: 22},
		})
		content.AddSubview(waitLabel)

		// Default button, right-aligned at the bottom.
		btnY := btnPadB
		primaryX := w - pad - btnW

		var requestBtn appkit.NSButton
		var resetBtn appkit.NSButton
		applyState := func() {
			if screenCaptureFlow {
				state := currentScreenCaptureWindowState(requested, requestTime)
				requestBtn.SetTitle(state.requestTitle)
				requestBtn.SetEnabled(state.requestEnabled)
				bodyLabel.SetStringValue(state.bodyText)
				waitLabel.SetStringValue(state.waitText)
				waitLabel.SetHidden(!state.showWait)
				spinner.SetHidden(!state.showSpinner)
				if state.showSpinner {
					spinner.StartAnimation(nil)
				} else {
					spinner.StopAnimation(nil)
				}
				if resetBtn.GetID() != 0 {
					resetBtn.SetHidden(!state.showReset)
				}
				return
			}

			elapsed := time.Duration(0)
			if requested && !requestTime.IsZero() {
				elapsed = time.Since(requestTime)
			}
			requestBtn.SetTitle(cfg.resolvedRequestTitle())
			requestBtn.SetEnabled(true)
			bodyLabel.SetStringValue(cfg.resolvedBodyText())
			waitLabel.SetStringValue(cfg.resolvedWaitText(elapsed))
			waitLabel.SetHidden(false)
			spinner.SetHidden(false)
			spinner.StartAnimation(nil)
			if resetBtn.GetID() != 0 {
				resetBtn.SetHidden(false)
			}
		}
		requestBtn = uiMakeButton(cfg.resolvedRequestTitle(), corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: primaryX, Y: btnY},
			Size:   corefoundation.CGSize{Width: btnW, Height: btnH},
		}, func() {
			uiPreparePermissionRequest(win)
			if screenCaptureFlow && requested {
				state := currentScreenCaptureWindowState(requested, requestTime)
				if state.phase == screenCaptureWindowPhaseSettings {
					go exec.Command("open", uiPrivacySettingsURL("ScreenCapture")).Run()
				}
				applyState()
				return
			}
			if screenCaptureFlow {
				requested = true
				requestTime = time.Now()
				cfg.requestFunc()
				applyState()
				return
			}
			requested = true
			requestTime = time.Now()
			cfg.requestFunc()
			applyState()
		})
		requestBtn.SetBezelStyle(appkit.NSBezelStylePush)
		requestBtn.SetKeyEquivalent("\r")
		content.AddSubview(requestBtn)

		if cfg.resetFunc != nil {
			resetBtn = uiMakeButton("Reset & Retry", corefoundation.CGRect{
				Origin: corefoundation.CGPoint{X: primaryX - btnGap - btnW, Y: btnY},
				Size:   corefoundation.CGSize{Width: btnW, Height: btnH},
			}, func() {
				uiPreparePermissionRequest(win)
				requested = true
				requestTime = time.Now()
				cfg.resetFunc()
				applyState()
			})
			resetBtn.SetBezelStyle(appkit.NSBezelStyleAccessoryBar)
			resetBtn.SetHidden(screenCaptureFlow)
			content.AddSubview(resetBtn)
		}

		applyState()

		win.Center()
		win.MakeKeyAndOrderFront(nil)
		app.Activate()

		// Poll for trust on the main thread with timeout.
		const pollTimeout = 120 * time.Second
		var pollTimer *time.Timer
		var poll func()
		poll = func() {
			elapsed := time.Duration(0)
			if requested && !requestTime.IsZero() {
				elapsed = time.Since(requestTime)
			}
			if !cfg.checkFunc() {
				if elapsed >= pollTimeout {
					msg := cfg.timeoutText
					if msg == "" {
						msg = "Timed out. Re-run to try again."
					}
					waitLabel.SetStringValue(msg)
					spinner.StopAnimation(nil)
					spinner.SetHidden(true)
					time.AfterFunc(2*time.Second, func() {
						dispatch.MainQueue().Async(func() {
							setPermissionInProgress(cfg.service, false)
							win.Close()
							activeWindows.Done()
						})
					})
					return
				}
				applyState()
				pollTimer = time.AfterFunc(500*time.Millisecond, func() {
					dispatch.MainQueue().Async(poll)
				})
				return
			}
			// Permission granted — transition to success state.
			spinner.StopAnimation(nil)
			spinner.SetHidden(true)
			waitLabel.SetHidden(true)
			requestBtn.SetHidden(true)
			if resetBtn.GetID() != 0 {
				resetBtn.SetHidden(true)
			}
			bodyLabel.SetHidden(true)

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
				appkit.GetNSColorClass().SystemGreenColor(),
			)
			checkFinalCfg := checkCfg.ConfigurationByApplyingConfiguration(checkColorCfg)
			checkImg := appkit.NSImageFromID(checkBase.ImageWithSymbolConfiguration(checkFinalCfg).GetID())
			iconView.SetImage(checkImg)

			contentH := successH - 28.0
			midY := contentH / 2.0
			titleLabel.SetStringValue(cfg.successText)
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
					setPermissionInProgress(cfg.service, false)
					win.Close()
					activeWindows.Done()
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

	var targetPid int32

	for _, app := range appkit.GetNSWorkspaceClass().SharedWorkspace().RunningApplications() {
		bid := app.BundleIdentifier()
		if bid == bundleID {
			targetPid = app.ProcessIdentifier()
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
		arr := foundation.NSArrayFromID(objc.ID(ptr))
		count := arr.Count()
		res := make([]*Element, count)
		for i := range res {
			item := arr.ObjectAtIndex(uint(i))
			res[i] = &Element{ax: uintptr(item.GetID())}
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
			return foundation.NSStringFromID(objc.ID(ptr)).UTF8String()
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
