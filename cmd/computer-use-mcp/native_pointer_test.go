package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/tmc/axmcp/internal/computeruse"
	"strings"
	"testing"
	"time"
)

func TestNativePointerClickSequence(t *testing.T) {
	for _, button := range []string{"left", "right", "middle"} {
		for count := 1; count <= 3; count++ {
			t.Run(fmt.Sprintf("%s/%d", button, count), func(t *testing.T) {
				var events []nativePointerEvent
				attempted, err := nativePointerSequence(context.Background(), nativeActInput{Action: "click", Point: &nativePoint{1.25, 2.5}, MouseButton: button, ClickCount: count}, func(_ context.Context, e nativePointerEvent, cleanup bool) (bool, error) {
					if cleanup != (e.Kind == "up") {
						t.Fatal("invalid cleanup mode")
					}
					events = append(events, e)
					return true, nil
				})
				if !attempted || err != nil || len(events) != 2*count {
					t.Fatalf("attempted=%v error=%v events=%v", attempted, err, events)
				}
				for i := 0; i < count; i++ {
					down, up := events[2*i], events[2*i+1]
					if down.Kind != "down" || up.Kind != "up" || down.Group != up.Group || down.Count != int64(i+1) || down.Count != up.Count || down.Button != button || down.Point != up.Point {
						t.Fatalf("bad pair: %+v %+v", down, up)
					}
					if i > 0 && down.Group == events[2*i-2].Group {
						t.Fatal("group reused")
					}
				}
			})
		}
	}
}

func TestNativePointerCleanup(t *testing.T) {
	for _, where := range []string{"before down", "after down", "after drag", "cleanup fails"} {
		t.Run(where, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if where == "before down" {
				cancel()
			}
			var sent []nativePointerEvent
			cleanupErr := errors.New("original process gone")
			attempted, err := nativePointerSequence(ctx, nativeActInput{Action: "drag", FromPoint: &nativePoint{1, 2}, ToPoint: &nativePoint{7, 8}, DurationMS: 100}, func(c context.Context, e nativePointerEvent, cleanup bool) (bool, error) {
				if cleanup {
					if c.Err() != nil {
						t.Fatal("cleanup context canceled")
					}
					if where == "cleanup fails" {
						return false, cleanupErr
					}
				} else if err := c.Err(); err != nil {
					return false, err
				}
				sent = append(sent, e)
				if (e.Kind == "down" && (where == "after down" || where == "cleanup fails")) || (e.Kind == "drag" && where == "after drag") {
					cancel()
				}
				return true, nil
			})
			if err == nil {
				t.Fatal("missing cancellation/error")
			}
			if where == "before down" {
				if attempted || len(sent) != 0 {
					t.Fatal("unowned event posted")
				}
				return
			}
			if !attempted {
				t.Fatal("lost attempted state")
			}
			if where == "cleanup fails" {
				if !errors.Is(err, cleanupErr) || !errors.Is(err, context.Canceled) {
					t.Fatalf("lost errors: %v", err)
				}
				return
			}
			last, previous := sent[len(sent)-1], sent[len(sent)-2]
			if last.Kind != "up" || last.Point != previous.Point || last.Group != previous.Group {
				t.Fatalf("cleanup retargeted: %v", sent)
			}
		})
	}
}

func TestNativePointerValidationConsumesState(t *testing.T) {
	for _, kind := range []string{"wrong image", "out of bounds", "mixed", "partial drag", "wrong button"} {
		t.Run(kind, func(t *testing.T) {
			r, b := newNativeTestRunner(t)
			b.afterCapture = func() {
				b.snapshots[len(b.snapshots)-1].state.ScreenshotMetadata = &computeruse.ScreenshotInfo{ImageID: "image", SourceKind: "window", TargetWindow: 7, Width: 20, Height: 20, GlobalRect: computeruse.CaptureRect{Width: 10, Height: 10}, ScaleX: 2, ScaleY: 2}
			}
			observed := nativeTestObserve(t, r)
			in := nativeActInput{StateID: observed.StateID, TargetID: observed.TargetID, Action: "click", Point: &nativePoint{1, 1}, ImageID: "image"}
			switch kind {
			case "wrong image":
				in.ImageID = "wrong"
			case "out of bounds":
				in.Point = &nativePoint{-1, 1}
			case "mixed":
				index := 1
				in.ElementIndex = &index
			case "partial drag":
				in.Action = "drag"
				in.Point = nil
				in.FromPoint = &nativePoint{}
			case "wrong button":
				in.MouseButton = "extra"
			}
			out := r.act(context.Background(), nil, in)
			if out.Execution != "not_dispatched" || b.calls != 0 || out.ErrorText == "" {
				t.Fatalf("invalid action=%+v calls=%d", out, b.calls)
			}
			if kind == "out of bounds" && !strings.Contains(out.ErrorText, "out of bounds") {
				t.Fatalf("wrong rejection: %s", out.ErrorText)
			}
			replay := r.act(context.Background(), nil, nativeTestClick(observed))
			if replay.Execution != "not_dispatched" || b.calls != 0 {
				t.Fatalf("invalid request did not consume token: %+v", replay)
			}
		})
	}
}

func TestNativeDragScheduleIncludesDispatchCost(t *testing.T) {
	current := time.Unix(0, 0)
	start := current
	waits := 0
	_, err := runNativePointerSequence(context.Background(), nativeActInput{Action: "drag", FromPoint: &nativePoint{1, 2}, ToPoint: &nativePoint{7, 8}, DurationMS: 100}, func(_ context.Context, _ nativePointerEvent, _ bool) (bool, error) {
		current = current.Add(10 * time.Millisecond)
		return true, nil
	}, func() time.Time { return current }, func(_ context.Context, deadline time.Time) error {
		waits++
		if deadline.After(current) {
			current = deadline
		}
		return nil
	})
	// Down costs10ms, then the100ms schedule, final move10ms and up10ms.
	if err != nil || waits != 6 || current.Sub(start) != 130*time.Millisecond {
		t.Fatalf("err=%v waits=%d elapsed=%s", err, waits, current.Sub(start))
	}
}
