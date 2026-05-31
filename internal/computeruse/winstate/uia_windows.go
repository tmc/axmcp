//go:build windows

package winstate

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"syscall"
	"unsafe"

	"github.com/tmc/axmcp/internal/computeruse"
	"golang.org/x/sys/windows"
)

const (
	clsctxInprocServer = 1

	vtBool = 11
	vtBSTR = 8

	uiaInvokePatternID         = 10000
	uiaValuePatternID          = 10002
	uiaRangeValuePatternID     = 10003
	uiaExpandCollapsePatternID = 10005
	uiaSelectionItemPatternID  = 10010
	uiaScrollItemPatternID     = 10017
	uiaTogglePatternID         = 10015

	uiaValueValuePropertyID      = 30045
	uiaValueIsReadOnlyPropertyID = 30046
)

var (
	clsidCUIAutomation = windows.GUID{Data1: 0xff48dba4, Data2: 0x60ef, Data3: 0x4201, Data4: [8]byte{0xaa, 0x87, 0x54, 0x10, 0x3e, 0xef, 0x59, 0x4e}}
	iidIUIAutomation   = windows.GUID{Data1: 0x30cbe57d, Data2: 0xd9d0, Data3: 0x452a, Data4: [8]byte{0xab, 0x13, 0x7a, 0xc5, 0xac, 0x48, 0x25, 0xee}}

	ole32                 = windows.NewLazySystemDLL("ole32.dll")
	oleaut32              = windows.NewLazySystemDLL("oleaut32.dll")
	procCoCreateInstance  = ole32.NewProc("CoCreateInstance")
	procSysAllocStringLen = oleaut32.NewProc("SysAllocStringLen")
	procSysFreeString     = oleaut32.NewProc("SysFreeString")
	procSysStringLen      = oleaut32.NewProc("SysStringLen")
	procVariantClear      = oleaut32.NewProc("VariantClear")
)

type uiaReadResult struct {
	root AutomationNode
	err  error
}

func readAutomationTree(ctx context.Context, win Window) (AutomationNode, error) {
	if err := ctx.Err(); err != nil {
		return AutomationNode{}, err
	}
	if win.Handle == 0 {
		return AutomationNode{}, fmt.Errorf("missing window handle")
	}

	done := make(chan struct{})
	result := make(chan uiaReadResult, 1)
	go readAutomationTreeOnThread(ctx, win, result, done)

	select {
	case res := <-result:
		if res.err != nil {
			close(done)
			return AutomationNode{}, res.err
		}
		return res.root, nil
	case <-ctx.Done():
		close(done)
		return AutomationNode{}, ctx.Err()
	}
}

func readAutomationTreeOnThread(ctx context.Context, win Window, result chan<- uiaReadResult, done chan struct{}) {
	runtime.LockOSThread()

	releaseCOM, err := initializeCOM()
	if err != nil {
		runtime.UnlockOSThread()
		result <- uiaReadResult{err: err}
		return
	}

	reader, err := newUIAReader()
	if err != nil {
		releaseCOM()
		runtime.UnlockOSThread()
		result <- uiaReadResult{err: err}
		return
	}

	root, err := reader.readWindow(ctx, win)
	if err != nil {
		reader.close()
		releaseCOM()
		runtime.UnlockOSThread()
		result <- uiaReadResult{err: err}
		return
	}

	released := make(chan struct{})
	var once sync.Once
	root.release = func() {
		once.Do(func() {
			close(done)
			<-released
		})
	}

	select {
	case result <- uiaReadResult{root: root}:
		<-done
	case <-ctx.Done():
	}
	reader.close()
	releaseCOM()
	runtime.UnlockOSThread()
	close(released)
}

func initializeCOM() (func(), error) {
	err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED)
	if err == nil || errors.Is(err, syscall.Errno(windows.S_FALSE)) {
		return windows.CoUninitialize, nil
	}
	if errors.Is(err, syscall.Errno(windows.RPC_E_CHANGED_MODE)) {
		return func() {}, nil
	}
	return nil, fmt.Errorf("%w: initialize COM: %v", errAutomationUnavailable, err)
}

type uiaReader struct {
	automation *iUIAutomation
	walker     *iUIAutomationTreeWalker
	retained   []uintptr
}

