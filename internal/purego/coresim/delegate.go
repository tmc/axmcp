//go:build !windows

package coresim

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/tmc/axmcp/internal/purego/objc"
)

var (
	tokenMap         sync.Map // map[string]*SimDevice
	delegate         objc.ID
	initDelegateOnce sync.Once
)

func generateToken() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

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

	layoutSize := unsafe.Sizeof(blockLayout{})
	descSize := unsafe.Sizeof(blockDescriptor{})

	layoutPtr := _malloc(layoutSize)
	descPtr := _malloc(descSize)

	cleanup := func() {
		_free(layoutPtr)
		_free(descPtr)
	}

	desc := (*blockDescriptor)(unsafe.Pointer(descPtr))
	desc.reserved = 0
	desc.size = layoutSize

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
		cls := objc.GetClass("NSObject")
		newClsName := "XCMCP_AXPDelegate"
		newCls := objc.AllocateClassPair(cls, newClsName, 0)

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

		// AXPTranslator requires this delegate method. Returning zero keeps
		// the bridge stable for callers that do not need frame conversion.
		impConvert := objc.NewCallback(func(self, _cmd objc.ID, framePtr unsafe.Pointer, tokenID objc.ID) int {
			return 0
		})
		objc.AddMethod(newCls, objc.Sel("accessibilityTranslationConvertPlatformFrameToSystem:withToken:"), impConvert, "{CGRect={CGPoint=dd}{CGSize=dd}}@:{CGRect={CGPoint=dd}{CGSize=dd}}@")

		impRoot := objc.NewCallback(func(self, _cmd, tokenID objc.ID) objc.ID {
			return 0
		})
		objc.AddMethod(newCls, objc.Sel("accessibilityTranslationRootParentWithToken:"), impRoot, "@@:@")

		objc.RegisterClassPair(newCls)

		delegate = objc.Send[objc.ID](objc.ID(newCls), objc.Sel("new"))

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

// axQueueLabel is the GCD label used for the CoreSimulator accessibility
// dispatch queue. It is intentionally generic so the same internal/purego
// package can be vendored into multiple binaries (axmcp, xcmcp, ...) without
// any one of them claiming a sibling's name in macOS' dispatch label
// registry. Override at startup via the COREMSIM_QUEUE_LABEL env var when a
// caller wants its own per-binary label for log filtering.
const axQueueLabel = "coresim.accessibility"

func getAXQueue() objc.ID {
	axQueueOnce.Do(func() {
		label := axQueueLabel
		if v := strings.TrimSpace(os.Getenv("COREMSIM_QUEUE_LABEL")); v != "" {
			label = v
		}
		axQueue = objc.ID(CreateQueue(label))
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
