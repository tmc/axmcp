package coresim

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/tmc/xcmcp/internal/purego/objc"
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
		// Load SimulatorKit
		path := "/Applications/Xcode.app/Contents/Developer/Library/PrivateFrameworks/SimulatorKit.framework/SimulatorKit"
		simKitLib, err = purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			fmt.Printf("Failed to load SimulatorKit: %v\n", err)
			return
		}

		// Bind Symbols
		indigoCalls.MessageForMouseNSEvent, _ = purego.Dlsym(simKitLib, "IndigoHIDMessageForButton")
		if indigoCalls.MessageForMouseNSEvent == 0 {
			indigoCalls.MessageForMouseNSEvent, _ = purego.Dlsym(simKitLib, "IndigoHIDMessageForMouseNSEvent")
		}
	})
}

// SimDeviceLegacyClient wrapper
type SimDeviceLegacyClient struct {
	id objc.ID
}

func (d SimDevice) ConnectToHID() (*SimDeviceLegacyClient, error) {
	if d.id == 0 {
		return nil, fmt.Errorf("device is nil")
	}

	cls := objc.GetClass("SimDeviceLegacyClient")
	if cls == 0 {
		// Try loading if not present (should be in SimulatorKit)
		initIndigo()
		cls = objc.GetClass("SimDeviceLegacyClient")
		if cls == 0 {
			return nil, fmt.Errorf("SimDeviceLegacyClient class not found")
		}
	}

	alloc := objc.Send[objc.ID](objc.ID(cls), objc.Sel("alloc"))

	// initWithDevice:error:
	var errPtr objc.ID
	client := objc.Send[objc.ID](alloc, objc.Sel("initWithDevice:error:"), d.id, unsafe.Pointer(&errPtr))
	if errPtr != 0 {
		return nil, fmt.Errorf("failed to init HID client")
	}

	return &SimDeviceLegacyClient{id: client}, nil
}

func (c *SimDeviceLegacyClient) SendMessage(msg []byte) error {
	// sendWithMessage:freeWhenDone:completionQueue:completion:
	// msg must be passed as pointer. CoreSimulator expects it to be malloc'd if freeWhenDone=YES.
	// We can pass a Go pointer if we use freeWhenDone=NO and keep it alive?
	// But `SendMessage` is async usually?
	// The signature: `sendWithMessage:(IndigoMessage *)arg1 freeWhenDone:(BOOL)arg2 ...`
	// We should probably optimize by copying to C memory?
	// For now, let's try passing unsafe.Pointer(&msg[0]) and freeWhenDone:NO.
	// But we must ensure msg survives until completion.
	// To be safe, let's use C.malloc? No Cgo here.
	// We can use `objc.Malloc`? No.
	// We can relying on synchronous wait?
	// Actually `FBSimulatorIndigoHID` copies it.

	// For purego, passing Go memory to C that expects to keep it is dangerous.
	// However, if we wait for completion, it might be ok.
	// Or we can use `freeWhenDone:NO` and hope it copies it immediately?
	// `FBSimulatorIndigoHID`: "copy the message ... let client manage lifecycle".

	// Let's assume we need to pass a valid pointer.
	// Since we don't have C malloc easily, we'll risk Go pointer pinning (implicit in syscall/cgo calls, but purego?)
	// purego might not pin.
	// Let's assume for this MVP we just pass &msg[0] and `freeWhenDone:NO`.

	queue := objc.Send[objc.ID](objc.ID(objc.GetClass("OS_dispatch_queue")), objc.Sel("mainQueue")) // or create one?
	// Actually easier to just run on main queue or background.

	// Block for completion
	done := make(chan error, 1)
	block := objc.NewBlock(func(_ objc.Block, err objc.ID) {
		if err != 0 {
			// Get error description
			desc := objc.Send[objc.ID](err, objc.Sel("localizedDescription"))
			done <- fmt.Errorf("HID error: %s", objc.GoString(desc))
		} else {
			done <- nil
		}
	})
	defer block.Release()

	// sendWithMessage:freeWhenDone:completionQueue:completion:
	objc.Send[objc.ID](c.id, objc.Sel("sendWithMessage:freeWhenDone:completionQueue:completion:"),
		unsafe.Pointer(&msg[0]), false, queue, unsafe.Pointer(&block))

	// Wait for completion
	return <-done
}