func newUIAReader() (*uiaReader, error) {
	automation, err := createUIAutomation()
	if err != nil {
		return nil, err
	}
	walker, err := automation.controlViewWalker()
	if err != nil {
		releaseInterface(uintptr(unsafe.Pointer(automation)))
		return nil, err
	}
	return &uiaReader{automation: automation, walker: walker}, nil
}

func (r *uiaReader) close() {
	for i := len(r.retained) - 1; i >= 0; i-- {
		releaseInterface(r.retained[i])
	}
	r.retained = nil
	releaseInterface(uintptr(unsafe.Pointer(r.walker)))
	releaseInterface(uintptr(unsafe.Pointer(r.automation)))
}

func (r *uiaReader) readWindow(ctx context.Context, win Window) (AutomationNode, error) {
	root, err := r.automation.elementFromHandle(win.Handle)
	if err != nil {
		return AutomationNode{}, err
	}
	r.retain(root)
	node, err := r.nodeFromElement(ctx, win, root, 0)
	if err != nil {
		return AutomationNode{}, err
	}
	return node, nil
}

func (r *uiaReader) nodeFromElement(ctx context.Context, win Window, el *iUIAutomationElement, depth int) (AutomationNode, error) {
	if err := ctx.Err(); err != nil {
		return AutomationNode{}, err
	}
	node := r.automationNode(win, el)
	if depth >= maxAutomationDepth || len(r.retained) >= maxAutomationNodes {
		return node, nil
	}
	child, err := r.walker.firstChild(el)
	if err != nil {
		return node, nil
	}
	for child != nil && len(r.retained) < maxAutomationNodes {
		r.retain(child)
		childNode, err := r.nodeFromElement(ctx, win, child, depth+1)
		if err != nil {
			return AutomationNode{}, err
		}
		node.Children = append(node.Children, childNode)
		next, err := r.walker.nextSibling(child)
		if err != nil {
			return node, nil
		}
		child = next
	}
	return node, nil
}

func (r *uiaReader) retain(el *iUIAutomationElement) {
	if el != nil {
		r.retained = append(r.retained, uintptr(unsafe.Pointer(el)))
	}
}

func (r *uiaReader) automationNode(win Window, el *iUIAutomationElement) AutomationNode {
	hwnd := el.currentNativeWindowHandle()
	if hwnd == 0 {
		hwnd = win.Handle
	}
	role := controlTypeRole(el.currentControlType())
	if role == "" {
		role = el.currentLocalizedControlType()
	}
	actions, settable := el.actions()
	return AutomationNode{
		Native: NativeElement{
			WindowHandle:     hwnd,
			AutomationHandle: uintptr(unsafe.Pointer(el)),
		},
		Role:             role,
		Title:            el.currentName(),
		Value:            el.currentStringProperty(uiaValueValuePropertyID),
		Description:      el.currentHelpText(),
		Identifier:       el.currentAutomationID(),
		Rect:             rectFromWindows(el.currentBoundingRectangle()),
		Enabled:          el.currentIsEnabled(),
		Settable:         settable,
		SecondaryActions: actions,
	}
}

const (
	maxAutomationDepth = 12
	maxAutomationNodes = 512
)

type iUnknownVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
}

type iUnknown struct {
	lpVtbl *iUnknownVtbl
}

type iUIAutomation struct {
	lpVtbl *iUIAutomationVtbl
}

