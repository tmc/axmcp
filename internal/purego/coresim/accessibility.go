package coresim

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/tmc/xcmcp/internal/purego/objc"
)

var (
	dispatchOnce sync.Once
	dispatchLib  uintptr

	// dispatch queue (from symbol, not function)
	dispatchMainQueue uintptr

	// dispatch functions
	_dispatch_queue_create     func(label *byte, attr uintptr) uintptr
	_dispatch_get_global_queue func(identifier int64, flags uint64) uintptr
)

func initDispatch() {
	dispatchOnce.Do(func() {
		var err error
		dispatchLib, err = purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		// dispatch_get_main_queue() is a macro - get the actual _dispatch_main_q symbol
		dispatchMainQueue, err = purego.Dlsym(dispatchLib, "_dispatch_main_q")
		if err != nil {
			return
		}
		purego.RegisterLibFunc(&_dispatch_queue_create, dispatchLib, "dispatch_queue_create")
		purego.RegisterLibFunc(&_dispatch_get_global_queue, dispatchLib, "dispatch_get_global_queue")
	})
}

// GetMainQueue returns the main dispatch queue.
func GetMainQueue() uintptr {
	initDispatch()
	return dispatchMainQueue
}

// GetGlobalQueue returns a global dispatch queue.
func GetGlobalQueue() uintptr {
	initDispatch()
	// QOS_CLASS_DEFAULT = 0x15
	return _dispatch_get_global_queue(0x15, 0)
}

// CreateQueue creates a new serial dispatch queue.
func CreateQueue(label string) uintptr {
	initDispatch()
	labelBytes := append([]byte(label), 0)
	return _dispatch_queue_create(&labelBytes[0], 0)
}

// AccessibilityElement represents an iOS accessibility element.
type AccessibilityElement struct {
	AXLabel      string                  `json:"AXLabel,omitempty"`
	AXValue      string                  `json:"AXValue,omitempty"`
	AXIdentifier string                  `json:"AXIdentifier,omitempty"`
	AXFrame      map[string]float64      `json:"AXFrame,omitempty"`
	AXUniqueId   string                  `json:"AXUniqueId,omitempty"`
	AXTraits     uint64                  `json:"AXTraits,omitempty"`
	AXEnabled    bool                    `json:"AXEnabled,omitempty"`
	AXChildren   []*AccessibilityElement `json:"AXChildren,omitempty"`
	Role         string                  `json:"role,omitempty"`
	PID          int32                   `json:"pid,omitempty"`
	RawData      []byte                  `json:"-"`
	RawElement   map[string]interface{}  `json:"-"`
	Token        string                  `json:"-"` // Token for AXPDelegate session
}

// ... (Constants)

// GetFrontmostApplicationElement retrieves the frontmost application element.
func (d SimDevice) GetFrontmostApplicationElement(token string) (*AccessibilityElement, error) {
	if d.id == 0 {
		return nil, fmt.Errorf("device is nil")
	}

	if result := RegisterDeviceForToken(&d); token == "" {
		token = result
		defer UnregisterToken(token)
	} else if token != result {
		// If caller provided token, ensure it's registered?
		// Caller is responsible for registration if they provide a string.
	}

	// Ensure we are registered if token provided
	// Actually RegisterDeviceForToken returns a new token.
	// If caller passes token, we assume it's valid.

	// ...
	// Use token
	// ...
	return nil, fmt.Errorf("GetFrontmostApplicationElement deprecated, use GetAccessibilityElements")
}

// GetAccessibilityElements retrieves accessibility elements from a booted simulator.
func (d SimDevice) GetAccessibilityElements() ([]*AccessibilityElement, error) {
	if d.id == 0 {
		return nil, fmt.Errorf("device is nil")
	}

	if d.State() != SimDeviceStateBooted {
		return nil, fmt.Errorf("device is not booted")
	}

	// 1. Register Session Token
	// Create persistent device copy
	deviceRef := new(SimDevice)
	*deviceRef = d

	token := RegisterDeviceForToken(deviceRef)
	defer UnregisterToken(token)

	hitElement, err := d.GetAccessibilityElementAtPoint(200, 400, token)
	if err != nil {
		return nil, err
	}
	if hitElement == nil {
		return nil, nil
	}

	// Create Synthetic Root
	root := &AccessibilityElement{
		AXUniqueId: "0",
		Role:       "application",
		PID:        hitElement.PID,
		AXLabel:    "Frontmost App (Synthetic)",
		Token:      token, // Bind token to root
	}

	// Perform recursive fetch
	d.RecursivelyFetchChildren(root, 0)

	return []*AccessibilityElement{root}, nil
}

