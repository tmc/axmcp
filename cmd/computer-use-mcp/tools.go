package main

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/appstate"
	"github.com/tmc/axmcp/internal/computeruse/input"
	"github.com/tmc/axmcp/internal/ui/permissions"
)

type listAppsOutput struct {
	Apps []computeruse.AppInfo `json:"apps"`
}

func registerComputerUseTools(s *mcp.Server, rt *runtimeState) {
	registerListApps(s, rt)
	registerGetAppState(s, rt)
	registerClick(s, rt)
	registerPerformSecondaryAction(s, rt)
	registerSetValue(s, rt)
	registerScroll(s, rt)
	registerDrag(s, rt)
	registerPressKey(s, rt)
	registerTypeText(s, rt)
}

func registerListApps(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_apps",
		Description: "List the apps on this computer. Returns the set of apps that are currently running, as well as any that have been used in the last 14 days, including details on usage frequency",
		Annotations: readOnlyToolAnnotations(),
		InputSchema: exactObjectSchema(map[string]any{}),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ listAppsInput) (*mcp.CallToolResult, any, error) {
		apps, err := appstate.ListApps(ctx)
		if err != nil {
			return toolError(err), nil, nil
		}
		return &mcp.CallToolResult{}, listAppsOutput{Apps: apps}, nil
	})
}

func registerGetAppState(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_app_state",
		Description: "Start an app use session if needed, then get the state of the app's key window and return a screenshot and accessibility tree. This must be called once per assistant turn before interacting with the app",
		Annotations: readOnlyToolAnnotations(),
		InputSchema: exactObjectSchema(map[string]any{
			"app": stringProperty("App name or bundle identifier"),
		}, "app"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, args getAppStateInput) (*mcp.CallToolResult, any, error) {
		info, err := appstate.ResolveApp(ctx, args.App)
		if err != nil {
			return toolError(err), nil, nil
		}
		permissions := currentPermissions()
		approval := rt.approvals.Status(info.BundleID)
		if permissions.Pending {
			state := computeruse.AppState{
				App:         info,
				Approval:    approval,
				Permissions: permissions,
			}
			return textResult(permissions.Message), state, nil
		}
		var approvalErr error
		if approval.Required && !approval.Approved {
			approval, approvalErr = elicitApproval(ctx, req, rt, info)
			if approvalErr != nil && !approval.Approved {
				return toolError(approvalErr), nil, nil
			}
			if approval.Required && !approval.Approved {
				if approval.Message == "" {
					approval.Message = fmt.Sprintf("approval required for %s", info.BundleID)
				}
				state := computeruse.AppState{
					App:         info,
					Approval:    approval,
					Permissions: permissions,
				}
				return textResult(approval.Message), state, nil
			}
		}

		snapshot, err := rt.builder.Build(ctx, args.App, "", rt.instructions)
		if err != nil {
			return toolError(err), nil, nil
		}
		state, err := rt.sessions.Bind(snapshot)
		if err != nil {
			return toolError(err), nil, nil
		}
		state.Approval = approval
		state.Permissions = permissions
		if approvalErr != nil {
			state.Approval.Message = approvalErr.Error()
			return textResult(state.Approval.Message), state, nil
		}
		return &mcp.CallToolResult{}, state, nil
	})
}

