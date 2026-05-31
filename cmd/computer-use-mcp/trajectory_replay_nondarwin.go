//go:build !darwin

package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tmc/axmcp/internal/computeruse"
)

func (rt *runtimeState) replayTrajectoryStep(ctx context.Context, step trajectoryStep) (any, error) {
	switch step.Tool {
	case "click":
		var args clickInput
		if err := decodeStepArgs(step, &args); err != nil {
			return nil, err
		}
		return rt.replayClick(ctx, args)
	case "perform_secondary_action":
		var args performSecondaryActionInput
		if err := decodeStepArgs(step, &args); err != nil {
			return nil, err
		}
		return rt.replayPerformSecondaryAction(ctx, args)
	case "set_value":
		var args setValueInput
		if err := decodeStepArgs(step, &args); err != nil {
			return nil, err
		}
		return rt.replaySetValue(ctx, args)
	case "scroll":
		var args scrollInput
		if err := decodeStepArgs(step, &args); err != nil {
			return nil, err
		}
		return rt.replayScroll(ctx, args)
	case "drag":
		var args dragInput
		if err := decodeStepArgs(step, &args); err != nil {
			return nil, err
		}
		return rt.replayDrag(ctx, args)
	case "press_key":
		var args pressKeyInput
		if err := decodeStepArgs(step, &args); err != nil {
			return nil, err
		}
		return rt.replayPressKey(ctx, args)
	case "type_text":
		var args typeTextInput
		if err := decodeStepArgs(step, &args); err != nil {
			return nil, err
		}
		return rt.replayTypeText(ctx, args)
	case "evaluate_javascript", "evaluate_cdp_javascript":
		return nil, computeruse.PlatformUnsupported(step.Tool)
	default:
		return nil, fmt.Errorf("unsupported recorded tool %q", step.Tool)
	}
}