// GetAccessibilityElementsForPID retrieves the accessibility tree for a specific PID.
func (d SimDevice) GetAccessibilityElementsForPID(pid int) ([]*AccessibilityElement, error) {
	// Create persistent device copy
	deviceRef := new(SimDevice)
	*deviceRef = d

	token := RegisterDeviceForToken(deviceRef)
	defer UnregisterToken(token)

	// Direct Root Creation
	root := &AccessibilityElement{
		AXUniqueId: "0",
		Role:       "application",
		PID:        int32(pid),
		AXLabel:    fmt.Sprintf("App PID %d", pid),
		Token:      token,
	}

	// Perform recursive fetch
	d.RecursivelyFetchChildren(root, 0)

	return []*AccessibilityElement{root}, nil
}

// ...

// FetchChildren retrieves the children of an accessibility element.
func (d SimDevice) FetchChildren(element *AccessibilityElement) ([]*AccessibilityElement, error) {
	if d.id == 0 {
		return nil, fmt.Errorf("device is nil")
	}

	// Start Request
	reqClass := objc.GetClass("AXPTranslatorRequest")
	alloc := objc.Send[objc.ID](objc.ID(reqClass), objc.Sel("alloc"))
	request := objc.Send[objc.ID](alloc, objc.Sel("init"))

	transClass := objc.GetClass("AXPTranslationObject")
	transAlloc := objc.Send[objc.ID](objc.ID(transClass), objc.Sel("alloc"))
	trans := objc.Send[objc.ID](transAlloc, objc.Sel("init"))

	var objectID uint64
	fmt.Sscanf(element.AXUniqueId, "%d", &objectID)

	objc.Send[objc.ID](trans, objc.Sel("setObjectID:"), objectID)
	objc.Send[objc.ID](trans, objc.Sel("setPid:"), element.PID)

	if objectID == 0 && element.Role == "application" {
		objc.Send[objc.ID](trans, objc.Sel("setIsApplicationElement:"), true)
	}

	// Use Element Token
	token := element.Token
	if token == "" {
		// Fallback? Or error?
		// If we are deep in recursion, token should be present.
		// If not, we might fail delegate usage.
		token = "xcmcp-fallback-token"
	}
	objc.Send[objc.ID](trans, objc.Sel("setBridgeDelegateToken:"), objc.NSString(token))

	// Set raw data
	if len(element.RawData) > 0 {
		nsDataClass := objc.GetClass("NSData")
		dataObj := objc.Send[objc.ID](objc.ID(nsDataClass), objc.Sel("dataWithBytes:length:"),
			unsafe.Pointer(&element.RawData[0]), len(element.RawData))
		objc.Send[objc.ID](trans, objc.Sel("setRawElementData:"), dataObj)
	}

	objc.Send[objc.ID](request, objc.Sel("setTranslation:"), trans)
	objc.Send[objc.ID](request, objc.Sel("setRequestType:"), AXPRequestTypeAttribute)
	objc.Send[objc.ID](request, objc.Sel("setAttributeType:"), AXPAttributeTypeChildren)

	children, err := d.sendAccessibilityRequest(request)
	if err != nil {
		return nil, err
	}

	// Propagate Token to children
	for _, child := range children {
		child.Token = token
	}
	return children, nil
}

// maxTreeDepth limits how deep we recurse into the accessibility tree.
const maxTreeDepth = 15

// maxTreeElements limits the total number of elements fetched.
const maxTreeElements = 500

// RecursivelyFetchChildren traverses the accessibility tree.
func (d SimDevice) RecursivelyFetchChildren(element *AccessibilityElement, depth int) {
	d.recursivelyFetchChildren(element, depth, new(int))
}

func (d SimDevice) recursivelyFetchChildren(element *AccessibilityElement, depth int, count *int) {
	if depth > maxTreeDepth || *count >= maxTreeElements {
		return
	}

	children, err := d.FetchChildren(element)
	if err == nil && len(children) > 0 {
		element.AXChildren = children
		*count += len(children)
		for _, child := range children {
			if *count >= maxTreeElements {
				return
			}
			d.recursivelyFetchChildren(child, depth+1, count)
		}
	}
}