func registerClick(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "click",
		Description: "Click an element by index or pixel coordinates from screenshot",
		Annotations: actionToolAnnotations(),
		InputSchema: exactObjectSchema(map[string]any{
			"app":           stringProperty("App name or bundle identifier"),
			"click_count":   integerProperty("Number of clicks. Defaults to 1"),
			"element_index": stringProperty("Element index to click"),
			"mouse_button":  enumStringProperty("Mouse button to click. Defaults to left.", "left", "right", "middle"),
			"state_id":      stringProperty("State token returned by get_app_state"),
			"x":             numberProperty("X coordinate in screenshot pixel coordinates"),
			"y":             numberProperty("Y coordinate in screenshot pixel coordinates"),
		}, "app", "state_id"),
	}, func(_ context.Context, _ *mcp.CallToolRequest, args clickInput) (*mcp.CallToolResult, any, error) {
		if res, payload, ok := actionBlockedForPermissions("click"); ok {
			return res, payload, nil
		}
		if res, payload, ok := actionBlockedForIntervention(rt, "click"); ok {
			return res, payload, nil
		}
		state, err := stateForAction(rt, "click", args.App, args.StateID)
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
			el, node, err := rt.sessions.Resolve(args.StateID, index)
			if err != nil {
				return staleStateResult("click", err)
			}
			if err := input.ClickElement(el, args.MouseButton, clickCount); err != nil {
				return toolError(err), nil, nil
			}
			return &mcp.CallToolResult{}, computeruse.ActionResult{
				SessionID: state.SessionID,
				StateID:   state.StateID,
				Action:    "click",
				Target:    formatNode(node),
				Message:   fmt.Sprintf("clicked %s", formatNode(node)),
			}, nil
		}
		if args.X == nil || args.Y == nil {
			return toolError(missingCoordinatesError()), nil, nil
		}
		root, _, err := rt.sessions.Resolve(args.StateID, 0)
		if err != nil {
			return staleStateResult("click", err)
		}
		x := roundCoordinate(*args.X)
		y := roundCoordinate(*args.Y)
		point, err := input.ScreenshotPointToWindowLocal(state.Window, x, y)
		if err != nil {
			return toolError(err), nil, nil
		}
		if err := input.ClickElementAt(root, point, args.MouseButton, clickCount); err != nil {
			return toolError(err), nil, nil
		}
		return &mcp.CallToolResult{}, computeruse.ActionResult{
			SessionID: state.SessionID,
			StateID:   state.StateID,
			Action:    "click",
			Target:    fmt.Sprintf("pixel %d,%d", x, y),
			Message:   fmt.Sprintf("clicked pixel %d,%d", x, y),
		}, nil
	})
}

func registerPerformSecondaryAction(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "perform_secondary_action",
		Description: "Invoke a secondary accessibility action exposed by an element",
		Annotations: actionToolAnnotations(),
		InputSchema: exactObjectSchema(map[string]any{
			"action":        stringProperty("Secondary accessibility action name"),
			"app":           stringProperty("App name or bundle identifier"),
			"element_index": stringProperty("Element identifier"),
			"state_id":      stringProperty("State token returned by get_app_state"),
		}, "app", "state_id", "element_index", "action"),
	}, func(_ context.Context, _ *mcp.CallToolRequest, args performSecondaryActionInput) (*mcp.CallToolResult, any, error) {
		if res, payload, ok := actionBlockedForPermissions(args.Action); ok {
			return res, payload, nil
		}
		if res, payload, ok := actionBlockedForIntervention(rt, args.Action); ok {
			return res, payload, nil
		}
		state, err := stateForAction(rt, args.Action, args.App, args.StateID)
		if err != nil {
			return staleStateResult(args.Action, err)
		}
		index, err := parseElementIndex(args.ElementIndex)
		if err != nil {
			return toolError(err), nil, nil
		}
		el, node, err := rt.sessions.Resolve(args.StateID, index)
		if err != nil {
			return staleStateResult(args.Action, err)
		}
		if err := el.PerformAction(args.Action); err != nil {
			return toolError(err), nil, nil
		}
		return &mcp.CallToolResult{}, computeruse.ActionResult{
			SessionID: state.SessionID,
			StateID:   state.StateID,
			Action:    args.Action,
			Target:    formatNode(node),
			Message:   fmt.Sprintf("performed %s on %s", args.Action, formatNode(node)),
		}, nil
	})
}

func registerSetValue(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "set_value",
		Description: "Set the value of a settable accessibility element",
		Annotations: actionToolAnnotations(),
		InputSchema: exactObjectSchema(map[string]any{
			"app":           stringProperty("App name or bundle identifier"),
			"element_index": stringProperty("Element identifier"),
			"state_id":      stringProperty("State token returned by get_app_state"),
			"value":         stringProperty("Value to assign"),
		}, "app", "state_id", "element_index", "value"),
	}, func(_ context.Context, _ *mcp.CallToolRequest, args setValueInput) (*mcp.CallToolResult, any, error) {
		if res, payload, ok := actionBlockedForPermissions("set_value"); ok {
			return res, payload, nil
		}
		if res, payload, ok := actionBlockedForIntervention(rt, "set_value"); ok {
			return res, payload, nil
		}
		state, err := stateForAction(rt, "set_value", args.App, args.StateID)
		if err != nil {
			return staleStateResult("set_value", err)
		}
		index, err := parseElementIndex(args.ElementIndex)
		if err != nil {
			return toolError(err), nil, nil
		}
		el, node, err := rt.sessions.Resolve(args.StateID, index)
		if err != nil {
			return staleStateResult("set_value", err)
		}
		if err := el.SetValue(args.Value); err != nil {
			return toolError(err), nil, nil
		}
		return &mcp.CallToolResult{}, computeruse.ActionResult{
			SessionID: state.SessionID,
			StateID:   state.StateID,
			Action:    "set_value",
			Target:    formatNode(node),
			Message:   fmt.Sprintf("set value on %s", formatNode(node)),
		}, nil
	})
}