type iUIAutomationVtbl struct {
	QueryInterface                       uintptr
	AddRef                               uintptr
	Release                              uintptr
	CompareElements                      uintptr
	CompareRuntimeIds                    uintptr
	GetRootElement                       uintptr
	ElementFromHandle                    uintptr
	ElementFromPoint                     uintptr
	GetFocusedElement                    uintptr
	GetRootElementBuildCache             uintptr
	ElementFromHandleBuildCache          uintptr
	ElementFromPointBuildCache           uintptr
	GetFocusedElementBuildCache          uintptr
	CreateTreeWalker                     uintptr
	ControlViewWalker                    uintptr
	ContentViewWalker                    uintptr
	RawViewWalker                        uintptr
	RawViewCondition                     uintptr
	ControlViewCondition                 uintptr
	ContentViewCondition                 uintptr
	CreateCacheRequest                   uintptr
	CreateTrueCondition                  uintptr
	CreateFalseCondition                 uintptr
	CreatePropertyCondition              uintptr
	CreatePropertyConditionEx            uintptr
	CreateAndCondition                   uintptr
	CreateAndConditionFromArray          uintptr
	CreateAndConditionFromNativeArray    uintptr
	CreateOrCondition                    uintptr
	CreateOrConditionFromArray           uintptr
	CreateOrConditionFromNativeArray     uintptr
	CreateNotCondition                   uintptr
	AddAutomationEventHandler            uintptr
	RemoveAutomationEventHandler         uintptr
	AddPropertyChangedEventHandlerNative uintptr
	AddPropertyChangedEventHandler       uintptr
	RemovePropertyChangedEventHandler    uintptr
	AddStructureChangedEventHandler      uintptr
	RemoveStructureChangedEventHandler   uintptr
	AddFocusChangedEventHandler          uintptr
	RemoveFocusChangedEventHandler       uintptr
	RemoveAllEventHandlers               uintptr
	IntNativeArrayToSafeArray            uintptr
	IntSafeArrayToNativeArray            uintptr
	RectToVariant                        uintptr
	VariantToRect                        uintptr
	SafeArrayToRectNativeArray           uintptr
	CreateProxyFactoryEntry              uintptr
	ProxyFactoryMapping                  uintptr
	GetPropertyProgrammaticName          uintptr
	GetPatternProgrammaticName           uintptr
	PollForPotentialSupportedPatterns    uintptr
	PollForPotentialSupportedProperties  uintptr
	CheckNotSupported                    uintptr
	ReservedNotSupportedValue            uintptr
	ReservedMixedAttributeValue          uintptr
	ElementFromIAccessible               uintptr
	ElementFromIAccessibleBuildCache     uintptr
}

type iUIAutomationTreeWalker struct {
	lpVtbl *iUIAutomationTreeWalkerVtbl
}

type iUIAutomationTreeWalkerVtbl struct {
	QueryInterface                      uintptr
	AddRef                              uintptr
	Release                             uintptr
	GetParentElement                    uintptr
	GetFirstChildElement                uintptr
	GetLastChildElement                 uintptr
	GetNextSiblingElement               uintptr
	GetPreviousSiblingElement           uintptr
	NormalizeElement                    uintptr
	GetParentElementBuildCache          uintptr
	GetFirstChildElementBuildCache      uintptr
	GetLastChildElementBuildCache       uintptr
	GetNextSiblingElementBuildCache     uintptr
	GetPreviousSiblingElementBuildCache uintptr
	NormalizeElementBuildCache          uintptr
	Condition                           uintptr
}

type iUIAutomationElement struct {
	lpVtbl *iUIAutomationElementVtbl
}

type iUIAutomationElementVtbl struct {
	QueryInterface              uintptr
	AddRef                      uintptr
	Release                     uintptr
	SetFocus                    uintptr
	GetRuntimeID                uintptr
	FindFirst                   uintptr
	FindAll                     uintptr
	FindFirstBuildCache         uintptr
	FindAllBuildCache           uintptr
	BuildUpdatedCache           uintptr
	GetCurrentPropertyValue     uintptr
	GetCurrentPropertyValueEx   uintptr
	GetCachedPropertyValue      uintptr
	GetCachedPropertyValueEx    uintptr
	GetCurrentPatternAs         uintptr
	GetCachedPatternAs          uintptr
	GetCurrentPattern           uintptr
	GetCachedPattern            uintptr
	GetCachedParent             uintptr
	GetCachedChildren           uintptr
	CurrentProcessID            uintptr
	CurrentControlType          uintptr
	CurrentLocalizedControlType uintptr
	CurrentName                 uintptr
	CurrentAcceleratorKey       uintptr
	CurrentAccessKey            uintptr
	CurrentHasKeyboardFocus     uintptr
	CurrentIsKeyboardFocusable  uintptr
	CurrentIsEnabled            uintptr
	CurrentAutomationID         uintptr
	CurrentClassName            uintptr
	CurrentHelpText             uintptr
	CurrentCulture              uintptr
	CurrentIsControlElement     uintptr
	CurrentIsContentElement     uintptr
	CurrentIsPassword           uintptr
	CurrentNativeWindowHandle   uintptr
	CurrentItemType             uintptr
	CurrentIsOffscreen          uintptr
	CurrentOrientation          uintptr
	CurrentFrameworkID          uintptr
	CurrentIsRequiredForForm    uintptr
	CurrentItemStatus           uintptr
	CurrentBoundingRectangle    uintptr
	CurrentLabeledBy            uintptr
	CurrentAriaRole             uintptr
	CurrentAriaProperties       uintptr
	CurrentIsDataValidForForm   uintptr
	CurrentControllerFor        uintptr
	CurrentDescribedBy          uintptr
	CurrentFlowsTo              uintptr
	CurrentProviderDescription  uintptr
	CachedProcessID             uintptr
	CachedControlType           uintptr
	CachedLocalizedControlType  uintptr
	CachedName                  uintptr
	CachedAcceleratorKey        uintptr
	CachedAccessKey             uintptr
	CachedHasKeyboardFocus      uintptr
	CachedIsKeyboardFocusable   uintptr
	CachedIsEnabled             uintptr
	CachedAutomationID          uintptr
	CachedClassName             uintptr
	CachedHelpText              uintptr
	CachedCulture               uintptr
	CachedIsControlElement      uintptr
	CachedIsContentElement      uintptr
	CachedIsPassword            uintptr
	CachedNativeWindowHandle    uintptr
	CachedItemType              uintptr
	CachedIsOffscreen           uintptr
	CachedOrientation           uintptr
	CachedFrameworkID           uintptr
	CachedIsRequiredForForm     uintptr
	CachedItemStatus            uintptr
	CachedBoundingRectangle     uintptr
	CachedLabeledBy             uintptr
	CachedAriaRole              uintptr
	CachedAriaProperties        uintptr
	CachedIsDataValidForForm    uintptr
	CachedControllerFor         uintptr
	CachedDescribedBy           uintptr
	CachedFlowsTo               uintptr
	CachedProviderDescription   uintptr
	GetClickablePoint           uintptr
}

