// Package coresim provides purego bindings for CoreSimulator.framework.
package coresim

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/tmc/xcmcp/internal/purego/objc"
)

var (
	once        sync.Once
	coreSimLib  uintptr
	axTranslLib uintptr
	available   bool
)

// SimDeviceState represents the state of a simulator device.
type SimDeviceState uint64

const (
	SimDeviceStateCreating     SimDeviceState = 0
	SimDeviceStateShutdown     SimDeviceState = 1
	SimDeviceStateBooting      SimDeviceState = 2
	SimDeviceStateBooted       SimDeviceState = 3
	SimDeviceStateShuttingDown SimDeviceState = 4
)

func (s SimDeviceState) String() string {
	switch s {
	case SimDeviceStateCreating:
		return "Creating"
	case SimDeviceStateShutdown:
		return "Shutdown"
	case SimDeviceStateBooting:
		return "Booting"
	case SimDeviceStateBooted:
		return "Booted"
	case SimDeviceStateShuttingDown:
		return "ShuttingDown"
	default:
		return fmt.Sprintf("Unknown(%d)", s)
	}
}

func init() {
	once.Do(func() {
		var err error
		coreSimLib, err = objc.Dlopen("/Library/Developer/PrivateFrameworks/CoreSimulator.framework/CoreSimulator", objc.RTLD_NOW|objc.RTLD_GLOBAL)
		if err != nil {
			fmt.Printf("Failed to load CoreSimulator: %v\n", err)
			return
		}

		// Try to find AccessibilityPlatformTranslation.framework
		// It's usually inside Xcode.app
		xcodePaths := []string{
			"/Applications/Xcode.app/Contents/Developer/Platforms/MacOSX.platform/Developer/SDKs/MacOSX.sdk/System/Library/PrivateFrameworks/AccessibilityPlatformTranslation.framework/AccessibilityPlatformTranslation",
			"/Applications/Xcode.app/Contents/SharedFrameworks/AccessibilityPlatformTranslation.framework/AccessibilityPlatformTranslation",
		}

		for _, path := range xcodePaths {
			axTranslLib, err = objc.Dlopen(path, objc.RTLD_NOW|objc.RTLD_GLOBAL)
			if err == nil {
				// verify we loaded it by checking for AXPTranslator class
				if objc.GetClass("AXPTranslator") != 0 {
					break
				}
			}
		}

		available = true
	})
}

// Available returns true if CoreSimulator.framework was loaded successfully.
func Available() bool {
	return available
}

// SimServiceContext wraps the SimServiceContext class.
type SimServiceContext struct {
	id objc.ID
}

// SharedServiceContext returns the shared service context for the current Xcode.
func SharedServiceContext() (SimServiceContext, error) {
	cls := objc.GetClass("SimServiceContext")
	if cls == 0 {
		return SimServiceContext{}, fmt.Errorf("SimServiceContext class not found")
	}

	// Get developer dir from xcode-select
	developerDir := "/Applications/Xcode.app/Contents/Developer"

	// Create error pointer
	var errPtr objc.ID

	ctx := objc.Send[objc.ID](objc.ID(cls), objc.Sel("sharedServiceContextForDeveloperDir:error:"),
		objc.NSString(developerDir), uintptr(unsafe.Pointer(&errPtr)))

	if errPtr != 0 {
		errDesc := objc.Send[objc.ID](errPtr, objc.Sel("localizedDescription"))
		return SimServiceContext{}, fmt.Errorf("failed to get service context: %s", objc.GoString(errDesc))
	}

	if ctx == 0 {
		return SimServiceContext{}, fmt.Errorf("failed to get service context (nil)")
	}

	return SimServiceContext{id: ctx}, nil
}

// DefaultDeviceSet returns the default device set.
func (c SimServiceContext) DefaultDeviceSet() (SimDeviceSet, error) {
	if c.id == 0 {
		return SimDeviceSet{}, fmt.Errorf("service context is nil")
	}

	var errPtr objc.ID
	set := objc.Send[objc.ID](c.id, objc.Sel("defaultDeviceSetWithError:"), uintptr(unsafe.Pointer(&errPtr)))

	if errPtr != 0 {
		errDesc := objc.Send[objc.ID](errPtr, objc.Sel("localizedDescription"))
		return SimDeviceSet{}, fmt.Errorf("failed to get device set: %s", objc.GoString(errDesc))
	}

	if set == 0 {
		return SimDeviceSet{}, fmt.Errorf("failed to get device set (nil)")
	}

	return SimDeviceSet{id: set}, nil
}

// SimDeviceSet wraps the SimDeviceSet class.
type SimDeviceSet struct {
	id objc.ID
}