func registerScroll(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "scroll",
		Description: "Scroll an element in a direction by a number of pages",
		Annotations: actionToolAnnotations(),
		InputSchema: exactObjectSchema(map[string]any{
			"app":           stringProperty("App name or bundle identifier"),
			"direction":     stringProperty("Scroll direction: up, down, left, or right"),
			"element_index": stringProperty("Element identifier"),
			"pages":         numberProperty("Number of pages to scroll. Fractional values are supported. Defaults to 1"),
			"state_id":      stringProperty("State token returned by get_app_state"),
		}, "app", "state_id", "element_index", "direction"),
	}, func(_ context.Context, _ *mcp.CallToolRequest, args scrollInput) (*mcp.CallToolResult, any, error) {
		if res, payload, ok := actionBlockedForPermissions("scroll"); ok {
			return res, payload, nil
		}
		if res, payload, ok := actionBlockedForIntervention(rt, "scroll"); ok {
			return res, payload, nil
		}
		state, err := stateForAction(rt, "scroll", args.App, args.StateID)
		if err != nil {
			return staleStateResult("scroll", err)
		}
		index, err := parseElementIndex(args.ElementIndex)
		if err != nil {
			return toolError(err), nil, nil
		}
		el, node, err := rt.sessions.Resolve(args.StateID, index)
		if err != nil {
			return staleStateResult("scroll", err)
		}
		if err := input.ScrollElement(el, args.Direction, args.Pages); err != nil {
			return toolError(err), nil, nil
		}
		return &mcp.CallToolResult{}, computeruse.ActionResult{
			SessionID: state.SessionID,
			StateID:   state.StateID,
			Action:    "scroll",
			Target:    formatNode(node),
			Message:   fmt.Sprintf("scrolled %s %s", formatNode(node), args.Direction),
		}, nil
	})
}

func registerDrag(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "drag",
		Description: "Drag from one point to another using pixel coordinates",
		Annotations: actionToolAnnotations(),
		InputSchema: exactObjectSchema(map[string]any{
			"app":      stringProperty("App name or bundle identifier"),
			"from_x":   numberProperty("Start X coordinate"),
			"from_y":   numberProperty("Start Y coordinate"),
			"state_id": stringProperty("State token returned by get_app_state"),
			"to_x":     numberProperty("End X coordinate"),
			"to_y":     numberProperty("End Y coordinate"),
		}, "app", "state_id", "from_x", "from_y", "to_x", "to_y"),
	}, func(_ context.Context, _ *mcp.CallToolRequest, args dragInput) (*mcp.CallToolResult, any, error) {
		if res, payload, ok := actionBlockedForPermissions("drag"); ok {
			return res, payload, nil
		}
		if res, payload, ok := actionBlockedForIntervention(rt, "drag"); ok {
			return res, payload, nil
		}
		state, err := stateForAction(rt, "drag", args.App, args.StateID)
		if err != nil {
			return staleStateResult("drag", err)
		}
		root, _, err := rt.sessions.Resolve(args.StateID, 0)
		if err != nil {
			return staleStateResult("drag", err)
		}
		startX := roundCoordinate(args.FromX)
		startY := roundCoordinate(args.FromY)
		endX := roundCoordinate(args.ToX)
		endY := roundCoordinate(args.ToY)
		start, err := input.ScreenshotPointToWindowLocal(state.Window, startX, startY)
		if err != nil {
			return toolError(err), nil, nil
		}
		end, err := input.ScreenshotPointToWindowLocal(state.Window, endX, endY)
		if err != nil {
			return toolError(err), nil, nil
		}
		if err := input.DragElement(root, start, end, "left"); err != nil {
			return toolError(err), nil, nil
		}
		return &mcp.CallToolResult{}, computeruse.ActionResult{
			SessionID: state.SessionID,
			StateID:   state.StateID,
			Action:    "drag",
			Target:    fmt.Sprintf("%d,%d -> %d,%d", startX, startY, endX, endY),
			Message:   fmt.Sprintf("dragged from %d,%d to %d,%d", startX, startY, endX, endY),
		}, nil
	})
}

