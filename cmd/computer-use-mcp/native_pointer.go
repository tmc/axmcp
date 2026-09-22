package main

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

// nativePointerEvent uses image pixels. Only the OS adapter converts the point
// to window-local and global logical coordinates immediately before routing.
type nativePointerEvent struct {
	Kind         string
	Point        nativePoint
	Button       string
	Count, Group int64
}

var nativePointerGroup atomic.Int64

// nativePointerSequence owns only the downs its post callback reports as sent.
// Cleanup releases that button at its last posted point, through the callback's
// same-instance path. It never posts an unrelated blanket mouse-up.
func nativePointerSequence(ctx context.Context, in nativeActInput, post func(context.Context, nativePointerEvent, bool) (bool, error)) (bool, error) {
	return runNativePointerSequence(ctx, in, post, time.Now, waitNativePointer)
}

func waitNativePointer(ctx context.Context, deadline time.Time) error {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func runNativePointerSequence(ctx context.Context, in nativeActInput, post func(context.Context, nativePointerEvent, bool) (bool, error), now func() time.Time, wait func(context.Context, time.Time) error) (attempted bool, err error) {
	button := in.MouseButton
	if button == "" {
		button = "left"
	}
	var held *nativePointerEvent
	defer func() {
		if held != nil {
			up := *held
			up.Kind = "up"
			_, upErr := post(context.WithoutCancel(ctx), up, true)
			err = errors.Join(err, upErr)
		}
	}()
	send := func(event nativePointerEvent, cleanup bool) error {
		eventCtx := ctx
		if cleanup {
			eventCtx = context.WithoutCancel(ctx)
		}
		sent, sendErr := post(eventCtx, event, cleanup)
		if !sent && sendErr == nil {
			sendErr = fmt.Errorf("pointer event was not posted")
		}
		attempted = attempted || sent
		if sent {
			switch event.Kind {
			case "down", "drag":
				copy := event
				held = &copy
			case "up":
				held = nil
			}
		}
		return sendErr
	}
	if in.Action == "click" {
		count := in.ClickCount
		if count == 0 {
			count = 1
		}
		for i := 1; i <= count; i++ {
			event := nativePointerEvent{Kind: "down", Point: *in.Point, Button: button, Count: int64(i), Group: nativePointerGroup.Add(1)}
			if err := send(event, false); err != nil {
				return attempted, err
			}
			event.Kind = "up"
			if err := send(event, true); err != nil {
				return attempted, err
			}
		}
		return attempted, nil
	}
	duration := time.Duration(in.DurationMS) * time.Millisecond
	if duration == 0 {
		duration = 300 * time.Millisecond
	}
	event := nativePointerEvent{Kind: "down", Point: *in.FromPoint, Button: button, Count: 1, Group: nativePointerGroup.Add(1)}
	if err := send(event, false); err != nil {
		return attempted, err
	}
	steps := int(duration / (16 * time.Millisecond))
	if steps < 1 {
		steps = 1
	}
	started := now()
	for i := 1; i <= steps; i++ {
		// Include validation and dispatch costs in the schedule instead of adding
		// them to every interval. Slow checks can still exceed the requested time.
		deadline := started.Add(duration * time.Duration(i) / time.Duration(steps))
		if err := wait(ctx, deadline); err != nil {
			return attempted, err
		}

		fraction := float64(i) / float64(steps)
		event.Kind = "drag"
		event.Point = nativePoint{X: in.FromPoint.X + (in.ToPoint.X-in.FromPoint.X)*fraction, Y: in.FromPoint.Y + (in.ToPoint.Y-in.FromPoint.Y)*fraction}
		if err := send(event, false); err != nil {
			return attempted, err
		}
	}
	event.Kind = "up"
	if err := send(event, true); err != nil {
		return attempted, err
	}
	return attempted, nil
}