// UpgradeElement fetches full data
func (d SimDevice) UpgradeElement(element *AccessibilityElement) (*AccessibilityElement, error) {
	// ... (Setup Request)
	reqClass := objc.GetClass("AXPTranslatorRequest")
	request := objc.Send[objc.ID](objc.Send[objc.ID](objc.ID(reqClass), objc.Sel("alloc")), objc.Sel("init"))
	objc.Send[objc.ID](request, objc.Sel("setRequestType:"), AXPRequestTypeElement)

	transClass := objc.GetClass("AXPTranslationObject")
	trans := objc.Send[objc.ID](objc.Send[objc.ID](objc.ID(transClass), objc.Sel("alloc")), objc.Sel("init"))

	var objectID uint64
	fmt.Sscanf(element.AXUniqueId, "%d", &objectID)
	objc.Send[objc.ID](trans, objc.Sel("setObjectID:"), objectID)
	objc.Send[objc.ID](trans, objc.Sel("setPid:"), element.PID)

	token := element.Token
	if token == "" {
		token = "xcmcp-fallback-token"
	}
	objc.Send[objc.ID](trans, objc.Sel("setBridgeDelegateToken:"), objc.NSString(token))

	objc.Send[objc.ID](request, objc.Sel("setTranslation:"), trans)

	elements, err := d.sendAccessibilityRequest(request)
	if err != nil || len(elements) == 0 {
		return nil, err
	}
	// Propagate token
	elements[0].Token = token
	return elements[0], nil
}

// GetAccessibilityElementAtPoint with Token
func (d SimDevice) GetAccessibilityElementAtPoint(x, y float64, token string) (*AccessibilityElement, error) {
	if token == "" {
		// Register ephemeral
		devRef := new(SimDevice)
		*devRef = d
		token = RegisterDeviceForToken(devRef)
		defer UnregisterToken(token)
	}

	// ... Request setup ...
	reqClass := objc.GetClass("AXPTranslatorRequest")
	request := objc.Send[objc.ID](objc.Send[objc.ID](objc.ID(reqClass), objc.Sel("alloc")), objc.Sel("init"))

	objc.Send[objc.ID](request, objc.Sel("setRequestType:"), AXPRequestTypeHitTest)

	nsValueClass := objc.GetClass("NSValue")
	// ABI Hack: separate x, y arguments for CGPoint struct (passed in d0, d1)
	pointValue := objc.Send[objc.ID](objc.ID(nsValueClass), objc.Sel("valueWithPoint:"), x, y)

	dictClass := objc.GetClass("NSDictionary")
	params := objc.Send[objc.ID](objc.ID(dictClass), objc.Sel("dictionaryWithObject:forKey:"), pointValue, objc.NSString("point"))
	objc.Send[objc.ID](request, objc.Sel("setParameters:"), params)

	transClass := objc.GetClass("AXPTranslationObject")
	trans := objc.Send[objc.ID](objc.Send[objc.ID](objc.ID(transClass), objc.Sel("alloc")), objc.Sel("init"))
	objc.Send[objc.ID](trans, objc.Sel("setBridgeDelegateToken:"), objc.NSString(token))
	objc.Send[objc.ID](request, objc.Sel("setTranslation:"), trans)

	elements, err := d.sendAccessibilityRequest(request)
	if err != nil || len(elements) == 0 {
		return nil, err
	}
	elements[0].Token = token
	return elements[0], nil
}

