package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/unix"

	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/apple/x/skylight"
	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/appstate"
	"github.com/tmc/axmcp/internal/computeruse/session"
)

func performNativePointer(ctx context.Context, target nativeTarget, lease *session.Lease, in nativeActInput, guard func() error) (bool, error) {
	if err := prepareNativeEvents(); err != nil {
		return false, err
	}
	root, _, err := lease.Resolve(0)
	if err != nil {
		return false, err
	}
	info := lease.State().ScreenshotMetadata
	var pending *nativePointerEvent
	attempted, err := nativePointerSequence(ctx, in, func(eventCtx context.Context, event nativePointerEvent, cleanup bool) (bool, error) {
		sent, err := postNativePointer(eventCtx, target, root, info, event, cleanup, guard)
		if cleanup {
			pending = nil
			if !sent {
				copy := event
				pending = &copy
			}
		}
		return sent, err
	})
	if pending != nil {
		if start, ok := ctx.Value(nativePointerRecoveryKey{}).(nativePointerRecoveryStart); ok {
			err = errors.Join(err, start(*pending))
		} else {
			err = errors.Join(err, fmt.Errorf("pending pointer recovery unavailable"))
		}
	}
	return attempted, err
}

func postNativePointer(ctx context.Context, target nativeTarget, root *axuiautomation.Element, info *computeruse.ScreenshotInfo, event nativePointerEvent, cleanup bool, guard func() error) (bool, error) {
	if info == nil || info.TargetWindow != target.Window.WindowID {
		return false, fmt.Errorf("pointer target does not match screenshot window")
	}
	local, global, err := mapNativePoint(info, event.Point)
	if err != nil {
		return false, err
	}
	button := coregraphics.KCGMouseButtonLeft
	down, up, drag := coregraphics.KCGEventLeftMouseDown, coregraphics.KCGEventLeftMouseUp, coregraphics.KCGEventLeftMouseDragged
	switch event.Button {
	case "left":
	case "right":
		button = coregraphics.KCGMouseButtonRight
		down, up, drag = coregraphics.KCGEventRightMouseDown, coregraphics.KCGEventRightMouseUp, coregraphics.KCGEventRightMouseDragged
	case "middle":
		button = coregraphics.KCGMouseButtonCenter
		down, up, drag = coregraphics.KCGEventOtherMouseDown, coregraphics.KCGEventOtherMouseUp, coregraphics.KCGEventOtherMouseDragged
	default:
		return false, fmt.Errorf("invalid pointer button")
	}
	kind := down
	switch event.Kind {
	case "down":
	case "up":
		kind = up
	case "drag":
		kind = drag
	default:
		return false, fmt.Errorf("invalid pointer event")
	}
	cg := coregraphics.CGEventCreateMouseEvent(0, kind, corefoundation.CGPoint{X: global.X, Y: global.Y}, button)
	if cg == 0 {
		return false, fmt.Errorf("create native pointer event")
	}
	defer nativeKeyRelease.release(uintptr(cg))
	coregraphics.CGEventSetFlags(cg, 0)
	if err := skylight.RouteMouseEventToWindow(cg, skylight.Window(target.Window.WindowID), target.App.PID, corefoundation.CGPoint{X: local.X, Y: local.Y}, event.Count, event.Group); err != nil {
		return false, err
	}
	// The routing helper stamps button zero, so restore the requested button.
	coregraphics.CGEventSetIntegerValueField(cg, coregraphics.KCGMouseEventButtonNumber, int64(button))
	// Use the documented event number to correlate a down/up pair. The routing
	// helper's field 58 aliases the timestamp on the qualified macOS host.
	coregraphics.CGEventSetIntegerValueField(cg, coregraphics.KCGMouseEventNumber, event.Group)
	if cleanup {
		// Own-up may finish after cancellation or revoked approval. It must still
		// address the exact original process/window and captured geometry.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if event.Kind != "up" {
			return false, fmt.Errorf("cleanup only permits mouse-up")
		}
		if err := appstate.CheckScreenshotGeometry(ctx, root, info); err != nil {
			return false, err
		}
	} else {
		if err := guard(); err != nil {
			return false, err
		}
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	start, err := nativeProcessStart(target.App.PID)
	if err != nil {
		return false, err
	}
	if start != target.Start {
		return false, fmt.Errorf("native process changed before pointer post")
	}
	// CGEvent timestamps are nanoseconds since startup. Synthetic mouse events
	// otherwise arrive at AppKit with the helper's small group counter as time.
	var now unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_UPTIME_RAW, &now); err != nil {
		return false, fmt.Errorf("pointer timestamp: %w", err)
	}
	coregraphics.CGEventSetTimestamp(cg, coregraphics.CGEventTimestamp(now.Nano()))
	// This final check/post pair is not atomic with process exit or window changes.
	coregraphics.CGEventPostToPid(int32(target.App.PID), cg)
	return true, nil
}
