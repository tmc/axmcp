//go:build !darwin

// Package skylightinput reports SkyLight input as unavailable off Darwin.
package skylightinput

import (
	"errors"
	"fmt"
)

// ErrUnavailable is returned when the SkyLight input path is unavailable.
var ErrUnavailable = errors.New("skylightinput: required SPI unavailable")

// Point is a screen point in Quartz convention (top-left origin, y-down).
type Point struct {
	X float64
	Y float64
}

// Trace is an optional hook invoked with structured per-call diagnostics.
var Trace func(event string, fields map[string]any)

func trace(event string, fields map[string]any) {
	if Trace != nil {
		Trace(event, fields)
	}
}

// Status returns a one-line availability summary.
func Status() string {
	return "skylightinput=unavailable platform=non-darwin"
}

// Available reports that SkyLight input is unavailable.
func Available() error {
	return ErrUnavailable
}

// ActivateWithoutRaise reports that SkyLight activation is unavailable.
func ActivateWithoutRaise(targetPID int32, targetWindowID uint32) error {
	if err := validatePID(targetPID); err != nil {
		return err
	}
	trace("activate_unavailable", map[string]any{
		"pid":       targetPID,
		"window_id": targetWindowID,
	})
	return ErrUnavailable
}

// MouseClick reports that SkyLight mouse input is unavailable.
func MouseClick(pid int32, screenPt, windowLocalPt Point, windowID uint32, clickCount int) error {
	if err := validatePID(pid); err != nil {
		return err
	}
	if clickCount <= 0 {
		return fmt.Errorf("click count must be positive")
	}
	trace("mouse_click_unavailable", map[string]any{
		"pid":          pid,
		"screen":       screenPt,
		"window_local": windowLocalPt,
		"window_id":    windowID,
		"click_count":  clickCount,
	})
	return ErrUnavailable
}

func validatePID(pid int32) error {
	if pid <= 0 {
		return fmt.Errorf("pid must be positive")
	}
	return nil
}