type iUIAutomationInvokePattern struct {
	lpVtbl *iUIAutomationInvokePatternVtbl
}

type iUIAutomationInvokePatternVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
	Invoke         uintptr
}

type iUIAutomationTogglePattern struct {
	lpVtbl *iUIAutomationTogglePatternVtbl
}

type iUIAutomationTogglePatternVtbl struct {
	QueryInterface     uintptr
	AddRef             uintptr
	Release            uintptr
	Toggle             uintptr
	CurrentToggleState uintptr
	CachedToggleState  uintptr
}

type iUIAutomationSelectionItemPattern struct {
	lpVtbl *iUIAutomationSelectionItemPatternVtbl
}

type iUIAutomationSelectionItemPatternVtbl struct {
	QueryInterface            uintptr
	AddRef                    uintptr
	Release                   uintptr
	Select                    uintptr
	AddToSelection            uintptr
	RemoveFromSelection       uintptr
	CurrentIsSelected         uintptr
	CurrentSelectionContainer uintptr
	CachedIsSelected          uintptr
	CachedSelectionContainer  uintptr
}

type iUIAutomationExpandCollapsePattern struct {
	lpVtbl *iUIAutomationExpandCollapsePatternVtbl
}

type iUIAutomationExpandCollapsePatternVtbl struct {
	QueryInterface             uintptr
	AddRef                     uintptr
	Release                    uintptr
	Expand                     uintptr
	Collapse                   uintptr
	CurrentExpandCollapseState uintptr
	CachedExpandCollapseState  uintptr
}

type iUIAutomationValuePattern struct {
	lpVtbl *iUIAutomationValuePatternVtbl
}

type iUIAutomationValuePatternVtbl struct {
	QueryInterface    uintptr
	AddRef            uintptr
	Release           uintptr
	SetValue          uintptr
	CurrentValue      uintptr
	CurrentIsReadOnly uintptr
	CachedValue       uintptr
	CachedIsReadOnly  uintptr
}

type oleVariant struct {
	VT        uint16
	Reserved1 uint16
	Reserved2 uint16
	Reserved3 uint16
	Value     [8]byte
}

func createUIAutomation() (*iUIAutomation, error) {
	var out *iUIAutomation
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidCUIAutomation)),
		0,
		clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidIUIAutomation)),
		uintptr(unsafe.Pointer(&out)),
	)
	if failedHRESULT(hr) {
		return nil, fmt.Errorf("%w: create UI Automation: %s", errAutomationUnavailable, hresultString(hr))
	}
	if out == nil {
		return nil, fmt.Errorf("%w: create UI Automation returned nil", errAutomationUnavailable)
	}
	return out, nil
}