func registerPressKey(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "press_key",
		Description: "Press a key or key-combination on the keyboard, including modifier and navigation keys.\n  - This supports xdotool's `key` syntax.\n  - Examples: \"a\", \"Return\", \"Tab\", \"super+c\", \"Up\", \"KP_0\" (for the numpad 0 key).",
		Annotations: actionToolAnnotations(),
		InputSchema: exactObjectSchema(map[string]any{
			"app":      stringProperty("App name or bundle identifier"),
			"key":      stringProperty("Key or key combination to press"),
			"state_id": stringProperty("State token returned by get_app_state"),
		}, "app", "state_id", "key"),
	}, func(_ context.Context, _ *mcp.CallToolRequest, args pressKeyInput) (*mcp.CallToolResult, any, error) {
		if res, payload, ok := actionBlockedForPermissions("press_key"); ok {
			return res, payload, nil
		}
		if res, payload, ok := actionBlockedForIntervention(rt, "press_key"); ok {
			return res, payload, nil
		}
		state, err := stateForAction(rt, "press_key", args.App, args.StateID)
		if err != nil {
			return staleStateResult("press_key", err)
		}
		if err := input.SendKeyComboToPID(int32(state.App.PID), args.Key); err != nil {
			return toolError(err), nil, nil
		}
		return &mcp.CallToolResult{}, computeruse.ActionResult{
			SessionID: state.SessionID,
			StateID:   state.StateID,
			Action:    "press_key",
			Target:    args.Key,
			Message:   fmt.Sprintf("pressed %s", args.Key),
		}, nil
	})
}

func registerTypeText(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "type_text",
		Description: "Type literal text using keyboard input",
		Annotations: actionToolAnnotations(),
		InputSchema: exactObjectSchema(map[string]any{
			"app":           stringProperty("App name or bundle identifier"),
			"element_index": stringProperty("Element index to type into. When omitted, the app's focused element is used."),
			"state_id":      stringProperty("State token returned by get_app_state"),
			"text":          stringProperty("Literal text to type"),
		}, "app", "state_id", "text"),
	}, func(_ context.Context, _ *mcp.CallToolRequest, args typeTextInput) (*mcp.CallToolResult, any, error) {
		if res, payload, ok := actionBlockedForPermissions("type_text"); ok {
			return res, payload, nil
		}
		if res, payload, ok := actionBlockedForIntervention(rt, "type_text"); ok {
			return res, payload, nil
		}
		state, err := stateForAction(rt, "type_text", args.App, args.StateID)
		if err != nil {
			return staleStateResult("type_text", err)
		}
		if args.ElementIndex != nil {
			index, err := parseElementIndex(*args.ElementIndex)
			if err != nil {
				return toolError(err), nil, nil
			}
			el, node, err := rt.sessions.Resolve(args.StateID, index)
			if err != nil {
				return staleStateResult("type_text", err)
			}
			endTypingCursor := beginTypingCursor(el)
			defer endTypingCursor()
			if err := el.TypeText(args.Text); err != nil {
				return toolError(err), nil, nil
			}
			return &mcp.CallToolResult{}, computeruse.ActionResult{
				SessionID: state.SessionID,
				StateID:   state.StateID,
				Action:    "type_text",
				Target:    formatNode(node),
				Message:   fmt.Sprintf("typed into %s", formatNode(node)),
			}, nil
		}
		root, _, err := rt.sessions.Resolve(args.StateID, 0)
		if err != nil {
			return staleStateResult("type_text", err)
		}
		app := root.Application()
		if app == nil {
			return toolError(fmt.Errorf("no active application for %q", args.App)), nil, nil
		}
		el := app.FocusedElement()
		if el == nil {
			return toolError(fmt.Errorf("no focused element found")), nil, nil
		}
		endTypingCursor := beginTypingCursor(el)
		defer endTypingCursor()
		if err := el.TypeText(args.Text); err != nil {
			return toolError(err), nil, nil
		}
		node := computeruse.ElementNode{Role: "AXUIElement", Title: "focused element"}
		return &mcp.CallToolResult{}, computeruse.ActionResult{
			SessionID: state.SessionID,
			StateID:   state.StateID,
			Action:    "type_text",
			Target:    formatNode(node),
			Message:   fmt.Sprintf("typed into %s", formatNode(node)),
		}, nil
	})
}

func readOnlyToolAnnotations() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		DestructiveHint: boolPtr(false),
		IdempotentHint:  true,
		OpenWorldHint:   boolPtr(false),
		ReadOnlyHint:    true,
	}
}

func actionToolAnnotations() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		DestructiveHint: boolPtr(false),
		IdempotentHint:  false,
		OpenWorldHint:   boolPtr(false),
		ReadOnlyHint:    false,
	}
}

