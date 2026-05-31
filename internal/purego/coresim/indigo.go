package coresim

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/tmc/axmcp/internal/purego/objc"
)

var (
	indigoOnce  sync.Once
	indigoCalls struct {
		MessageForMouseNSEvent uintptr
	}
	simKitLib uintptr
)

func initIndigo() {
	indigoOnce.Do(func() {
		var err error
		path := "/Applications/Xcode.app/Contents/Developer/Library/PrivateFrameworks/SimulatorKit.framework/SimulatorKit"
		simKitLib, err = purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			fmt.Printf("Failed to load SimulatorKit: %v\n", err)
			return
		}

		indigoCalls.MessageForMouseNSEvent, _ = purego.Dlsym(simKitLib, "IndigoHIDMessageForButton")
		if indigoCalls.MessageForMouseNSEvent == 0 {
			indigoCalls.MessageForMouseNSEvent, _ = purego.Dlsym(simKitLib, "IndigoHIDMessageForMouseNSEvent")
		}
	})
}

type SimDeviceLegacyClient struct {
	id objc.ID
}

func (d SimDevice) ConnectToHID() (*SimDeviceLegacyClient, error) {
	if d.id == 0 {
		return nil, fmt.Errorf("device is nil")
	}

	cls := objc.GetClass("SimDeviceLegacyClient")
	if cls == 0 {
		initIndigo()
		cls = objc.GetClass("SimDeviceLegacyClient")
		if cls == 0 {
			return nil, fmt.Errorf("SimDeviceLegacyClient class not found")
		}
	}

	alloc := objc.Send[objc.ID](objc.ID(cls), objc.Sel("alloc"))

	var errPtr objc.ID
	client := objc.Send[objc.ID](alloc, objc.Sel("initWithDevice:error:"), d.id, unsafe.Pointer(&errPtr))
	if errPtr != 0 {
		return nil, fmt.Errorf("failed to init HID client")
	}

	return &SimDeviceLegacyClient{id: client}, nil
}

func (c *SimDeviceLegacyClient) SendMessage(msg []byte) error {
	if c == nil || c.id == 0 {
		return fmt.Errorf("HID client is nil")
	}
	if len(msg) == 0 {
		return fmt.Errorf("HID message is empty")
	}

	queue := objc.Send[objc.ID](objc.ID(objc.GetClass("OS_dispatch_queue")), objc.Sel("mainQueue"))

	done := make(chan error, 1)
	block := objc.NewBlock(func(_ objc.Block, err objc.ID) {
		if err != 0 {
			desc := objc.Send[objc.ID](err, objc.Sel("localizedDescription"))
			done <- fmt.Errorf("HID error: %s", objc.GoString(desc))
		} else {
			done <- nil
		}
	})
	defer block.Release()

	objc.Send[objc.ID](c.id, objc.Sel("sendWithMessage:freeWhenDone:completionQueue:completion:"),
		unsafe.Pointer(&msg[0]), false, queue, unsafe.Pointer(&block))

	return <-done
}

func (d SimDevice) Tap(x, y float64) error {
	initIndigo()
	if indigoCalls.MessageForMouseNSEvent == 0 {
		return fmt.Errorf("IndigoHIDMessageForMouseNSEvent symbol not found")
	}

	pt := struct{ X, Y float64 }{x, y}

	var msgForMouse func(*struct{ X, Y float64 }, uintptr, int32, int32, bool) *IndigoMessage
	purego.RegisterFunc(&msgForMouse, indigoCalls.MessageForMouseNSEvent)

	template := msgForMouse(&pt, 0, 0x32, 2, false)
	if template == nil {
		return fmt.Errorf("failed to create template message")
	}

	message := make([]byte, 320)

	srcBytes := unsafe.Slice((*byte)(unsafe.Pointer(template)), 176)
	copy(message[0:], srcBytes)

	// IndigoTouch uses pack(4): payload offset 0x20, event offset 0x10,
	// touch X/Y offsets 0x0c and 0x14.
	writeFloat64(message, 0x3C, x)
	writeFloat64(message, 0x44, y)

	copy(message[176:], message[32:32+144])

	writeUint32(message, 192, 1)
	writeUint32(message, 196, 2)

	client, err := d.ConnectToHID()
	if err != nil {
		return err
	}

	return client.SendMessage(message)
}

func writeFloat64(b []byte, offset int, val float64) {
	u := *(*uint64)(unsafe.Pointer(&val))
	b[offset] = byte(u)
	b[offset+1] = byte(u >> 8)
	b[offset+2] = byte(u >> 16)
	b[offset+3] = byte(u >> 24)
	b[offset+4] = byte(u >> 32)
	b[offset+5] = byte(u >> 40)
	b[offset+6] = byte(u >> 48)
	b[offset+7] = byte(u >> 56)
}

func writeUint32(b []byte, offset int, val uint32) {
	b[offset] = byte(val)
	b[offset+1] = byte(val >> 8)
	b[offset+2] = byte(val >> 16)
	b[offset+3] = byte(val >> 24)
}