func (a *iUIAutomation) elementFromHandle(hwnd uintptr) (*iUIAutomationElement, error) {
	var out *iUIAutomationElement
	hr, _, _ := syscall.SyscallN(
		a.lpVtbl.ElementFromHandle,
		uintptr(unsafe.Pointer(a)),
		hwnd,
		uintptr(unsafe.Pointer(&out)),
	)
	if failedHRESULT(hr) {
		return nil, fmt.Errorf("%w: element from HWND: %s", errAutomationUnavailable, hresultString(hr))
	}
	if out == nil {
		return nil, fmt.Errorf("%w: element from HWND returned nil", errAutomationUnavailable)
	}
	return out, nil
}

func (a *iUIAutomation) controlViewWalker() (*iUIAutomationTreeWalker, error) {
	var out *iUIAutomationTreeWalker
	hr, _, _ := syscall.SyscallN(
		a.lpVtbl.ControlViewWalker,
		uintptr(unsafe.Pointer(a)),
		uintptr(unsafe.Pointer(&out)),
	)
	if failedHRESULT(hr) {
		return nil, fmt.Errorf("%w: get control-view walker: %s", errAutomationUnavailable, hresultString(hr))
	}
	if out == nil {
		return nil, fmt.Errorf("%w: control-view walker returned nil", errAutomationUnavailable)
	}
	return out, nil
}

func (w *iUIAutomationTreeWalker) firstChild(el *iUIAutomationElement) (*iUIAutomationElement, error) {
	return w.element(w.lpVtbl.GetFirstChildElement, el)
}

func (w *iUIAutomationTreeWalker) nextSibling(el *iUIAutomationElement) (*iUIAutomationElement, error) {
	return w.element(w.lpVtbl.GetNextSiblingElement, el)
}

func (w *iUIAutomationTreeWalker) element(method uintptr, el *iUIAutomationElement) (*iUIAutomationElement, error) {
	var out *iUIAutomationElement
	hr, _, _ := syscall.SyscallN(
		method,
		uintptr(unsafe.Pointer(w)),
		uintptr(unsafe.Pointer(el)),
		uintptr(unsafe.Pointer(&out)),
	)
	if failedHRESULT(hr) {
		return nil, fmt.Errorf("walk UI Automation tree: %s", hresultString(hr))
	}
	return out, nil
}

func (el *iUIAutomationElement) currentControlType() int32 {
	var out int32
	el.callInt32(el.lpVtbl.CurrentControlType, &out)
	return out
}

func (el *iUIAutomationElement) currentLocalizedControlType() string {
	return el.callBSTR(el.lpVtbl.CurrentLocalizedControlType)
}

func (el *iUIAutomationElement) currentName() string {
	return el.callBSTR(el.lpVtbl.CurrentName)
}

func (el *iUIAutomationElement) currentAutomationID() string {
	return el.callBSTR(el.lpVtbl.CurrentAutomationID)
}

func (el *iUIAutomationElement) currentHelpText() string {
	return el.callBSTR(el.lpVtbl.CurrentHelpText)
}

func (el *iUIAutomationElement) currentIsEnabled() bool {
	var out int32
	if !el.callBool(el.lpVtbl.CurrentIsEnabled, &out) {
		return true
	}
	return out != 0
}

func (el *iUIAutomationElement) currentNativeWindowHandle() uintptr {
	var out uintptr
	el.callPtr(el.lpVtbl.CurrentNativeWindowHandle, &out)
	return out
}

func (el *iUIAutomationElement) currentBoundingRectangle() windows.Rect {
	var out windows.Rect
	el.callRect(el.lpVtbl.CurrentBoundingRectangle, &out)
	return out
}

func (el *iUIAutomationElement) currentStringProperty(id int32) string {
	var out oleVariant
	hr, _, _ := syscall.SyscallN(
		el.lpVtbl.GetCurrentPropertyValue,
		uintptr(unsafe.Pointer(el)),
		uintptr(id),
		uintptr(unsafe.Pointer(&out)),
	)
	if failedHRESULT(hr) {
		return ""
	}
	defer variantClear(&out)
	if out.VT != vtBSTR {
		return ""
	}
	return bstrString(out.ptr())
}

