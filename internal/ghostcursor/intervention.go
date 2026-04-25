package ghostcursor

import (
	"math"
	"os"
	"sync"
	"time"
	"unsafe"

	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
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
	id := registerInterventionController(c)
	tap := coregraphics.CGEventTapCreate(
		coregraphics.KCGSessionEventTap,
		coregraphics.KCGHeadInsertEventTap,
		eventTapOptionListenOnly,
		mouseInterventionMask(),
		ghostCursorInterventionCallback,
		unsafe.Pointer(id),
	)
	if tap == 0 {
		unregisterInterventionController(id)
		return
	}
	src := corefoundation.CFMachPortCreateRunLoopSource(0, tap, 0)
	if src == 0 {
		corefoundation.CFMachPortInvalidate(tap)
		corefoundation.CFRelease(corefoundation.CFTypeRef(tap))
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

func ghostCursorInterventionCallback(_ uintptr, typ coregraphics.CGEventType, event uintptr, userInfo unsafe.Pointer) uintptr {
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
	if pid := int(coregraphics.CGEventGetIntegerValueField(coregraphics.CGEventRef(event), coregraphics.KCGEventSourceUnixProcessID)); pid == os.Getpid() {
		return event
	}
	loc := coregraphics.CGEventGetLocation(coregraphics.CGEventRef(event))
	go c.handleUserIntervention(int(math.Round(loc.X)), int(math.Round(loc.Y)))
	return event
}

func registerInterventionController(c *Controller) uintptr {
	interventionRegistry.Lock()
	defer interventionRegistry.Unlock()
	if interventionRegistry.byID == nil {
		interventionRegistry.byID = make(map[uintptr]*Controller)
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

func interventionController(userInfo unsafe.Pointer) *Controller {
	id := uintptr(userInfo)
	if id == 0 {
		return nil
	}
	interventionRegistry.Lock()
	defer interventionRegistry.Unlock()
	return interventionRegistry.byID[id]
}