func decodeStepArgs(step trajectoryStep, out any) error {
	data, err := json.Marshal(step.Args)
	if err != nil {
		return fmt.Errorf("marshal args: %w", err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("unmarshal args: %w", err)
	}
	return nil
}

func (rt *runtimeState) bindReplayState(ctx context.Context, app string) (computeruse.AppState, error) {
	if rt == nil || rt.sessions == nil {
		return computeruse.AppState{}, fmt.Errorf("runtime is missing session store")
	}
	snapshot, err := rt.stateBackend().BuildState(ctx, computeruse.StateRequest{
		App:          app,
		Instructions: rt.instructions,
	})
	if err != nil {
		return computeruse.AppState{}, err
	}
	return rt.bindSnapshot(snapshot)
}

func (rt *runtimeState) replayClick(ctx context.Context, args clickInput) (computeruse.ActionResult, error) {
	state, err := rt.bindReplayState(ctx, args.App)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	args.StateID = state.StateID
	snapshot, err := rt.snapshotForAction(state.StateID)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	clickCount := args.ClickCount
	if clickCount <= 0 {
		clickCount = 1
	}
	if args.ElementIndex != nil {
		index, err := parseElementIndex(*args.ElementIndex)
		if err != nil {
			return computeruse.ActionResult{}, err
		}
		node, err := rt.sessions.Resolve(args.StateID, index)
		if err != nil {
			return computeruse.ActionResult{}, err
		}
		opts := computeruse.ClickOptions{
			Button:        args.MouseButton,
			ClickCount:    clickCount,
			ForegroundHID: args.ForegroundHID,
		}
		if err := rt.inputBackend().ClickElement(ctx, snapshot, index, opts); err != nil {
			return computeruse.ActionResult{}, err
		}
		return computeruse.ActionResult{SessionID: state.SessionID, StateID: state.StateID, Action: "click", Target: formatNode(node), Message: fmt.Sprintf("clicked %s", formatNode(node))}, nil
	}
	if args.X == nil || args.Y == nil {
		return computeruse.ActionResult{}, missingCoordinatesError()
	}
	x := roundCoordinate(*args.X)
	y := roundCoordinate(*args.Y)
	opts := computeruse.ClickOptions{
		Button:        args.MouseButton,
		ClickCount:    clickCount,
		ForegroundHID: args.ForegroundHID,
	}
	if err := rt.inputBackend().ClickPoint(ctx, snapshot, computeruse.Point{X: x, Y: y}, opts); err != nil {
		return computeruse.ActionResult{}, err
	}
	message := fmt.Sprintf("clicked pixel %d,%d", x, y)
	if args.ForegroundHID {
		message = fmt.Sprintf("clicked pixel %d,%d using foreground HID fallback", x, y)
	}
	return computeruse.ActionResult{SessionID: state.SessionID, StateID: state.StateID, Action: "click", Target: fmt.Sprintf("pixel %d,%d", x, y), Message: message}, nil
}

func (rt *runtimeState) replayPerformSecondaryAction(ctx context.Context, args performSecondaryActionInput) (computeruse.ActionResult, error) {
	state, err := rt.bindReplayState(ctx, args.App)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	snapshot, err := rt.snapshotForAction(state.StateID)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	index, err := parseElementIndex(args.ElementIndex)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	node, err := rt.sessions.Resolve(state.StateID, index)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	if err := rt.inputBackend().PerformSecondaryAction(ctx, snapshot, index, args.Action); err != nil {
		return computeruse.ActionResult{}, err
	}
	return computeruse.ActionResult{SessionID: state.SessionID, StateID: state.StateID, Action: args.Action, Target: formatNode(node), Message: fmt.Sprintf("performed %s on %s", args.Action, formatNode(node))}, nil
}

func (rt *runtimeState) replaySetValue(ctx context.Context, args setValueInput) (computeruse.ActionResult, error) {
	state, err := rt.bindReplayState(ctx, args.App)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	snapshot, err := rt.snapshotForAction(state.StateID)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	index, err := parseElementIndex(args.ElementIndex)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	node, err := rt.sessions.Resolve(state.StateID, index)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	if err := rt.inputBackend().SetValue(ctx, snapshot, index, args.Value); err != nil {
		return computeruse.ActionResult{}, err
	}
	return computeruse.ActionResult{SessionID: state.SessionID, StateID: state.StateID, Action: "set_value", Target: formatNode(node), Message: fmt.Sprintf("set value on %s", formatNode(node))}, nil
}

func (rt *runtimeState) replayScroll(ctx context.Context, args scrollInput) (computeruse.ActionResult, error) {
	state, err := rt.bindReplayState(ctx, args.App)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	snapshot, err := rt.snapshotForAction(state.StateID)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	index, err := parseElementIndex(args.ElementIndex)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	node, err := rt.sessions.Resolve(state.StateID, index)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	opts := computeruse.ScrollOptions{Direction: args.Direction, Pages: args.Pages}
	if err := rt.inputBackend().ScrollElement(ctx, snapshot, index, opts); err != nil {
		return computeruse.ActionResult{}, err
	}
	return computeruse.ActionResult{SessionID: state.SessionID, StateID: state.StateID, Action: "scroll", Target: formatNode(node), Message: fmt.Sprintf("scrolled %s %s", formatNode(node), args.Direction)}, nil
}

func (rt *runtimeState) replayDrag(ctx context.Context, args dragInput) (computeruse.ActionResult, error) {
	state, err := rt.bindReplayState(ctx, args.App)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	snapshot, err := rt.snapshotForAction(state.StateID)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	startX := roundCoordinate(args.FromX)
	startY := roundCoordinate(args.FromY)
	endX := roundCoordinate(args.ToX)
	endY := roundCoordinate(args.ToY)
	start := computeruse.Point{X: startX, Y: startY}
	end := computeruse.Point{X: endX, Y: endY}
	if err := rt.inputBackend().Drag(ctx, snapshot, start, end, computeruse.DragOptions{Button: "left"}); err != nil {
		return computeruse.ActionResult{}, err
	}
	return computeruse.ActionResult{SessionID: state.SessionID, StateID: state.StateID, Action: "drag", Target: fmt.Sprintf("%d,%d -> %d,%d", startX, startY, endX, endY), Message: fmt.Sprintf("dragged from %d,%d to %d,%d", startX, startY, endX, endY)}, nil
}

func (rt *runtimeState) replayPressKey(ctx context.Context, args pressKeyInput) (computeruse.ActionResult, error) {
	state, err := rt.bindReplayState(ctx, args.App)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	snapshot, err := rt.snapshotForAction(state.StateID)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	if err := rt.inputBackend().PressKey(ctx, snapshot, args.Key); err != nil {
		return computeruse.ActionResult{}, err
	}
	return computeruse.ActionResult{SessionID: state.SessionID, StateID: state.StateID, Action: "press_key", Target: args.Key, Message: fmt.Sprintf("pressed %s", args.Key)}, nil
}

func (rt *runtimeState) replayTypeText(ctx context.Context, args typeTextInput) (computeruse.ActionResult, error) {
	state, err := rt.bindReplayState(ctx, args.App)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	snapshot, err := rt.snapshotForAction(state.StateID)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	if args.ElementIndex != nil {
		index, err := parseElementIndex(*args.ElementIndex)
		if err != nil {
			return computeruse.ActionResult{}, err
		}
		node, err := rt.sessions.Resolve(state.StateID, index)
		if err != nil {
			return computeruse.ActionResult{}, err
		}
		if err := rt.inputBackend().TypeText(ctx, snapshot, &index, args.Text); err != nil {
			return computeruse.ActionResult{}, err
		}
		return computeruse.ActionResult{SessionID: state.SessionID, StateID: state.StateID, Action: "type_text", Target: formatNode(node), Message: fmt.Sprintf("typed into %s", formatNode(node))}, nil
	}
	if err := rt.inputBackend().TypeText(ctx, snapshot, nil, args.Text); err != nil {
		return computeruse.ActionResult{}, err
	}
	node := computeruse.ElementNode{Role: "focused element"}
	return computeruse.ActionResult{SessionID: state.SessionID, StateID: state.StateID, Action: "type_text", Target: formatNode(node), Message: fmt.Sprintf("typed into %s", formatNode(node))}, nil
}