func (el *iUIAutomationElement) currentBoolProperty(id int32) (bool, bool) {
	var out oleVariant
	hr, _, _ := syscall.SyscallN(
		el.lpVtbl.GetCurrentPropertyValue,
		uintptr(unsafe.Pointer(el)),
		uintptr(id),
		uintptr(unsafe.Pointer(&out)),
	)
	if failedHRESULT(hr) {
		return false, false
	}
	defer variantClear(&out)
	if out.VT != vtBool {
		return false, false
	}
	return int16(binaryUint16(out.Value[:2])) != 0, true
}

func (el *iUIAutomationElement) actions() ([]string, bool) {
	type pattern struct {
		id     int32
		action string
	}
	patterns := []pattern{
		{id: uiaInvokePatternID, action: "invoke"},
		{id: uiaTogglePatternID, action: "toggle"},
		{id: uiaSelectionItemPatternID, action: "select"},
		{id: uiaExpandCollapsePatternID, action: "expand_collapse"},
		{id: uiaScrollItemPatternID, action: "scroll_into_view"},
	}
	var actions []string
	for _, p := range patterns {
		if el.patternSupported(p.id) {
			actions = append(actions, p.action)
		}
	}
	settable := el.patternSupported(uiaValuePatternID) || el.patternSupported(uiaRangeValuePatternID)
	if readOnly, ok := el.currentBoolProperty(uiaValueIsReadOnlyPropertyID); ok && readOnly {
		settable = false
	}
	if settable {
		actions = append(actions, "set_value")
	}
	sort.Strings(actions)
	return actions, settable
}

func (el *iUIAutomationElement) patternSupported(patternID int32) bool {
	out, err := el.currentPattern(patternID, "")
	if err != nil {
		return false
	}
	releaseInterface(out)
	return true
}

func performAutomationAction(ctx context.Context, action automationAction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if action.Element == 0 {
		return computeruse.PlatformUnsupported("perform UI Automation action")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	releaseCOM, err := initializeCOM()
	if err != nil {
		return err
	}
	defer releaseCOM()

	el := (*iUIAutomationElement)(unsafe.Pointer(action.Element))
	switch action.Kind {
	case automationInvoke:
		return el.invoke()
	case automationToggle:
		return el.toggle()
	case automationSelect:
		return el.selectItem()
	case automationExpand:
		return el.expand()
	case automationCollapse:
		return el.collapse()
	case automationExpandCollapse:
		return el.toggleExpandCollapse()
	case automationSetValue:
		return el.setValue(action.Value)
	default:
		return fmt.Errorf("unknown UI Automation action %d", action.Kind)
	}
}

func (el *iUIAutomationElement) currentPattern(patternID int32, name string) (uintptr, error) {
	var out uintptr
	hr, _, _ := syscall.SyscallN(
		el.lpVtbl.GetCurrentPattern,
		uintptr(unsafe.Pointer(el)),
		uintptr(patternID),
		uintptr(unsafe.Pointer(&out)),
	)
	if failedHRESULT(hr) || out == 0 {
		feature := "UI Automation pattern"
		if name != "" {
			feature = "UI Automation " + name + " pattern"
		}
		if failedHRESULT(hr) {
			return 0, fmt.Errorf("%s: %s: %w", feature, hresultString(hr), computeruse.ErrPlatformUnsupported)
		}
		return 0, computeruse.PlatformUnsupported(feature)
	}
	return out, nil
}

func (el *iUIAutomationElement) invoke() error {
	ptr, err := el.currentPattern(uiaInvokePatternID, "invoke")
	if err != nil {
		return err
	}
	defer releaseInterface(ptr)
	p := (*iUIAutomationInvokePattern)(unsafe.Pointer(ptr))
	hr, _, _ := syscall.SyscallN(p.lpVtbl.Invoke, ptr)
	if failedHRESULT(hr) {
		return fmt.Errorf("invoke UI Automation element: %s", hresultString(hr))
	}
	return nil
}

func (el *iUIAutomationElement) toggle() error {
	ptr, err := el.currentPattern(uiaTogglePatternID, "toggle")
	if err != nil {
		return err
	}
	defer releaseInterface(ptr)
	p := (*iUIAutomationTogglePattern)(unsafe.Pointer(ptr))
	hr, _, _ := syscall.SyscallN(p.lpVtbl.Toggle, ptr)
	if failedHRESULT(hr) {
		return fmt.Errorf("toggle UI Automation element: %s", hresultString(hr))
	}
	return nil
}

func (el *iUIAutomationElement) selectItem() error {
	ptr, err := el.currentPattern(uiaSelectionItemPatternID, "selection item")
	if err != nil {
		return err
	}
	defer releaseInterface(ptr)
	p := (*iUIAutomationSelectionItemPattern)(unsafe.Pointer(ptr))
	hr, _, _ := syscall.SyscallN(p.lpVtbl.Select, ptr)
	if failedHRESULT(hr) {
		return fmt.Errorf("select UI Automation element: %s", hresultString(hr))
	}
	return nil
}

func (el *iUIAutomationElement) expand() error {
	ptr, err := el.currentPattern(uiaExpandCollapsePatternID, "expand/collapse")
	if err != nil {
		return err
	}
	defer releaseInterface(ptr)
	p := (*iUIAutomationExpandCollapsePattern)(unsafe.Pointer(ptr))
	return p.expand()
}

func (el *iUIAutomationElement) collapse() error {
	ptr, err := el.currentPattern(uiaExpandCollapsePatternID, "expand/collapse")
	if err != nil {
		return err
	}
	defer releaseInterface(ptr)
	p := (*iUIAutomationExpandCollapsePattern)(unsafe.Pointer(ptr))
	return p.collapse()
}

func (el *iUIAutomationElement) toggleExpandCollapse() error {
	ptr, err := el.currentPattern(uiaExpandCollapsePatternID, "expand/collapse")
	if err != nil {
		return err
	}
	defer releaseInterface(ptr)
	p := (*iUIAutomationExpandCollapsePattern)(unsafe.Pointer(ptr))
	state, ok := p.currentState()
	if ok && state != 1 {
		return p.collapse()
	}
	return p.expand()
}

func (p *iUIAutomationExpandCollapsePattern) expand() error {
	hr, _, _ := syscall.SyscallN(p.lpVtbl.Expand, uintptr(unsafe.Pointer(p)))
	if failedHRESULT(hr) {
		return fmt.Errorf("expand UI Automation element: %s", hresultString(hr))
	}
	return nil
}

func (p *iUIAutomationExpandCollapsePattern) collapse() error {
	hr, _, _ := syscall.SyscallN(p.lpVtbl.Collapse, uintptr(unsafe.Pointer(p)))
	if failedHRESULT(hr) {
		return fmt.Errorf("collapse UI Automation element: %s", hresultString(hr))
	}
	return nil
}

func (p *iUIAutomationExpandCollapsePattern) currentState() (int32, bool) {
	var out int32
	hr, _, _ := syscall.SyscallN(p.lpVtbl.CurrentExpandCollapseState, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&out)))
	if failedHRESULT(hr) {
		return 0, false
	}
	return out, true
}