// PerformAction triggers an accessibility action on the specified element.
func (d SimDevice) PerformAction(element *AccessibilityElement, actionType uint64) error {
	if d.id == 0 {
		return fmt.Errorf("device is nil")
	}

	if d.State() != SimDeviceStateBooted {
		return fmt.Errorf("device is not booted")
	}

	// Create Request
	reqClass := objc.GetClass("AXPTranslatorRequest")
	alloc := objc.Send[objc.ID](objc.ID(reqClass), objc.Sel("alloc"))
	request := objc.Send[objc.ID](alloc, objc.Sel("init"))

	// RequestType 2 = Action
	objc.Send[objc.ID](request, objc.Sel("setRequestType:"), 2)
	objc.Send[objc.ID](request, objc.Sel("setActionType:"), actionType)

	// Create Translation Object from element data
	transClass := objc.GetClass("AXPTranslationObject")
	transAlloc := objc.Send[objc.ID](objc.ID(transClass), objc.Sel("alloc"))
	trans := objc.Send[objc.ID](transAlloc, objc.Sel("init"))

	var objectID uint64
	fmt.Sscanf(element.AXUniqueId, "%d", &objectID)

	objc.Send[objc.ID](trans, objc.Sel("setObjectID:"), objectID)
	objc.Send[objc.ID](trans, objc.Sel("setPid:"), element.PID)
	token := element.Token
	if token == "" {
		token = "xcmcp-fallback-token"
	}
	objc.Send[objc.ID](trans, objc.Sel("setBridgeDelegateToken:"), objc.NSString(token))

	// Set Translation on Request
	objc.Send[objc.ID](request, objc.Sel("setTranslation:"), trans)

	// Send Request
	// Actions usually don't return elements, but we wait for completion
	_, err := d.sendAccessibilityRequest(request)
	return err
}

const (
	AXPRequestTypeElement   = 0
	AXPRequestTypeAttribute = 1
	AXPRequestTypeAction    = 2
	AXPRequestTypeHitTest   = 4

	AXPAttributeTypeChildren = 5
)

func (d SimDevice) sendAccessibilityRequest(request objc.ID) ([]*AccessibilityElement, error) {
	resp, err := d.SendAccessibilityRequestID(request)
	if err != nil {
		return nil, err
	}
	if resp == 0 {
		return nil, nil
	}

	errObj := objc.Send[objc.ID](resp, objc.Sel("error"))
	if errObj != 0 {
		desc := objc.Send[objc.ID](errObj, objc.Sel("localizedDescription"))
		return nil, fmt.Errorf("accessibility error: %s", objc.GoString(desc))
	}

	resultData := objc.Send[objc.ID](resp, objc.Sel("resultData"))
	if resultData == 0 {
		return nil, nil
	}

	count := objc.Send[int](resultData, objc.Sel("count"))
	elements := make([]*AccessibilityElement, 0, count)

	for i := 0; i < count; i++ {
		transObj := objc.Send[objc.ID](resultData, objc.Sel("objectAtIndex:"), i)
		el := parseTranslationObject(transObj)
		if el != nil {
			elements = append(elements, el)
		}
	}

	return elements, nil
}

func parseTranslationObject(trans objc.ID) *AccessibilityElement {
	if trans == 0 {
		return nil
	}

	el := &AccessibilityElement{}

	objID := objc.Send[uint64](trans, objc.Sel("objectID"))
	el.AXUniqueId = fmt.Sprintf("%d", objID)

	el.PID = int32(objc.Send[int](trans, objc.Sel("pid")))

	rawDataObj := objc.Send[objc.ID](trans, objc.Sel("rawElementData"))
	if rawDataObj != 0 {
		length := objc.Send[int](rawDataObj, objc.Sel("length"))
		if length > 0 {
			ptr := objc.Send[uintptr](rawDataObj, objc.Sel("bytes"))
			el.RawData = make([]byte, length)
			src := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), length)
			copy(el.RawData, src)
		}
	}
	// Identifier
	axID := objc.Send[objc.ID](trans, objc.Sel("accessibilityIdentifier"))
	if axID != 0 {
		el.AXIdentifier = objc.GoString(axID)
	}

	// Label
	axLabel := objc.Send[objc.ID](trans, objc.Sel("accessibilityLabel"))
	if axLabel != 0 {
		el.AXLabel = objc.GoString(axLabel)
	}

	// Value
	axValue := objc.Send[objc.ID](trans, objc.Sel("accessibilityValue"))
	if axValue != 0 {
		// Value can be string or number
		desc := objc.Send[objc.ID](axValue, objc.Sel("description"))
		el.AXValue = objc.GoString(desc)
	}

	// Frame from Description
	descFrame := objc.Send[objc.ID](trans, objc.Sel("description"))
	if descFrame != 0 {
		d := objc.GoString(descFrame)
		// Append to AXValue to debug
		el.AXValue = fmt.Sprintf("FrameDesc: %s | Val: %s", d, el.AXValue)
	}

	return el
}
