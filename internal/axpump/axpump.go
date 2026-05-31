package axpump

import (
	"fmt"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/x/axuiautomation"
)

const cfStringEncodingUTF8 = 0x08000100

var (
	mu            sync.Mutex
	asserted      = make(map[int32]bool)
	nonAssertable = make(map[int32]bool)
	observers     = make(map[int32]*observerHold)

	cfBooleanOnce  sync.Once
	cfBooleanTrue  uintptr
	cfBooleanFalse uintptr
)

type observerHold struct {
	app      *axuiautomation.Application
	observer *axuiautomation.Observer
}

// Ensure asks a target app to keep its accessibility tree populated while it
// is backgrounded. It is best-effort: native apps commonly reject the Chromium
// AX attributes, and callers should still proceed with a normal snapshot.
// Successful observers are retained for the process lifetime so the target
// keeps sending AX notifications.
func Ensure(pid int32) (bool, error) {
	if pid <= 0 {
		return false, fmt.Errorf("invalid pid %d", pid)
	}

	mu.Lock()
	if nonAssertable[pid] {
		mu.Unlock()
		return false, nil
	}
	if observers[pid] != nil {
		mu.Unlock()
		return true, nil
	}
	mu.Unlock()

	app := axuiautomation.NewApplicationFromPID(pid)
	if app == nil {
		return false, fmt.Errorf("connect to pid %d", pid)
	}

	ok := assertEnabled(app.Root())
	if !ok {
		mu.Lock()
		if !asserted[pid] {
			nonAssertable[pid] = true
		}
		mu.Unlock()
		app.Close()
		return false, nil
	}

	obs, err := axuiautomation.NewObserver(app)
	if err != nil {
		app.Close()
		return true, nil
	}
	root := app.Root()
	noop := func(axuiautomation.ObserverEvent) {}
	for _, name := range []string{
		axuiautomation.NotificationFocusedUIElementChanged,
		"AXFocusedWindowChanged",
		axuiautomation.NotificationApplicationActivated,
		axuiautomation.NotificationApplicationDeactivated,
		axuiautomation.NotificationApplicationHidden,
		axuiautomation.NotificationApplicationShown,
		axuiautomation.NotificationWindowCreated,
		axuiautomation.NotificationWindowMoved,
		axuiautomation.NotificationWindowResized,
		axuiautomation.NotificationValueChanged,
		axuiautomation.NotificationTitleChanged,
		axuiautomation.NotificationSelectedChildrenChanged,
		"AXLayoutChanged",
	} {
		_ = obs.OnNotification(name, root, noop)
	}
	obs.Start()
	axuiautomation.SpinRunLoop(500 * time.Millisecond)

	mu.Lock()
	asserted[pid] = true
	observers[pid] = &observerHold{app: app, observer: obs}
	mu.Unlock()
	return true, nil
}

func assertEnabled(root *axuiautomation.Element) bool {
	if root == nil || root.Ref() == 0 {
		return false
	}
	trueValue := getCFBooleanTrue()
	if trueValue == 0 {
		return false
	}
	manual := setBool(root, "AXManualAccessibility", trueValue)
	enhanced := setBool(root, "AXEnhancedUserInterface", trueValue)
	return manual || enhanced
}

func setBool(el *axuiautomation.Element, name string, value uintptr) bool {
	attr := corefoundation.CFStringCreateWithCString(0, name, cfStringEncodingUTF8)
	if attr == 0 {
		return false
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(attr))
	err := axuiautomation.AXUIElementSetAttributeValue(el.Ref(), uintptr(attr), value)
	return int(err) == 0
}

func getCFBooleanTrue() uintptr {
	cfBooleanOnce.Do(func() {
		lib, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		if sym, err := purego.Dlsym(lib, "kCFBooleanTrue"); err == nil {
			cfBooleanTrue = *(*uintptr)(unsafe.Pointer(sym))
		}
		if sym, err := purego.Dlsym(lib, "kCFBooleanFalse"); err == nil {
			cfBooleanFalse = *(*uintptr)(unsafe.Pointer(sym))
		}
	})
	return cfBooleanTrue
}