func (d SimDevice) Tap(x, y float64) error {
	initIndigo()
	if indigoCalls.MessageForMouseNSEvent == 0 {
		return fmt.Errorf("IndigoHIDMessageForMouseNSEvent symbol not found")
	}

	// 1. Get Template
	// IndigoHIDMessageForMouseNSEvent(CGPoint *point0, CGPoint *point1, int target, int eventType, BOOL something)
	// point0, point1 are pointers to CGPoint.
	// target=0x32, eventType=2 (Touch?), something=0

	// CGPoint is {x, y} (doubles)
	pt := struct{ X, Y float64 }{x, y}

	// Call C function via purego.SyscallN? No, it's a bound function.
	// purego.RegisterFunc?
	// We need to register it.
	var msgForMouse func(*struct{ X, Y float64 }, uintptr, int32, int32, bool) *IndigoMessage
	purego.RegisterFunc(&msgForMouse, indigoCalls.MessageForMouseNSEvent)

	template := msgForMouse(&pt, 0, 0x32, 2, false)
	if template == nil {
		return fmt.Errorf("failed to create template message")
	}

	// 2. Construct Dual Payload Message (Touch Down + Touch Up/Duplication logic)
	// Size 0x140 (320 bytes)
	message := make([]byte, 320)

	// Copy template (0xB0 bytes = 176 bytes)
	// Actually sizeof(IndigoMessage) = header(32) + payload(144) = 176.
	// Wait, is it 0xB0?
	// 0x20 + 0x90 = 0xB0. Yes.

	// Access template bytes?
	// template is *IndigoMessage.
	// We can cast to unsafe.Pointer then slice.
	srcBytes := unsafe.Slice((*byte)(unsafe.Pointer(template)), 176)
	copy(message[0:], srcBytes)

	// 3. Update XRatio, YRatio in first payload.
	// Struct offsets:
	// IndigoMessage -> Payload (offset 32) -> Event (offset 16 inside Payload = 48 total) -> Touch
	// Touch XRatio is at 0x3C inside Touch.
	// Touch starts at 48 (0x30).
	// So XRatio at 48 + 0x3C? No.
	// Struct:
	// Touch { Field1..3 (12 bytes). XRatio (offset 12) }
	// So Touch.XRatio is at 0x30 + 12 = 0x3C.
	// Correct.

	// Wait, `IndigoTouch` struct definition:
	// Field1(4), Field2(4), Field3(4) -> 12 bytes.
	// XRatio (8 bytes).
	// Aligned?
	// 12 bytes + 4 bytes padding to align double?
	// `#pragma pack(4)`!
	// So doubled are 4-byte aligned. No padding.
	// So offset is 12.
	// 0x30 + 0xC = 0x3C.
	// Matches `Indigo.h` comments: `// 0x20 + 0x10 + 0xc = 0x3c`

	writeFloat64(message, 0x3C, x)
	writeFloat64(message, 0x44, y) // YRatio

	// 4. Duplicate Payload for second event
	// Payload 1 starts at 0x20. Length 0x90.
	// Payload 2 starts at 0x20 + 0x90 = 0xB0 (176).
	// Copy Payload 1 to Payload 2.
	copy(message[176:], message[32:32+144])

	// 5. Modify Second Payload
	// second -> event -> touch -> field1 = 1
	// second -> event -> touch -> field2 = 2
	// Payload2 starts 176 (0xB0).
	// Event starts +16 = 192 (0xC0).
	// Touch starts 192.
	// Field1 at 192.
	// Field2 at 196.

	writeUint32(message, 192, 1)
	writeUint32(message, 196, 2)

	// 6. Connect and Send
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
