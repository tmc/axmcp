package main

import (
	"context"
	"fmt"

	"github.com/tmc/axmcp/internal/computeruse"
)

// nativeDispatchGuard reads current OS facts before each dispatch. Tests replace
// these reads, while exercising the same decisions and event sequence as the OS
// adapter. The closures borrow handles from the action's lease.
type nativeDispatchGuard struct {
	authorize      func() (computeruse.PermissionState, computeruse.ApprovalState, error)
	processStart   func() (nativeStart, error)
	window         func() (computeruse.WindowInfo, bool, error)
	focusedWindow  func() (uint32, error)
	elementFocused func() (bool, error)
}

func (g nativeDispatchGuard) check(ctx context.Context, target nativeTarget, action string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, a, err := g.authorize()
	if err != nil {
		return err
	}
	if p.Pending || !a.Approved {
		return fmt.Errorf("native authorization changed")
	}
	start, err := g.processStart()
	if err != nil {
		return err
	}
	if start != target.Start {
		return fmt.Errorf("native process changed")
	}
	window, exists, err := g.window()
	if err != nil {
		return err
	}
	if !exists || !sameNativeWindowInfo(target.Window, window) {
		return fmt.Errorf("observed window changed before dispatch")
	}
	if err := checkNativeFocus(action, target.Window.WindowID, g.focusedWindow); err != nil {
		return err
	}
	if g.elementFocused != nil {
		focused, err := g.elementFocused()
		if err != nil {
			return err
		}
		if !focused {
			return fmt.Errorf("focus moved away from observed element")
		}
	}
	return ctx.Err()
}

func sameNativeWindowInfo(w, current computeruse.WindowInfo) bool {
	return w.WindowID != 0 && w.WindowID == current.WindowID && w.X == current.X && w.Y == current.Y && w.Width == current.Width && w.Height == current.Height
}
