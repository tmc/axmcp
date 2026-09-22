package ghostcursor

import (
	"math"
	"os"
	"sync"
	"time"

	"github.com/ebitengine/purego"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
	"github.com/tmc/axmcp/internal/purego/cfhandle"
)

const (
	eventTapOptionListenOnly  coregraphics.CGEventTapOptions = 1
	userInterventionCooldown                                 = 150 * time.Millisecond
	userInterventionHideDelay                                = 900 * time.Millisecond
)

var interventionRegistry struct {
	sync.Mutex
	next uintptr
	byID map[uintptr]*Controller
}

func (c *Controller) startInterventionMonitor() {
	if c.interventionTap != 0 {
		return
	}
	lib, err := cfhandle.Open()
	if err != nil {
		return
	}
	create, err := loadInterventionTap()
	if err != nil {
		return
	}
	c.interventionLib = lib
	id := registerInterventionController(c)
	if id == 0 {
		return
	}
	tap := create(
		coregraphics.KCGSessionEventTap,
		coregraphics.KCGHeadInsertEventTap,
		eventTapOptionListenOnly,
		mouseInterventionMask(),
		id,
	)
	if tap == 0 {
		unregisterInterventionController(id)
		return
	}
	src := corefoundation.CFMachPortCreateRunLoopSource(0, tap, 0)
	if src == 0 {
		corefoundation.CFMachPortInvalidate(tap)
		lib.Release(uintptr(tap))
		unregisterInterventionController(id)
		return
	}
	corefoundation.CFRunLoopAddSource(corefoundation.CFRunLoopGetMain(), src, corefoundation.KCFRunLoopCommonModes)
	coregraphics.CGEventTapEnable(tap, true)
	c.interventionTap = tap
	c.interventionSrc = src
	c.interventionID = id
}

func (c *Controller) handleUserIntervention(_ int, _ int) {
	c.mu.Lock()
	active := c.enabled && c.visible && c.hasCursor
	if active {
		c.activity = ActivityPaused
	}
	seq := c.seq.Add(1)
	c.mu.Unlock()
	if !active {
		return
	}
	now := time.Now().UnixNano()
	if last := c.lastIntervention.Load(); last != 0 && time.Duration(now-last) < userInterventionCooldown {
		return
	}
	c.lastIntervention.Store(now)
	runOnMain(func() {
		if c.win.GetID() == 0 {
			return
		}
		c.applyActivityAnimated(ActivityPaused, pausedFadeTime)
		c.applyMotionTransform(ActivityPaused, 0, 0, 0)
	})
	c.hideAfter(seq, userInterventionHideDelay)
}

func mouseInterventionMask() coregraphics.CGEventMask {
	types := []coregraphics.CGEventType{
		coregraphics.KCGEventMouseMoved,
		coregraphics.KCGEventLeftMouseDragged,
		coregraphics.KCGEventRightMouseDragged,
		coregraphics.KCGEventOtherMouseDragged,
	}
	var mask coregraphics.CGEventMask
	for _, typ := range types {
		mask |= 1 << uint(typ)
	}
	return mask
}

func ghostCursorInterventionCallback(_ coregraphics.CGEventTapProxy, typ coregraphics.CGEventType, event coregraphics.CGEventRef, userInfo uintptr) coregraphics.CGEventRef {
	switch typ {
	case coregraphics.KCGEventTapDisabledByTimeout, coregraphics.KCGEventTapDisabledByUserInput:
		if c := interventionController(userInfo); c != nil && c.interventionTap != 0 {
			coregraphics.CGEventTapEnable(c.interventionTap, true)
		}
		return event
	}
	c := interventionController(userInfo)
	if c == nil || event == 0 {
		return event
	}
	if pid := int(coregraphics.CGEventGetIntegerValueField(event, coregraphics.KCGEventSourceUnixProcessID)); pid == os.Getpid() {
		return event
	}
	loc := coregraphics.CGEventGetLocation(event)
	go c.handleUserIntervention(int(math.Round(loc.X)), int(math.Round(loc.Y)))
	return event
}

func registerInterventionController(c *Controller) uintptr {
	interventionRegistry.Lock()
	defer interventionRegistry.Unlock()
	if interventionRegistry.byID == nil {
		interventionRegistry.byID = make(map[uintptr]*Controller)
	}
	if interventionRegistry.next == ^uintptr(0) {
		return 0
	}
	interventionRegistry.next++
	id := interventionRegistry.next
	interventionRegistry.byID[id] = c
	return id
}

func unregisterInterventionController(id uintptr) {
	if id == 0 {
		return
	}
	interventionRegistry.Lock()
	delete(interventionRegistry.byID, id)
	interventionRegistry.Unlock()
}

func interventionController(userInfo uintptr) *Controller {
	id := userInfo
	if id == 0 {
		return nil
	}
	interventionRegistry.Lock()
	defer interventionRegistry.Unlock()
	return interventionRegistry.byID[id]
}

// The registry token stays an integer across the native boundary. It is never
// dereferenced or converted to a Go pointer, and tokens are never reused.
type interventionTapCreate func(coregraphics.CGEventTapLocation, coregraphics.CGEventTapPlacement, coregraphics.CGEventTapOptions, coregraphics.CGEventMask, uintptr) corefoundation.CFMachPortRef

var loadInterventionTap = sync.OnceValues(func() (interventionTapCreate, error) {
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, err
	}
	symbol, err := purego.Dlsym(lib, "CGEventTapCreate")
	if err != nil {
		_ = purego.Dlclose(lib)
		return nil, err
	}
	var create func(coregraphics.CGEventTapLocation, coregraphics.CGEventTapPlacement, coregraphics.CGEventTapOptions, coregraphics.CGEventMask, uintptr, uintptr) corefoundation.CFMachPortRef
	purego.RegisterFunc(&create, symbol)
	callback := purego.NewCallback(ghostCursorInterventionCallback)
	// Keep the framework and callback trampoline for the process lifetime.
	return func(location coregraphics.CGEventTapLocation, placement coregraphics.CGEventTapPlacement, options coregraphics.CGEventTapOptions, mask coregraphics.CGEventMask, token uintptr) corefoundation.CFMachPortRef {
		return create(location, placement, options, mask, callback, token)
	}, nil
})
