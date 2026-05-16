package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/input"
	"github.com/tmc/axmcp/internal/skylightinput"
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
	case "evaluate_javascript":
		var args evaluateJavascriptInput
		if err := decodeStepArgs(step, &args); err != nil {
			return nil, err
		}
		return rt.replayEvaluateJavascript(ctx, args)
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
	if rt.builder == nil || rt.sessions == nil {
		return computeruse.AppState{}, fmt.Errorf("runtime is missing app-state builder")
	}
	snapshot, err := rt.builder.Build(ctx, app, "", rt.instructions)
	if err != nil {
		return computeruse.AppState{}, err
	}
	state, err := rt.sessions.Bind(snapshot)
	if err != nil {
		_ = snapshot.Close()
		return computeruse.AppState{}, err
	}
	return state, nil
}

func (rt *runtimeState) replayClick(ctx context.Context, args clickInput) (computeruse.ActionResult, error) {
	state, err := rt.bindReplayState(ctx, args.App)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	args.StateID = state.StateID
	clickCount := args.ClickCount
	if clickCount <= 0 {
		clickCount = 1
	}
	if args.ElementIndex != nil {
		index, err := parseElementIndex(*args.ElementIndex)
		if err != nil {
			return computeruse.ActionResult{}, err
		}
		el, node, err := rt.sessions.Resolve(args.StateID, index)
		if err != nil {
			return computeruse.ActionResult{}, err
		}
		if err := input.ClickElement(el, args.MouseButton, clickCount); err != nil {
			return computeruse.ActionResult{}, err
		}
		return computeruse.ActionResult{SessionID: state.SessionID, StateID: state.StateID, Action: "click", Target: formatNode(node), Message: fmt.Sprintf("clicked %s", formatNode(node))}, nil
	}
	if args.X == nil || args.Y == nil {
		return computeruse.ActionResult{}, missingCoordinatesError()
	}
	x := roundCoordinate(*args.X)
	y := roundCoordinate(*args.Y)
	point, err := input.ScreenshotPointToWindowLocal(state.Window, x, y)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	if canUseSkyLightPixelClick(args.MouseButton, clickCount, state) {
		screen := skylightinput.Point{X: float64(state.Window.X + point.X), Y: float64(state.Window.Y + point.Y)}
		local := skylightinput.Point{X: float64(point.X), Y: float64(point.Y)}
		if err := skylightinput.MouseClick(int32(state.App.PID), screen, local, state.Window.WindowID, clickCount); err == nil {
			return computeruse.ActionResult{SessionID: state.SessionID, StateID: state.StateID, Action: "click", Target: fmt.Sprintf("pixel %d,%d", x, y), Message: fmt.Sprintf("clicked pixel %d,%d", x, y)}, nil
		}
	}
	root, _, err := rt.sessions.Resolve(args.StateID, 0)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	if err := input.ClickElementAt(root, point, args.MouseButton, clickCount); err != nil {
		return computeruse.ActionResult{}, err
	}
	return computeruse.ActionResult{SessionID: state.SessionID, StateID: state.StateID, Action: "click", Target: fmt.Sprintf("pixel %d,%d", x, y), Message: fmt.Sprintf("clicked pixel %d,%d", x, y)}, nil
}

func (rt *runtimeState) replayPerformSecondaryAction(ctx context.Context, args performSecondaryActionInput) (computeruse.ActionResult, error) {
	state, err := rt.bindReplayState(ctx, args.App)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	index, err := parseElementIndex(args.ElementIndex)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	el, node, err := rt.sessions.Resolve(state.StateID, index)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	if err := el.PerformAction(args.Action); err != nil {
		return computeruse.ActionResult{}, err
	}
	return computeruse.ActionResult{SessionID: state.SessionID, StateID: state.StateID, Action: args.Action, Target: formatNode(node), Message: fmt.Sprintf("performed %s on %s", args.Action, formatNode(node))}, nil
}

func (rt *runtimeState) replaySetValue(ctx context.Context, args setValueInput) (computeruse.ActionResult, error) {
	state, err := rt.bindReplayState(ctx, args.App)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	index, err := parseElementIndex(args.ElementIndex)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	el, node, err := rt.sessions.Resolve(state.StateID, index)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	if err := el.SetValue(args.Value); err != nil {
		return computeruse.ActionResult{}, err
	}
	return computeruse.ActionResult{SessionID: state.SessionID, StateID: state.StateID, Action: "set_value", Target: formatNode(node), Message: fmt.Sprintf("set value on %s", formatNode(node))}, nil
}

