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
	"github.com/tmc/appledocs/generated/appkit"
	"github.com/tmc/appledocs/generated/corefoundation"
	"github.com/tmc/appledocs/generated/objc"
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

func uiOpenAccessibilityPrefs() {
	exec.Command("open", "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility").Start()
}

func uiResetTCC() {
	exe, _ := os.Executable()
	plist := filepath.Join(filepath.Dir(filepath.Dir(exe)), "Info.plist")
	out, err := exec.Command("defaults", "read", plist, "CFBundleIdentifier").Output()
	bundleID := ""
	if err == nil {
		bundleID = strings.TrimSpace(string(out))
	}
	if bundleID == "" {
		bundleID = "dev.tmc." + uiExecName()
	}
	exec.Command("tccutil", "reset", "Accessibility", bundleID).Run()
	// Re-query with prompt=false to re-register the entry in TCC without
	// triggering the system universalAccessAuthWarn popup. This causes macOS
	// to write a "denied" row back into the TCC database so it appears in
	// System Settings > Privacy & Security > Accessibility for the user to
	// toggle on.
	if axIsProcessTrustedWithOptions != nil {
		key := MkString("AXTrustedCheckOptionPrompt")
		val := objc.Send[uintptr](objc.ID(objc.GetClass("NSNumber")), objc.Sel("numberWithBool:"), false)
		dict := objc.Send[uintptr](objc.ID(objc.GetClass("NSDictionary")), objc.Sel("dictionaryWithObject:forKey:"), val, key)
		axIsProcessTrustedWithOptions(dict)
	}
	uiOpenAccessibilityPrefs()
}

func uiBindButtonAction(btn appkit.NSButton, fn func()) {
	item := appkit.NewNSMenuItem()
	appkit.BindAction(item, func(_ objc.ID) { fn() })
	btn.SetTarget(item.Target())
	btn.SetAction(item.Action())
}

