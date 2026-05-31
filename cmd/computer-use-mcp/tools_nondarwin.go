//go:build !darwin

package main

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/session"
)

type listAppsOutput struct {
	Apps []computeruse.AppInfo `json:"apps"`
}

func registerComputerUseTools(s *mcp.Server, rt *runtimeState) {
	registerListApps(s, rt)
	registerGetAppState(s, rt)
	registerSetRecording(s, rt)
	registerReplayTrajectory(s, rt)
	registerClick(s, rt)
	registerPerformSecondaryAction(s, rt)
	registerSetValue(s, rt)
	registerScroll(s, rt)
	registerDrag(s, rt)
	registerPressKey(s, rt)
	registerTypeText(s, rt)
	registerUnsupportedBrowserTools(s)
}

func registerListApps(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_apps",
		Description: "List the apps on this computer. Returns the set of apps that are currently running, as well as any that have been used in the last 14 days, including details on usage frequency",
		Annotations: readOnlyToolAnnotations(),
		InputSchema: exactObjectSchema(map[string]any{}),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ listAppsInput) (*mcp.CallToolResult, any, error) {
		apps, err := rt.stateBackend().ListApps(ctx)
		if err != nil {
			return toolError(err), nil, nil
		}
		return &mcp.CallToolResult{}, listAppsOutput{Apps: apps}, nil
	})
}

func registerGetAppState(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_app_state",
		Description: "Start an app use session if needed, then get the state of the app's key window. This must be called once per assistant turn before interacting with the app. capture_mode can be som, ax, or vision. Set omit_screenshot=true for compact automation logs that only need app/window metadata and element IDs.",
		Annotations: readOnlyToolAnnotations(),
		InputSchema: exactObjectSchema(map[string]any{
			"app":             stringProperty("App name or bundle identifier"),
			"capture_mode":    enumStringProperty("Capture response mode: som returns screenshot and AX tree, ax returns AX tree without screenshot, vision returns screenshot/window/app state without the AX tree. Defaults to som.", "som", "ax", "vision"),
			"omit_screenshot": booleanProperty("Omit screenshot_png_base64 from the returned state. The state_id remains valid for element-index actions; pixel-coordinate actions still require coordinates derived from a screenshot."),
		}, "app"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args getAppStateInput) (*mcp.CallToolResult, any, error) {
		mode, err := parseCaptureMode(args.CaptureMode)
		if err != nil {
			return toolError(err), nil, nil
		}
		snapshot, err := rt.stateBackend().BuildState(ctx, computeruse.StateRequest{
			App:          args.App,
			Instructions: rt.instructions,
		})
		if err != nil {
			return toolError(err), nil, nil
		}
		state, err := rt.bindSnapshot(snapshot)
		if err != nil {
			return toolError(err), nil, nil
		}
		return &mcp.CallToolResult{}, appStateResponse(state, mode, args.OmitScreenshot), nil
	})
}

func registerSetRecording(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "set_recording",
		Description: "Start or stop in-memory trajectory recording for subsequent action tools.",
		Annotations: actionToolAnnotations(),
		InputSchema: exactObjectSchema(map[string]any{
			"clear":   booleanProperty("Clear any previously recorded trajectory steps"),
			"enabled": booleanProperty("Whether trajectory recording should be enabled"),
		}, "enabled"),
	}, func(_ context.Context, _ *mcp.CallToolRequest, args setRecordingInput) (*mcp.CallToolResult, any, error) {
		if rt.recording == nil {
			rt.recording = newTrajectoryRecorder()
		}
		out := rt.recording.set(args.Enabled, args.Clear)
		return &mcp.CallToolResult{}, out, nil
	})
}

func registerReplayTrajectory(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "replay_trajectory",
		Description: "Replay the recorded trajectory. Use dry_run to inspect the recorded steps without executing them.",
		Annotations: actionToolAnnotations(),
		InputSchema: exactObjectSchema(map[string]any{
			"dry_run":   booleanProperty("Return recorded steps without executing them"),
			"from_step": integerProperty("First 1-based recorded step to replay. Defaults to 1"),
		}),
	}, func(_ context.Context, _ *mcp.CallToolRequest, args replayTrajectoryInput) (*mcp.CallToolResult, any, error) {
		if rt.recording == nil {
			return &mcp.CallToolResult{}, replayTrajectoryOutput{}, nil
		}
		fromStep := args.FromStep
		if fromStep <= 0 {
			fromStep = 1
		}
		steps := rt.recording.snapshot(fromStep)
		if args.DryRun {
			return &mcp.CallToolResult{}, replayTrajectoryOutput{Steps: steps}, nil
		}
		err := computeruse.PlatformUnsupported("replay trajectory")
		return toolError(err), replayTrajectoryOutput{Steps: steps}, nil
	})
}