func (rt *runtimeState) replayScroll(ctx context.Context, args scrollInput) (computeruse.ActionResult, error) {
	state, err := rt.bindReplayState(ctx, args.App)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	index, err := parseElementIndex(args.ElementIndex)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	el, node, err := rt.sessions.Resolve(state.StateID, index)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	if err := input.ScrollElement(el, args.Direction, args.Pages); err != nil {
		return computeruse.ActionResult{}, err
	}
	return computeruse.ActionResult{SessionID: state.SessionID, StateID: state.StateID, Action: "scroll", Target: formatNode(node), Message: fmt.Sprintf("scrolled %s %s", formatNode(node), args.Direction)}, nil
}

func (rt *runtimeState) replayDrag(ctx context.Context, args dragInput) (computeruse.ActionResult, error) {
	state, err := rt.bindReplayState(ctx, args.App)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	root, _, err := rt.sessions.Resolve(state.StateID, 0)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	startX := roundCoordinate(args.FromX)
	startY := roundCoordinate(args.FromY)
	endX := roundCoordinate(args.ToX)
	endY := roundCoordinate(args.ToY)
	start, err := input.ScreenshotPointToWindowLocal(state.Window, startX, startY)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	end, err := input.ScreenshotPointToWindowLocal(state.Window, endX, endY)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	if err := input.DragElement(root, start, end, "left"); err != nil {
		return computeruse.ActionResult{}, err
	}
	return computeruse.ActionResult{SessionID: state.SessionID, StateID: state.StateID, Action: "drag", Target: fmt.Sprintf("%d,%d -> %d,%d", startX, startY, endX, endY), Message: fmt.Sprintf("dragged from %d,%d to %d,%d", startX, startY, endX, endY)}, nil
}

func (rt *runtimeState) replayPressKey(ctx context.Context, args pressKeyInput) (computeruse.ActionResult, error) {
	state, err := rt.bindReplayState(ctx, args.App)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	if err := input.SendKeyComboToPID(int32(state.App.PID), args.Key); err != nil {
		return computeruse.ActionResult{}, err
	}
	return computeruse.ActionResult{SessionID: state.SessionID, StateID: state.StateID, Action: "press_key", Target: args.Key, Message: fmt.Sprintf("pressed %s", args.Key)}, nil
}

func (rt *runtimeState) replayTypeText(ctx context.Context, args typeTextInput) (computeruse.ActionResult, error) {
	state, err := rt.bindReplayState(ctx, args.App)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	if args.ElementIndex != nil {
		index, err := parseElementIndex(*args.ElementIndex)
		if err != nil {
			return computeruse.ActionResult{}, err
		}
		el, node, err := rt.sessions.Resolve(state.StateID, index)
		if err != nil {
			return computeruse.ActionResult{}, err
		}
		endTypingCursor := beginTypingCursor(el)
		defer endTypingCursor()
		if err := el.TypeText(args.Text); err != nil {
			return computeruse.ActionResult{}, err
		}
		return computeruse.ActionResult{SessionID: state.SessionID, StateID: state.StateID, Action: "type_text", Target: formatNode(node), Message: fmt.Sprintf("typed into %s", formatNode(node))}, nil
	}
	root, _, err := rt.sessions.Resolve(state.StateID, 0)
	if err != nil {
		return computeruse.ActionResult{}, err
	}
	app := root.Application()
	if app == nil {
		return computeruse.ActionResult{}, fmt.Errorf("no active application for %q", args.App)
	}
	el := app.FocusedElement()
	if el == nil {
		return computeruse.ActionResult{}, fmt.Errorf("no focused element found")
	}
	endTypingCursor := beginTypingCursor(el)
	defer endTypingCursor()
	if err := el.TypeText(args.Text); err != nil {
		return computeruse.ActionResult{}, err
	}
	node := computeruse.ElementNode{Role: "AXUIElement", Title: "focused element"}
	return computeruse.ActionResult{SessionID: state.SessionID, StateID: state.StateID, Action: "type_text", Target: formatNode(node), Message: fmt.Sprintf("typed into %s", formatNode(node))}, nil
}

func (rt *runtimeState) replayEvaluateJavascript(ctx context.Context, args evaluateJavascriptInput) (evaluateJavascriptOutput, error) {
	state, err := rt.bindReplayState(ctx, args.App)
	if err != nil {
		return evaluateJavascriptOutput{}, err
	}
	result, err := evaluateJavascript(state.App, args.Script, args.WindowIndex, args.TabIndex)
	if err != nil {
		return evaluateJavascriptOutput{}, err
	}
	return evaluateJavascriptOutput{SessionID: state.SessionID, StateID: state.StateID, Action: "evaluate_javascript", Result: result}, nil
}