func currentPermissions() computeruse.PermissionState {
	snapshot := permissions.CurrentSnapshot(permissions.ReqAccessibility, permissions.ReqScreenRecording)
	return computeruse.PermissionState{
		AccessibilityGranted:   snapshot.Accessibility == "granted",
		AccessibilityStatus:    snapshot.Accessibility,
		ScreenRecordingGranted: snapshot.ScreenRecording == "granted",
		ScreenRecordingStatus:  snapshot.ScreenRecording,
		Pending:                snapshot.Pending,
		Message:                snapshot.Message,
	}
}

func actionBlockedForPermissions(action string) (*mcp.CallToolResult, computeruse.ActionResult, bool) {
	perms := currentPermissions()
	if !perms.Pending {
		return nil, computeruse.ActionResult{}, false
	}
	return textResult(perms.Message), computeruse.ActionResult{
		Action:  action,
		Message: perms.Message,
	}, true
}

func actionBlockedForIntervention(rt *runtimeState, action string) (*mcp.CallToolResult, computeruse.ActionResult, bool) {
	if rt == nil || rt.intervention == nil {
		return nil, computeruse.ActionResult{}, false
	}
	status, blocked := rt.intervention.Blocked(time.Now())
	if !blocked {
		return nil, computeruse.ActionResult{}, false
	}
	wait := status.QuietPeriod - time.Since(status.LastInput)
	if wait < 0 {
		wait = 0
	}
	msg := fmt.Sprintf("physical user input detected recently (%s); wait %s and call get_app_state again", status.LastType, wait.Round(100*time.Millisecond))
	return toolError(fmt.Errorf("%s", msg)), computeruse.ActionResult{
		Action:          action,
		Message:         msg,
		RequiresRefresh: true,
	}, true
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

func elicitApproval(ctx context.Context, req *mcp.CallToolRequest, rt *runtimeState, info computeruse.AppInfo) (computeruse.ApprovalState, error) {
	if req == nil || req.Session == nil {
		return computeruse.ApprovalState{}, fmt.Errorf("approval required for %s but the client does not support elicitation", info.BundleID)
	}
	name := strings.TrimSpace(info.Name)
	if name == "" {
		name = info.BundleID
	}
	res, err := req.Session.Elicit(ctx, &mcp.ElicitParams{
		Meta:            mcp.Meta{"persist": []string{"always"}},
		Message:         fmt.Sprintf("Allow Codex to use %s?", name),
		RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	})
	if err != nil {
		return computeruse.ApprovalState{}, fmt.Errorf("request approval for %s: %w", info.BundleID, err)
	}
	decision, err := approvalDecisionFromElicit(res)
	if err != nil {
		return computeruse.ApprovalState{}, err
	}
	state, resolveErr := rt.approvals.Resolve(info.BundleID, decision)
	return state, resolveErr
}

func approvalDecisionFromElicit(res *mcp.ElicitResult) (computeruse.ApprovalDecision, error) {
	if res == nil {
		return "", fmt.Errorf("approval required but the client returned no decision")
	}
	switch strings.ToLower(strings.TrimSpace(res.Action)) {
	case "accept":
		return computeruse.ApprovalDecisionApprovePersistent, nil
	case "decline":
		return computeruse.ApprovalDecisionDeny, nil
	case "cancel":
		return computeruse.ApprovalDecisionCancel, nil
	default:
		return "", fmt.Errorf("unknown approval action %q", res.Action)
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

func missingAppStateError(app string) error {
	return fmt.Errorf("no current app state for %q; call get_app_state again", app)
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
		return computeruse.AppState{}, fmt.Errorf("unknown or stale state_id %q; call get_app_state again", stateID)
	}
	if !stateMatchesSelector(state, app) {
		return computeruse.AppState{}, fmt.Errorf("state_id %q belongs to %s, not %q; call get_app_state again", stateID, state.App.BundleID, app)
	}
	if rt.urlPolicy != nil {
		if err := rt.urlPolicy.CheckState(state); err != nil {
			return computeruse.AppState{}, err
		}
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

func requiresRefreshResult(action, app string) (*mcp.CallToolResult, any, error) {
	err := missingAppStateError(app)
	return toolError(err), computeruse.ActionResult{
		Action:          action,
		Message:         err.Error(),
		RequiresRefresh: true,
	}, nil
}

func roundCoordinate(v float64) int {
	return int(math.Round(v))
}

func boolPtr(v bool) *bool {
	return &v
}