func (el *iUIAutomationElement) setValue(value string) error {
	ptr, err := el.currentPattern(uiaValuePatternID, "value")
	if err != nil {
		return err
	}
	defer releaseInterface(ptr)
	bstr, err := allocBSTR(value)
	if err != nil {
		return err
	}
	defer procSysFreeString.Call(bstr)
	p := (*iUIAutomationValuePattern)(unsafe.Pointer(ptr))
	hr, _, _ := syscall.SyscallN(p.lpVtbl.SetValue, ptr, bstr)
	if failedHRESULT(hr) {
		return fmt.Errorf("set UI Automation value: %s", hresultString(hr))
	}
	return nil
}

func allocBSTR(s string) (uintptr, error) {
	u16, err := windows.UTF16FromString(s)
	if err != nil {
		return 0, fmt.Errorf("encode BSTR: %w", err)
	}
	ptr, _, _ := procSysAllocStringLen.Call(uintptr(unsafe.Pointer(&u16[0])), uintptr(len(u16)-1))
	if ptr == 0 && s != "" {
		return 0, fmt.Errorf("allocate BSTR")
	}
	return ptr, nil
}

func (el *iUIAutomationElement) callBSTR(method uintptr) string {
	var out uintptr
	hr, _, _ := syscall.SyscallN(method, uintptr(unsafe.Pointer(el)), uintptr(unsafe.Pointer(&out)))
	if failedHRESULT(hr) || out == 0 {
		return ""
	}
	defer procSysFreeString.Call(out)
	return bstrString(out)
}