// DefaultSet returns the default SimDeviceSet via SimServiceContext.
func DefaultSet() (SimDeviceSet, error) {
	ctx, err := SharedServiceContext()
	if err != nil {
		return SimDeviceSet{}, err
	}
	return ctx.DefaultDeviceSet()
}

// Devices returns all devices in the set.
func (s SimDeviceSet) Devices() []SimDevice {
	if s.id == 0 {
		return nil
	}

	arr := objc.Send[objc.ID](s.id, objc.Sel("devices"))
	if arr == 0 {
		return nil
	}

	count := objc.Send[int](arr, objc.Sel("count"))
	devices := make([]SimDevice, 0, count)

	for i := 0; i < count; i++ {
		dev := objc.Send[objc.ID](arr, objc.Sel("objectAtIndex:"), i)
		if dev != 0 {
			devices = append(devices, SimDevice{id: dev})
		}
	}

	return devices
}

// SimDevice wraps the SimDevice class.
type SimDevice struct {
	id objc.ID
}

// ID returns the underlying Objective-C object ID.
func (d SimDevice) ID() objc.ID {
	return d.id
}

// IsNil returns true if the device is nil.
func (d SimDevice) IsNil() bool {
	return d.id == 0
}

// UDID returns the device's UDID as a string.
func (d SimDevice) UDID() string {
	if d.id == 0 {
		return ""
	}
	uuid := objc.Send[objc.ID](d.id, objc.Sel("UDID"))
	if uuid == 0 {
		return ""
	}
	str := objc.Send[objc.ID](uuid, objc.Sel("UUIDString"))
	return objc.GoString(str)
}

// Name returns the device's name.
func (d SimDevice) Name() string {
	if d.id == 0 {
		return ""
	}
	str := objc.Send[objc.ID](d.id, objc.Sel("name"))
	return objc.GoString(str)
}

// State returns the device's current state.
func (d SimDevice) State() SimDeviceState {
	if d.id == 0 {
		return SimDeviceStateShutdown
	}
	state := objc.Send[uint64](d.id, objc.Sel("state"))
	return SimDeviceState(state)
}

// StateString returns a human-readable state string.
func (d SimDevice) StateString() string {
	if d.id == 0 {
		return ""
	}
	str := objc.Send[objc.ID](d.id, objc.Sel("stateString"))
	return objc.GoString(str)
}

// DeviceTypeIdentifier returns the device type identifier.
func (d SimDevice) DeviceTypeIdentifier() string {
	if d.id == 0 {
		return ""
	}
	str := objc.Send[objc.ID](d.id, objc.Sel("deviceTypeIdentifier"))
	return objc.GoString(str)
}

// RuntimeIdentifier returns the runtime identifier.
func (d SimDevice) RuntimeIdentifier() string {
	if d.id == 0 {
		return ""
	}
	str := objc.Send[objc.ID](d.id, objc.Sel("runtimeIdentifier"))
	return objc.GoString(str)
}

// SendAccessibilityRequestAsync sends an accessibility request asynchronously.
// The response is delivered to the callback when complete.
func (d SimDevice) SendAccessibilityRequestAsync(request objc.ID, queue objc.ID, callback func(response objc.ID)) {
	if d.id == 0 {
		return
	}

	// Create block for completion handler
	block := objc.NewBlock(func(_ objc.Block, response objc.ID) {
		callback(response)
	})
	defer block.Release()

	objc.Send[objc.ID](d.id, objc.Sel("sendAccessibilityRequestAsync:completionQueue:completionHandler:"),
		request, queue, unsafe.Pointer(block))
}

// FindDeviceByUDID finds a device by its UDID.
func FindDeviceByUDID(udid string) (SimDevice, bool) {
	set, err := DefaultSet()
	if err != nil {
		return SimDevice{}, false
	}

	for _, dev := range set.Devices() {
		if dev.UDID() == udid {
			return dev, true
		}
	}
	return SimDevice{}, false
}

// ListBootedDevices returns all booted simulator devices.
func ListBootedDevices() []SimDevice {
	set, err := DefaultSet()
	if err != nil {
		return nil
	}

	var booted []SimDevice
	for _, dev := range set.Devices() {
		if dev.State() == SimDeviceStateBooted {
			booted = append(booted, dev)
		}
	}
	return booted
}

// FindBootedDevice returns the first booted simulator device.
// Returns an error if no booted device is found.
func FindBootedDevice() (SimDevice, error) {
	devices := ListBootedDevices()
	if len(devices) == 0 {
		return SimDevice{}, fmt.Errorf("no booted simulator found")
	}
	return devices[0], nil
}