func registerClick(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, cloneOrderedTool("click"), func(ctx context.Context, _ *mcp.CallToolRequest, args clickInput) (*mcp.CallToolResult, any, error) {
		state, err := stateForAction(rt, "click", args.App, args.StateID)
		if err != nil {
			return staleStateResult("click", err)
		}
		snapshot, err := rt.snapshotForAction(args.StateID)
		if err != nil {
			return staleStateResult("click", err)
		}
		clickCount := args.ClickCount
		if clickCount <= 0 {
			clickCount = 1
		}
		if args.ElementIndex != nil {
			index, err := parseElementIndex(*args.ElementIndex)
			if err != nil {
				return toolError(err), nil, nil
			}
			node, err := rt.sessions.Resolve(args.StateID, index)
			if err != nil {
				return staleStateResult("click", err)
			}
			opts := computeruse.ClickOptions{
				Button:        args.MouseButton,
				ClickCount:    clickCount,
				ForegroundHID: args.ForegroundHID,
			}
			if err := rt.inputBackend().ClickElement(ctx, snapshot, index, opts); err != nil {
				return toolError(err), nil, nil
			}
			out := computeruse.ActionResult{
				SessionID: state.SessionID,
				StateID:   state.StateID,
				Action:    "click",
				Target:    formatNode(node),
				Message:   fmt.Sprintf("clicked %s", formatNode(node)),
			}
			rt.recording.record("click", args, out)
			return &mcp.CallToolResult{}, out, nil
		}
		if args.X == nil || args.Y == nil {
			return toolError(missingCoordinatesError()), nil, nil
		}
		x := roundCoordinate(*args.X)
		y := roundCoordinate(*args.Y)
		opts := computeruse.ClickOptions{
			Button:        args.MouseButton,
			ClickCount:    clickCount,
			ForegroundHID: args.ForegroundHID,
		}
		if err := rt.inputBackend().ClickPoint(ctx, snapshot, computeruse.Point{X: x, Y: y}, opts); err != nil {
			return toolError(err), nil, nil
		}
		message := fmt.Sprintf("clicked pixel %d,%d", x, y)
		if args.ForegroundHID {
			message = fmt.Sprintf("clicked pixel %d,%d using foreground HID fallback", x, y)
		}
		out := computeruse.ActionResult{
			SessionID: state.SessionID,
			StateID:   state.StateID,
			Action:    "click",
			Target:    fmt.Sprintf("pixel %d,%d", x, y),
			Message:   message,
		}
		rt.recording.record("click", args, out)
		return &mcp.CallToolResult{}, out, nil
	})
}

func registerPerformSecondaryAction(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, cloneOrderedTool("perform_secondary_action"), func(ctx context.Context, _ *mcp.CallToolRequest, args performSecondaryActionInput) (*mcp.CallToolResult, any, error) {
		state, err := stateForAction(rt, args.Action, args.App, args.StateID)
		if err != nil {
			return staleStateResult(args.Action, err)
		}
		snapshot, err := rt.snapshotForAction(args.StateID)
		if err != nil {
			return staleStateResult(args.Action, err)
		}
		index, err := parseElementIndex(args.ElementIndex)
		if err != nil {
			return toolError(err), nil, nil
		}
		node, err := rt.sessions.Resolve(args.StateID, index)
		if err != nil {
			return staleStateResult(args.Action, err)
		}
		if err := rt.inputBackend().PerformSecondaryAction(ctx, snapshot, index, args.Action); err != nil {
			return toolError(err), nil, nil
		}
		out := computeruse.ActionResult{
			SessionID: state.SessionID,
			StateID:   state.StateID,
			Action:    args.Action,
			Target:    formatNode(node),
			Message:   fmt.Sprintf("performed %s on %s", args.Action, formatNode(node)),
		}
		rt.recording.record("perform_secondary_action", args, out)
		return &mcp.CallToolResult{}, out, nil
	})
}

