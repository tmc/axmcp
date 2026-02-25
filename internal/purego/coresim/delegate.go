package coresim

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/tmc/xcmcp/internal/purego/objc"
)

// Exported types for use in accessibility.go
type CGPoint struct{ X, Y float64 }
type CGSize struct{ Width, Height float64 }
type CGRect struct {
	Origin CGPoint
	Size   CGSize
}

var (
	tokenMap         sync.Map // map[string]*SimDevice
	delegate         objc.ID
	initDelegateOnce sync.Once
)

func generateToken() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		// Fallback if crypto/rand fails (unlikely)
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// Manual Block Implementation
var (
	_NSConcreteGlobalBlock uintptr
	_malloc                func(size uintptr) uintptr
	_free                  func(ptr uintptr)
	initBlockOnce          sync.Once
)

type blockDescriptor struct {
	reserved uintptr
	size     uintptr
}

type blockLayout struct {
	isa        uintptr
	flags      int32
	reserved   int32
	invoke     uintptr
	descriptor uintptr
}

func initBlocks() {
	initBlockOnce.Do(func() {
		lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			fmt.Println("Failed to open libSystem:", err)
			return
		}
		_NSConcreteGlobalBlock, err = purego.Dlsym(lib, "_NSConcreteGlobalBlock")
		if err != nil {
			fmt.Println("Failed to find _NSConcreteGlobalBlock:", err)
		}
		purego.RegisterLibFunc(&_malloc, lib, "malloc")
		purego.RegisterLibFunc(&_free, lib, "free")
	})
}

func createGlobalBlock(invoke uintptr) (unsafe.Pointer, func()) {
	initBlocks()
	if _NSConcreteGlobalBlock == 0 || _malloc == nil {
		return nil, func() {}
	}

	// Allocate Memory for Layout and Descriptor
	layoutSize := unsafe.Sizeof(blockLayout{})
	descSize := unsafe.Sizeof(blockDescriptor{})

	// We allocate them separately or together?
	// C Blocks imply descriptor is pointer. So separate.

	layoutPtr := _malloc(layoutSize)
	descPtr := _malloc(descSize)

	// Cleanup function
	cleanup := func() {
		_free(layoutPtr)
		_free(descPtr)
	}

	// Initialize Descriptor
	desc := (*blockDescriptor)(unsafe.Pointer(descPtr))
	desc.reserved = 0
	desc.size = layoutSize

	// Initialize Layout
	layout := (*blockLayout)(unsafe.Pointer(layoutPtr))
	layout.isa = _NSConcreteGlobalBlock
	layout.flags = 1 << 28 // BLOCK_IS_GLOBAL
	layout.reserved = 0
	layout.invoke = invoke
	layout.descriptor = descPtr

	return unsafe.Pointer(layoutPtr), cleanup
}

func InitAXPDelegate() {
	initDelegateOnce.Do(func() {
		// Define class
		cls := objc.GetClass("NSObject")
		newClsName := "XCMCP_AXPDelegate"
		newCls := objc.AllocateClassPair(cls, newClsName, 0)

		// Method: accessibilityTranslationDelegateBridgeCallbackWithToken:
		imp := objc.NewCallback(func(self, _cmd, tokenID objc.ID) objc.ID {
			token := objc.GoString(tokenID)

			blk := objc.NewBlock(func(blk, req objc.ID) objc.ID {
				val, ok := tokenMap.Load(token)
				if !ok {
					return 0
				}
				device := val.(*SimDevice)

				resp, err := device.SendAccessibilityRequestID(req)
				if err != nil {
					return 0
				}
				return resp
			})
			return objc.ID(uintptr(unsafe.Pointer(blk)))
		})
		objc.AddMethod(newCls, objc.Sel("accessibilityTranslationDelegateBridgeCallbackWithToken:"), imp, "@:@:@")

		// Method: accessibilityTranslationConvertPlatformFrameToSystem:withToken:
		// We implement this to avoid crash. We don't care about correctness for now (return garbage/0).
		// Signature involves structs. We try returning 0 (which might zero-out d0, effectively returning {{0,0},{0,0}}).
		impConvert := objc.NewCallback(func(self, _cmd objc.ID, framePtr unsafe.Pointer, tokenID objc.ID) int {
			return 0
		})
		// Signature: Return Struct, Self, Sel, Arg Struct, Arg ID
		// Encoding for CGRect is usually `{CGRect={CGPoint=dd}{CGSize=dd}}`.
		// Let's use simple signature string or correct one.
		objc.AddMethod(newCls, objc.Sel("accessibilityTranslationConvertPlatformFrameToSystem:withToken:"), impConvert, "{CGRect={CGPoint=dd}{CGSize=dd}}@:{CGRect={CGPoint=dd}{CGSize=dd}}@")

		// Method: accessibilityTranslationRootParentWithToken:
		impRoot := objc.NewCallback(func(self, _cmd, tokenID objc.ID) objc.ID {
			return 0
		})
		objc.AddMethod(newCls, objc.Sel("accessibilityTranslationRootParentWithToken:"), impRoot, "@@:@")

		objc.RegisterClassPair(newCls)

		// Create Singleton Instance
		delegate = objc.Send[objc.ID](objc.ID(newCls), objc.Sel("new"))

		// Set Delegate on AXPTranslator
		translatorCls := objc.GetClass("AXPTranslator")
		translator := objc.Send[objc.ID](objc.ID(translatorCls), objc.Sel("sharedInstance"))
		objc.Send[objc.ID](translator, objc.Sel("setBridgeTokenDelegate:"), delegate)
	})
}

// RegisterDeviceForToken registers a device and returns a token string.
func RegisterDeviceForToken(d *SimDevice) string {
	InitAXPDelegate()
	token := generateToken()
	tokenMap.Store(token, d)
	return token
}

// UnregisterToken removes the token.
func UnregisterToken(token string) {
	tokenMap.Delete(token)
}

var (
	axQueue     objc.ID
	axQueueOnce sync.Once
)

func getAXQueue() objc.ID {
	axQueueOnce.Do(func() {
		axQueue = objc.ID(CreateQueue("xcmcp.accessibility"))
	})
	return axQueue
}

// SendAccessibilityRequestID sends a request using ID and waits for response.
func (d SimDevice) SendAccessibilityRequestID(requestID objc.ID) (objc.ID, error) {
	queue := getAXQueue()
	if queue == 0 {
		return 0, fmt.Errorf("failed to create dispatch queue")
	}

	done := make(chan objc.ID, 1)

	invoke := objc.NewCallback(func(_ uintptr, response objc.ID) {
		done <- response
	})

	blk, _ := createGlobalBlock(invoke)
	if blk == nil {
		return 0, fmt.Errorf("failed to create block")
	}

	objc.Send[objc.ID](d.id, objc.Sel("sendAccessibilityRequestAsync:completionQueue:completionHandler:"), requestID, queue, blk)

	select {
	case resp := <-done:
		return resp, nil
	case <-time.After(5 * time.Second):
		return 0, fmt.Errorf("timeout waiting for accessibility response")
	}
}