func (el *iUIAutomationElement) callBool(method uintptr, out *int32) bool {
	hr, _, _ := syscall.SyscallN(method, uintptr(unsafe.Pointer(el)), uintptr(unsafe.Pointer(out)))
	return !failedHRESULT(hr)
}

func (el *iUIAutomationElement) callInt32(method uintptr, out *int32) bool {
	hr, _, _ := syscall.SyscallN(method, uintptr(unsafe.Pointer(el)), uintptr(unsafe.Pointer(out)))
	return !failedHRESULT(hr)
}

func (el *iUIAutomationElement) callPtr(method uintptr, out *uintptr) bool {
	hr, _, _ := syscall.SyscallN(method, uintptr(unsafe.Pointer(el)), uintptr(unsafe.Pointer(out)))
	return !failedHRESULT(hr)
}

func (el *iUIAutomationElement) callRect(method uintptr, out *windows.Rect) bool {
	hr, _, _ := syscall.SyscallN(method, uintptr(unsafe.Pointer(el)), uintptr(unsafe.Pointer(out)))
	return !failedHRESULT(hr)
}

func (v *oleVariant) ptr() uintptr {
	return uintptr(binaryUint64(v.Value[:]))
}

func binaryUint16(b []byte) uint16 {
	return uint16(b[0]) | uint16(b[1])<<8
}

func binaryUint64(b []byte) uint64 {
	var out uint64
	for i := range 8 {
		out |= uint64(b[i]) << (8 * i)
	}
	return out
}

func variantClear(v *oleVariant) {
	if v != nil {
		procVariantClear.Call(uintptr(unsafe.Pointer(v)))
	}
}

func bstrString(bstr uintptr) string {
	if bstr == 0 {
		return ""
	}
	n, _, _ := procSysStringLen.Call(bstr)
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(bstr)), int(n)))
}

func releaseInterface(ptr uintptr) {
	if ptr == 0 {
		return
	}
	obj := (*iUnknown)(unsafe.Pointer(ptr))
	syscall.SyscallN(obj.lpVtbl.Release, ptr)
}

func failedHRESULT(hr uintptr) bool {
	return int32(uint32(hr)) < 0
}

func hresultString(hr uintptr) string {
	return fmt.Sprintf("HRESULT 0x%08x", uint32(hr))
}

func rectFromWindows(rect windows.Rect) Rect {
	width := int(rect.Right - rect.Left)
	height := int(rect.Bottom - rect.Top)
	if width <= 0 || height <= 0 {
		return Rect{}
	}
	return Rect{
		X:      int(rect.Left),
		Y:      int(rect.Top),
		Width:  width,
		Height: height,
	}
}

func controlTypeRole(id int32) string {
	switch id {
	case 50000:
		return "Button"
	case 50001:
		return "Calendar"
	case 50002:
		return "CheckBox"
	case 50003:
		return "ComboBox"
	case 50004:
		return "Edit"
	case 50005:
		return "Hyperlink"
	case 50006:
		return "Image"
	case 50007:
		return "ListItem"
	case 50008:
		return "List"
	case 50009:
		return "Menu"
	case 50010:
		return "MenuBar"
	case 50011:
		return "MenuItem"
	case 50012:
		return "ProgressBar"
	case 50013:
		return "RadioButton"
	case 50014:
		return "ScrollBar"
	case 50015:
		return "Slider"
	case 50016:
		return "Spinner"
	case 50017:
		return "StatusBar"
	case 50018:
		return "Tab"
	case 50019:
		return "TabItem"
	case 50020:
		return "Text"
	case 50021:
		return "ToolBar"
	case 50022:
		return "ToolTip"
	case 50023:
		return "Tree"
	case 50024:
		return "TreeItem"
	case 50025:
		return "Custom"
	case 50026:
		return "Group"
	case 50027:
		return "Thumb"
	case 50028:
		return "DataGrid"
	case 50029:
		return "DataItem"
	case 50030:
		return "Document"
	case 50031:
		return "SplitButton"
	case 50032:
		return "Window"
	case 50033:
		return "Pane"
	case 50034:
		return "Header"
	case 50035:
		return "HeaderItem"
	case 50036:
		return "Table"
	case 50037:
		return "TitleBar"
	case 50038:
		return "Separator"
	case 50039:
		return "SemanticZoom"
	case 50040:
		return "AppBar"
	default:
		if id > 0 {
			return fmt.Sprintf("ControlType%d", id)
		}
		return ""
	}
}
