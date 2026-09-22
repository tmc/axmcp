// Package intervention detects recent physical user input.
package intervention

import (
	"fmt"
	"os"
	"sync"
	"time"
	"unsafe"

	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
	"github.com/tmc/axmcp/internal/purego/cfhandle"
)

const defaultQuietPeriod = 750 * time.Millisecond

// Config controls the user-intervention monitor.
type Config struct {
	Enabled     bool
	QuietPeriod time.Duration
}

// Status reports the monitor state without exposing input payloads.
type Status struct {
	Enabled     bool
	QuietPeriod time.Duration
	LastInput   time.Time
	LastType    string
}

// Monitor records that physical input happened recently.
type Monitor struct {
	quietPeriod time.Duration
	lifecycle   sync.Mutex

	mu        sync.Mutex
	enabled   bool
	lastInput time.Time
	lastType  string

	lib    *cfhandle.Library
	tap    corefoundation.CFMachPortRef
	source corefoundation.CFRunLoopSourceRef
}

// New returns a monitor. It does not install an event tap until Start is called.
func New(cfg Config) *Monitor {
	quiet := cfg.QuietPeriod
	if quiet <= 0 {
		quiet = defaultQuietPeriod
	}
	return &Monitor{
		enabled:     cfg.Enabled,
		quietPeriod: quiet,
	}
}

// Start installs a listen-only event tap when the monitor is enabled.
// Calling Start on an already started monitor has no effect.
func (m *Monitor) Start() error {
	if m == nil || !m.isEnabled() {
		return nil
	}
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	m.mu.Lock()
	started := m.tap != 0
	m.mu.Unlock()
	if started {
		return nil
	}
	lib, err := cfhandle.Open()
	if err != nil {
		return err
	}
	mask := eventMask(
		coregraphics.KCGEventKeyDown,
		coregraphics.KCGEventFlagsChanged,
		coregraphics.KCGEventLeftMouseDown,
		coregraphics.KCGEventRightMouseDown,
		coregraphics.KCGEventOtherMouseDown,
		coregraphics.KCGEventScrollWheel,
	)
	tap := coregraphics.CGEventTapCreate(
		coregraphics.KCGSessionEventTap,
		coregraphics.KCGHeadInsertEventTap,
		coregraphics.CGEventTapOptions(1), // kCGEventTapOptionListenOnly
		mask,
		m.callback,
		nil,
	)
	if tap == 0 {
		return fmt.Errorf("create listen-only event tap")
	}
	source := corefoundation.CFMachPortCreateRunLoopSource(0, tap, 0)
	if source == 0 {
		lib.Release(uintptr(tap))
		return fmt.Errorf("create event tap run loop source")
	}
	corefoundation.CFRunLoopAddSource(corefoundation.CFRunLoopGetMain(), source, corefoundation.KCFRunLoopCommonModes)
	coregraphics.CGEventTapEnable(tap, true)

	m.mu.Lock()
	m.lib = lib
	m.tap = tap
	m.source = source
	m.mu.Unlock()
	return nil
}

// Close releases the event tap resources.
func (m *Monitor) Close() {
	if m == nil {
		return
	}
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	m.mu.Lock()
	lib := m.lib
	tap := m.tap
	source := m.source
	m.tap = 0
	m.source = 0
	m.mu.Unlock()
	if source != 0 {
		corefoundation.CFRunLoopRemoveSource(corefoundation.CFRunLoopGetMain(), source, corefoundation.KCFRunLoopCommonModes)
		lib.Release(uintptr(source))
	}
	if tap != 0 {
		coregraphics.CGEventTapEnable(tap, false)
		corefoundation.CFMachPortInvalidate(tap)
		lib.Release(uintptr(tap))
	}
}

// Record records a physical input event. It is exported for tests.
func (m *Monitor) Record(kind string, now time.Time) {
	if m == nil || !m.isEnabled() {
		return
	}
	m.mu.Lock()
	m.lastInput = now
	m.lastType = kind
	m.mu.Unlock()
}

// Blocked reports whether actions should pause for recent physical input.
func (m *Monitor) Blocked(now time.Time) (Status, bool) {
	status := m.Status()
	if !status.Enabled || status.LastInput.IsZero() {
		return status, false
	}
	return status, now.Sub(status.LastInput) < status.QuietPeriod
}

// Status returns the current monitor state.
func (m *Monitor) Status() Status {
	if m == nil {
		return Status{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return Status{
		Enabled:     m.enabled,
		QuietPeriod: m.quietPeriod,
		LastInput:   m.lastInput,
		LastType:    m.lastType,
	}
}

func (m *Monitor) isEnabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.enabled
}

func (m *Monitor) callback(_ coregraphics.CGEventTapProxy, typ coregraphics.CGEventType, event coregraphics.CGEventRef, _ unsafe.Pointer) coregraphics.CGEventRef {
	if typ == coregraphics.KCGEventTapDisabledByTimeout || typ == coregraphics.KCGEventTapDisabledByUserInput {
		m.mu.Lock()
		tap, lib := m.tap, m.lib
		if tap != 0 {
			lib.Retain(uintptr(tap))
		}
		m.mu.Unlock()
		if tap != 0 {
			coregraphics.CGEventTapEnable(tap, true)
			lib.Release(uintptr(tap))
		}
		return event
	}
	if eventSourcePID(event) == int64(os.Getpid()) {
		return event
	}
	m.Record(typ.String(), time.Now())
	return event
}

func eventSourcePID(event coregraphics.CGEventRef) int64 {
	if event == 0 {
		return 0
	}
	return coregraphics.CGEventGetIntegerValueField(event, coregraphics.KCGEventSourceUnixProcessID)
}

func eventMask(types ...coregraphics.CGEventType) coregraphics.CGEventMask {
	var mask coregraphics.CGEventMask
	for _, typ := range types {
		mask |= 1 << uint(typ)
	}
	return mask
}