func registerSetValue(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, cloneOrderedTool("set_value"), func(ctx context.Context, _ *mcp.CallToolRequest, args setValueInput) (*mcp.CallToolResult, any, error) {
		state, err := stateForAction(rt, "set_value", args.App, args.StateID)
		if err != nil {
			return staleStateResult("set_value", err)
		}
		snapshot, err := rt.snapshotForAction(args.StateID)
		if err != nil {
			return staleStateResult("set_value", err)
		}
		index, err := parseElementIndex(args.ElementIndex)
		if err != nil {
			return toolError(err), nil, nil
		}
		node, err := rt.sessions.Resolve(args.StateID, index)
		if err != nil {
			return staleStateResult("set_value", err)
		}
		if err := rt.inputBackend().SetValue(ctx, snapshot, index, args.Value); err != nil {
			return toolError(err), nil, nil
		}
		out := computeruse.ActionResult{
			SessionID: state.SessionID,
			StateID:   state.StateID,
			Action:    "set_value",
			Target:    formatNode(node),
			Message:   fmt.Sprintf("set value on %s", formatNode(node)),
		}
		rt.recording.record("set_value", args, out)
		return &mcp.CallToolResult{}, out, nil
	})
}

func registerScroll(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, cloneOrderedTool("scroll"), func(ctx context.Context, _ *mcp.CallToolRequest, args scrollInput) (*mcp.CallToolResult, any, error) {
		state, err := stateForAction(rt, "scroll", args.App, args.StateID)
		if err != nil {
			return staleStateResult("scroll", err)
		}
		snapshot, err := rt.snapshotForAction(args.StateID)
		if err != nil {
			return staleStateResult("scroll", err)
		}
		index, err := parseElementIndex(args.ElementIndex)
		if err != nil {
			return toolError(err), nil, nil
		}
		node, err := rt.sessions.Resolve(args.StateID, index)
		if err != nil {
			return staleStateResult("scroll", err)
		}
		opts := computeruse.ScrollOptions{Direction: args.Direction, Pages: args.Pages}
		if err := rt.inputBackend().ScrollElement(ctx, snapshot, index, opts); err != nil {
			return toolError(err), nil, nil
		}
		out := computeruse.ActionResult{
			SessionID: state.SessionID,
			StateID:   state.StateID,
			Action:    "scroll",
			Target:    formatNode(node),
			Message:   fmt.Sprintf("scrolled %s %s", formatNode(node), args.Direction),
		}
		rt.recording.record("scroll", args, out)
		return &mcp.CallToolResult{}, out, nil
	})
}

func registerDrag(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, cloneOrderedTool("drag"), func(ctx context.Context, _ *mcp.CallToolRequest, args dragInput) (*mcp.CallToolResult, any, error) {
		state, err := stateForAction(rt, "drag", args.App, args.StateID)
		if err != nil {
			return staleStateResult("drag", err)
		}
		snapshot, err := rt.snapshotForAction(args.StateID)
		if err != nil {
			return staleStateResult("drag", err)
		}
		startX := roundCoordinate(args.FromX)
		startY := roundCoordinate(args.FromY)
		endX := roundCoordinate(args.ToX)
		endY := roundCoordinate(args.ToY)
		start := computeruse.Point{X: startX, Y: startY}
		end := computeruse.Point{X: endX, Y: endY}
		if err := rt.inputBackend().Drag(ctx, snapshot, start, end, computeruse.DragOptions{Button: "left"}); err != nil {
			return toolError(err), nil, nil
		}
		out := computeruse.ActionResult{
			SessionID: state.SessionID,
			StateID:   state.StateID,
			Action:    "drag",
			Target:    fmt.Sprintf("%d,%d -> %d,%d", startX, startY, endX, endY),
			Message:   fmt.Sprintf("dragged from %d,%d to %d,%d", startX, startY, endX, endY),
		}
		rt.recording.record("drag", args, out)
		return &mcp.CallToolResult{}, out, nil
	})
}

func registerPressKey(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, cloneOrderedTool("press_key"), func(ctx context.Context, _ *mcp.CallToolRequest, args pressKeyInput) (*mcp.CallToolResult, any, error) {
		state, err := stateForAction(rt, "press_key", args.App, args.StateID)
		if err != nil {
			return staleStateResult("press_key", err)
		}
		snapshot, err := rt.snapshotForAction(args.StateID)
		if err != nil {
			return staleStateResult("press_key", err)
		}
		if err := rt.inputBackend().PressKey(ctx, snapshot, args.Key); err != nil {
			return toolError(err), nil, nil
		}
		out := computeruse.ActionResult{
			SessionID: state.SessionID,
			StateID:   state.StateID,
			Action:    "press_key",
			Target:    args.Key,
			Message:   fmt.Sprintf("pressed %s", args.Key),
		}
		rt.recording.record("press_key", args, out)
		return &mcp.CallToolResult{}, out, nil
	})
}