func uiMakeButton(title string, frame corefoundation.CGRect, fn func()) appkit.NSButton {
	btn := appkit.NewButtonWithFrame(frame)
	btn.SetTitle(title)
	btn.SetBezelStyle(appkit.NSBezelStyleAccessoryBar)
	uiBindButtonAction(btn, fn)
	return btn
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

func showWaitingForPermissionWindow() {
	appkit.DispatchMainSafe(func() {
		app := appkit.GetNSApplicationClass().SharedApplication()
		app.SetActivationPolicy(appkit.NSApplicationActivationPolicyAccessory)

		const (
			w         = 400.0
			h         = 148.0
			padding   = 16.0
			spinSz    = 24.0
			btnH      = 22.0
			btnGap    = 6.0
			titleH    = 34.0
			subtitleH = 38.0
		)

		name := uiExecName()

		win := appkit.NewWindowWithContentRectStyleMaskBackingDefer(
			corefoundation.CGRect{Size: corefoundation.CGSize{Width: w, Height: h}},
			appkit.NSWindowStyleMaskTitled,
			appkit.NSBackingStoreBuffered,
			false,
		)
		win.SetTitle(name + " — Accessibility Permission Required")
		win.SetLevel(appkit.NSFloatingWindowLevel)

		content := appkit.NSViewFrom(win.ContentView().GetID())

		// Spinner — left side, aligned with text area.
		spinner := appkit.NewProgressIndicatorWithFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: padding, Y: h - padding - spinSz - 4},
			Size:   corefoundation.CGSize{Width: spinSz, Height: spinSz},
		})
		spinner.SetStyle(appkit.NSProgressIndicatorStyleSpinning)
		spinner.SetIndeterminate(true)
		spinner.StartAnimation(nil)
		content.AddSubview(spinner)

		labelX := padding + spinSz + padding
		labelW := w - labelX - padding

		// Bold title line.
		titleLabel := appkit.NewTextFieldLabelWithString(
			`"` + name + `.app" would like to control this computer using accessibility features.`,
		)
		titleLabel.SetFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: labelX, Y: h - padding - titleH},
			Size:   corefoundation.CGSize{Width: labelW, Height: titleH},
		})
		content.AddSubview(titleLabel)

		// Secondary description.
		subtitleLabel := appkit.NewTextFieldLabelWithString(
			"Grant access in Privacy & Security settings, located in System Settings.",
		)
		subtitleLabel.SetFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: labelX, Y: h - padding - titleH - subtitleH - 2},
			Size:   corefoundation.CGSize{Width: labelW, Height: subtitleH},
		})
		content.AddSubview(subtitleLabel)

		// Two buttons side by side in the lower area.
		halfW := (labelW - btnGap) / 2
		openBtn := uiMakeButton("Open System Settings…", corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: labelX, Y: padding},
			Size:   corefoundation.CGSize{Width: halfW, Height: btnH},
		}, uiOpenAccessibilityPrefs)
		content.AddSubview(openBtn)

		resetBtn := uiMakeButton("Reset TCC…", corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: labelX + halfW + btnGap, Y: padding},
			Size:   corefoundation.CGSize{Width: halfW, Height: btnH},
		}, uiResetTCC)
		content.AddSubview(resetBtn)

		win.Center()
		win.MakeKeyAndOrderFront(nil)
		app.ActivateIgnoringOtherApps(true)

		// Poll on the main thread via time.AfterFunc + DispatchMainSafe so all
		// ObjC calls (including uiIsTrustedFresh and UI mutations) stay on the
		// main thread.
		var pollTimer *time.Timer
		var poll func()
		poll = func() {
			if !uiIsTrustedFresh() {
				pollTimer = time.AfterFunc(500*time.Millisecond, func() {
					appkit.DispatchMainSafe(poll)
				})
				return
			}
			// Permission granted — transition to success state.
			spinner.StopAnimation(nil)
			spinner.SetIsHidden(true)
			openBtn.SetIsHidden(true)
			resetBtn.SetIsHidden(true)
			subtitleLabel.SetIsHidden(true)

			const checkSz = 36.0
			baseImg := appkit.NewImageWithSystemSymbolNameAccessibilityDescription(
				"checkmark.circle.fill", "Permission granted",
			)
			sizeCfg := appkit.NewImageSymbolConfigurationWithPointSizeWeight(checkSz, appkit.NSFontWeightMedium)
			colorCfg := appkit.NewImageSymbolConfigurationWithHierarchicalColor(
				appkit.GetNSColorClass().SystemGreen(),
			)
			cfg := sizeCfg.ConfigurationByApplyingConfiguration(colorCfg)
			checkImg := appkit.NSImageFrom(baseImg.ImageWithSymbolConfiguration(cfg).GetID())
			checkView := appkit.NewImageViewWithFrame(corefoundation.CGRect{
				Origin: corefoundation.CGPoint{X: padding - 4, Y: (h - checkSz) / 2},
				Size:   corefoundation.CGSize{Width: checkSz, Height: checkSz},
			})
			checkView.SetImage(checkImg)
			content.AddSubview(checkView)
			titleLabel.SetStringValue("Accessibility permission granted.")

			time.AfterFunc(1200*time.Millisecond, func() {
				appkit.DispatchMainSafe(func() {
					win.Close()
				})
			})
		}

		pollTimer = time.AfterFunc(500*time.Millisecond, func() {
			appkit.DispatchMainSafe(poll)
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

func (a *App) PID() int32 {
	return a.pid
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

func (a *App) SwipeLeft()  {}
func (a *App) SwipeRight() {}
func (a *App) SwipeUp()    {}
func (a *App) SwipeDown()  {}

// Element
type Element struct {
	ax uintptr // AXUIElementRef
}

func NewElement(ax uintptr) *Element {
	return &Element{ax: ax}
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

func (e *Element) DoubleTap()                                 {}
func (e *Element) TwoFingerTap()                              {}
func (e *Element) Press(d float64)                            {}
func (e *Element) Pinch(s, v float64)                         {}
func (e *Element) TypeText(t string)                          { e.SetValue(t) }
func (e *Element) SwipeLeft()                                 {}
func (e *Element) SwipeRight()                                {}
func (e *Element) SwipeUp()                                   {}
func (e *Element) SwipeDown()                                 {}
func (e *Element) WaitForExistence(t float64) bool            { return e.Exists() }
func (e *Element) AdjustToNormalizedSliderPosition(p float64) {}
func (e *Element) DragTo(o *Element, d float64)               {}

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

func (e *Element) SetValue(val string) {
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
		// Recursive check? Or just direct children?
		// Usually Windows are top level, buttons inside.
		// Let's do simple BFS/DFS or just direct children for now.
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

type Coordinate struct{}

func (e *Element) Coordinate(x, y float64) *Coordinate { return &Coordinate{} }
func (c *Coordinate) Tap()                             {}

func CoordinateAt(x, y float64) *Coordinate { return &Coordinate{} }
func FocusedElement() *Element              { return nil }