func registerTypeText(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, cloneOrderedTool("type_text"), func(ctx context.Context, _ *mcp.CallToolRequest, args typeTextInput) (*mcp.CallToolResult, any, error) {
		state, err := stateForAction(rt, "type_text", args.App, args.StateID)
		if err != nil {
			return staleStateResult("type_text", err)
		}
		snapshot, err := rt.snapshotForAction(args.StateID)
		if err != nil {
			return staleStateResult("type_text", err)
		}
		if args.ElementIndex != nil {
			index, err := parseElementIndex(*args.ElementIndex)
			if err != nil {
				return toolError(err), nil, nil
			}
			node, err := rt.sessions.Resolve(args.StateID, index)
			if err != nil {
				return staleStateResult("type_text", err)
			}
			if err := rt.inputBackend().TypeText(ctx, snapshot, &index, args.Text); err != nil {
				return toolError(err), nil, nil
			}
			out := computeruse.ActionResult{
				SessionID: state.SessionID,
				StateID:   state.StateID,
				Action:    "type_text",
				Target:    formatNode(node),
				Message:   fmt.Sprintf("typed into %s", formatNode(node)),
			}
			rt.recording.record("type_text", args, out)
			return &mcp.CallToolResult{}, out, nil
		}
		if err := rt.inputBackend().TypeText(ctx, snapshot, nil, args.Text); err != nil {
			return toolError(err), nil, nil
		}
		node := computeruse.ElementNode{Role: "focused element"}
		out := computeruse.ActionResult{
			SessionID: state.SessionID,
			StateID:   state.StateID,
			Action:    "type_text",
			Target:    formatNode(node),
			Message:   fmt.Sprintf("typed into %s", formatNode(node)),
		}
		rt.recording.record("type_text", args, out)
		return &mcp.CallToolResult{}, out, nil
	})
}

func registerUnsupportedBrowserTools(s *mcp.Server) {
	unsupported := map[string]bool{
		"evaluate_javascript":     true,
		"evaluate_cdp_javascript": true,
	}
	for _, tool := range orderedComputerUseTools() {
		if !unsupported[tool.Name] {
			continue
		}
		tool := cloneTool(tool)
		name := tool.Name
		mcp.AddTool(s, tool, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
			err := computeruse.PlatformUnsupported(name)
			return toolError(err), computeruse.ActionResult{
				Action:  name,
				Message: err.Error(),
			}, nil
		})
	}
}

func cloneOrderedTool(name string) *mcp.Tool {
	for _, tool := range orderedComputerUseTools() {
		if tool.Name == name {
			return cloneTool(tool)
		}
	}
	panic("missing ordered computer-use tool " + name)
}

func cloneTool(tool *mcp.Tool) *mcp.Tool {
	clone := *tool
	return &clone
}

func formatNode(node computeruse.ElementNode) string {
	switch {
	case node.Title != "":
		return fmt.Sprintf("%s %q", node.Role, node.Title)
	case node.Description != "":
		return fmt.Sprintf("%s %q", node.Role, node.Description)
	case node.Role != "":
		return node.Role
	default:
		return "element"
	}
}

func parseElementIndex(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("element_index is required")
	}
	index, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid element_index %q", raw)
	}
	return index, nil
}

func stateForAction(rt *runtimeState, action, app, stateID string) (computeruse.AppState, error) {
	stateID = strings.TrimSpace(stateID)
	if stateID == "" {
		return computeruse.AppState{}, fmt.Errorf("%s requires state_id from get_app_state; call get_app_state again", action)
	}
	if rt == nil || rt.sessions == nil {
		return computeruse.AppState{}, fmt.Errorf("%s has no session store; call get_app_state again", action)
	}
	state, ok := rt.sessions.Get(stateID)
	if !ok {
		return computeruse.AppState{}, session.StaleStateError(stateID)
	}
	if !stateMatchesSelector(state, app) {
		name := strings.TrimSpace(state.App.Name)
		if name == "" {
			name = state.App.BundleID
		}
		return computeruse.AppState{}, fmt.Errorf("state_id %q belongs to %s, not %q; call get_app_state again and retry with the fresh state_id", stateID, name, app)
	}
	return state, nil
}

func stateMatchesSelector(state computeruse.AppState, selector string) bool {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return false
	}
	app := state.App
	if fmt.Sprintf("%d", app.PID) == selector {
		return true
	}
	want := strings.ToLower(selector)
	return strings.EqualFold(app.BundleID, selector) ||
		strings.EqualFold(app.Name, selector) ||
		strings.Contains(strings.ToLower(app.BundleID), want) ||
		strings.Contains(strings.ToLower(app.Name), want)
}

func staleStateResult(action string, err error) (*mcp.CallToolResult, any, error) {
	return toolError(err), computeruse.ActionResult{
		Action:          action,
		Message:         err.Error(),
		RequiresRefresh: true,
	}, nil
}

func roundCoordinate(v float64) int {
	return int(math.Round(v))
}
