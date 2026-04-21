package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/appstate"
	"github.com/tmc/axmcp/internal/computeruse/input"
	"github.com/tmc/axmcp/internal/ui"
)

type listAppsOutput struct {
	Apps []computeruse.AppInfo `json:"apps"`
}

func registerComputerUseTools(s *mcp.Server, rt *runtimeState) {
	registerListApps(s, rt)
	registerGetAppState(s, rt)
	registerClick(s, rt)
	registerPerformSecondaryAction(s, rt)
	registerScroll(s, rt)
	registerDrag(s, rt)
	registerTypeText(s, rt)
	registerPressKey(s, rt)
	registerSetValue(s, rt)
}

func registerListApps(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_apps",
		Description: "List running desktop apps available to the computer-use session.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ listAppsInput) (*mcp.CallToolResult, listAppsOutput, error) {
		apps, err := appstate.ListApps(ctx)
		if err != nil {
			return toolError(err), listAppsOutput{}, nil
		}
		return &mcp.CallToolResult{}, listAppsOutput{Apps: apps}, nil
	})
}

func registerGetAppState(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "get_app_state",
		Description: "Capture the current state of an app. This returns a screenshot, indexed AX tree, window metadata, and app-specific instructions. " +
			"Call this before sending action tools.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args getAppStateInput) (*mcp.CallToolResult, computeruse.AppState, error) {
		info, err := appstate.ResolveApp(ctx, args.App)
		if err != nil {
			return toolError(err), computeruse.AppState{}, nil
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
		if approval.Required {
			if !args.Approve {
				approval.Message = fmt.Sprintf("approval required for %s; call get_app_state again with approve=true", info.BundleID)
				state := computeruse.AppState{
					App:         info,
					Approval:    approval,
					Permissions: permissions,
				}
				return approvalRequiredResult(state), state, nil
			}
			approval, approvalErr = rt.approvals.Approve(info.BundleID, args.PersistApproval)
		}

		snapshot, err := rt.builder.Build(ctx, args.App, args.Window, rt.instructions)
		if err != nil {
			return toolError(err), computeruse.AppState{}, nil
		}
		state, err := rt.sessions.Bind(snapshot)
		if err != nil {
			return toolError(err), computeruse.AppState{}, nil
		}
		state.Approval = approval
		state.Permissions = permissions
		if approvalErr != nil {
			state.Approval.Message = fmt.Sprintf("%s (%v)", approval.Message, approvalErr)
			return textResult(state.Approval.Message), state, nil
		}
		return &mcp.CallToolResult{}, state, nil
	})
}

func registerClick(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "click",
		Description: "Click an indexed element or a pixel coordinate in the latest app screenshot. " +
			"Defaults to a single left click.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args clickInput) (*mcp.CallToolResult, computeruse.ActionResult, error) {
		state, ok := rt.sessions.Get(args.StateID)
		if !ok {
			err := fmt.Errorf("unknown or stale state_id %q; call get_app_state again", args.StateID)
			return toolError(err), computeruse.ActionResult{}, nil
		}
		clickCount := args.ClickCount
		if clickCount <= 0 {
			clickCount = 1
		}
		if args.ElementIndex != nil {
			el, node, err := rt.sessions.Resolve(args.StateID, *args.ElementIndex)
			if err != nil {
				return toolError(err), computeruse.ActionResult{}, nil
			}
			if err := input.ClickElement(el, args.Button, clickCount); err != nil {
				return toolError(err), computeruse.ActionResult{}, nil
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
			return toolError(missingCoordinatesError()), computeruse.ActionResult{}, nil
		}
		root, _, err := rt.sessions.Resolve(args.StateID, 0)
		if err != nil {
			return toolError(err), computeruse.ActionResult{}, nil
		}
		point, err := input.ScreenshotPointToWindowLocal(state.Window, *args.X, *args.Y)
		if err != nil {
			return toolError(err), computeruse.ActionResult{}, nil
		}
		if err := input.ClickElementAt(root, point, args.Button, clickCount); err != nil {
			return toolError(err), computeruse.ActionResult{}, nil
		}
		return &mcp.CallToolResult{}, computeruse.ActionResult{
			SessionID: state.SessionID,
			StateID:   state.StateID,
			Action:    "click",
			Target:    fmt.Sprintf("pixel %d,%d", *args.X, *args.Y),
			Message:   fmt.Sprintf("clicked pixel %d,%d", *args.X, *args.Y),
		}, nil
	})
}

func registerPerformSecondaryAction(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "perform_secondary_action",
		Description: "Perform a named AX action on an indexed element.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args performSecondaryActionInput) (*mcp.CallToolResult, computeruse.ActionResult, error) {
		state, ok := rt.sessions.Get(args.StateID)
		if !ok {
			err := fmt.Errorf("unknown or stale state_id %q; call get_app_state again", args.StateID)
			return toolError(err), computeruse.ActionResult{}, nil
		}
		el, node, err := rt.sessions.Resolve(args.StateID, args.ElementIndex)
		if err != nil {
			return toolError(err), computeruse.ActionResult{}, nil
		}
		if err := el.PerformAction(args.Action); err != nil {
			return toolError(err), computeruse.ActionResult{}, nil
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

func registerScroll(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "scroll",
		Description: "Scroll an indexed element or the active window by a number of pages.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args scrollInput) (*mcp.CallToolResult, computeruse.ActionResult, error) {
		state, ok := rt.sessions.Get(args.StateID)
		if !ok {
			err := fmt.Errorf("unknown or stale state_id %q; call get_app_state again", args.StateID)
			return toolError(err), computeruse.ActionResult{}, nil
		}
		targetIndex := 0
		targetLabel := "window"
		if args.ElementIndex != nil {
			targetIndex = *args.ElementIndex
		}
		el, node, err := rt.sessions.Resolve(args.StateID, targetIndex)
		if err != nil {
			return toolError(err), computeruse.ActionResult{}, nil
		}
		if targetIndex != 0 {
			targetLabel = formatNode(node)
		}
		if err := input.ScrollElement(el, args.Direction, args.Pages); err != nil {
			return toolError(err), computeruse.ActionResult{}, nil
		}
		return &mcp.CallToolResult{}, computeruse.ActionResult{
			SessionID: state.SessionID,
			StateID:   state.StateID,
			Action:    "scroll",
			Target:    targetLabel,
			Message:   fmt.Sprintf("scrolled %s %s", targetLabel, args.Direction),
		}, nil
	})
}

func registerDrag(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "drag",
		Description: "Drag from one screenshot pixel coordinate to another inside the active app window.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args dragInput) (*mcp.CallToolResult, computeruse.ActionResult, error) {
		state, ok := rt.sessions.Get(args.StateID)
		if !ok {
			err := fmt.Errorf("unknown or stale state_id %q; call get_app_state again", args.StateID)
			return toolError(err), computeruse.ActionResult{}, nil
		}
		root, _, err := rt.sessions.Resolve(args.StateID, 0)
		if err != nil {
			return toolError(err), computeruse.ActionResult{}, nil
		}
		start, err := input.ScreenshotPointToWindowLocal(state.Window, args.StartX, args.StartY)
		if err != nil {
			return toolError(err), computeruse.ActionResult{}, nil
		}
		end, err := input.ScreenshotPointToWindowLocal(state.Window, args.EndX, args.EndY)
		if err != nil {
			return toolError(err), computeruse.ActionResult{}, nil
		}
		if err := input.DragElement(root, start, end, args.Button); err != nil {
			return toolError(err), computeruse.ActionResult{}, nil
		}
		return &mcp.CallToolResult{}, computeruse.ActionResult{
			SessionID: state.SessionID,
			StateID:   state.StateID,
			Action:    "drag",
			Target:    fmt.Sprintf("%d,%d -> %d,%d", args.StartX, args.StartY, args.EndX, args.EndY),
			Message:   fmt.Sprintf("dragged from %d,%d to %d,%d", args.StartX, args.StartY, args.EndX, args.EndY),
		}, nil
	})
}

func registerTypeText(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "type_text",
		Description: "Type literal text into the focused element or a specific indexed element.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args typeTextInput) (*mcp.CallToolResult, computeruse.ActionResult, error) {
		state, ok := rt.sessions.Get(args.StateID)
		if !ok {
			err := fmt.Errorf("unknown or stale state_id %q; call get_app_state again", args.StateID)
			return toolError(err), computeruse.ActionResult{}, nil
		}
		var (
			el   *axuiautomation.Element
			node computeruse.ElementNode
			err  error
		)
		if args.ElementIndex != nil {
			el, node, err = rt.sessions.Resolve(args.StateID, *args.ElementIndex)
			if err != nil {
				return toolError(err), computeruse.ActionResult{}, nil
			}
			if err := el.Focus(); err != nil {
				return toolError(err), computeruse.ActionResult{}, nil
			}
		} else {
			root, _, err := rt.sessions.Resolve(args.StateID, 0)
			if err != nil {
				return toolError(err), computeruse.ActionResult{}, nil
			}
			app := root.Application()
			if app == nil {
				return toolError(fmt.Errorf("no active application for state %q", args.StateID)), computeruse.ActionResult{}, nil
			}
			el = app.FocusedElement()
			if el == nil {
				return toolError(fmt.Errorf("no focused element found")), computeruse.ActionResult{}, nil
			}
			node = computeruse.ElementNode{Title: "focused element"}
		}
		if err := el.TypeText(args.Text); err != nil {
			return toolError(err), computeruse.ActionResult{}, nil
		}
		return &mcp.CallToolResult{}, computeruse.ActionResult{
			SessionID: state.SessionID,
			StateID:   state.StateID,
			Action:    "type_text",
			Target:    formatNode(node),
			Message:   fmt.Sprintf("typed into %s", formatNode(node)),
		}, nil
	})
}

func registerPressKey(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "press_key",
		Description: "Press a key combo such as cmd+a, enter, escape, or command+shift+=.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args pressKeyInput) (*mcp.CallToolResult, computeruse.ActionResult, error) {
		state, ok := rt.sessions.Get(args.StateID)
		if !ok {
			err := fmt.Errorf("unknown or stale state_id %q; call get_app_state again", args.StateID)
			return toolError(err), computeruse.ActionResult{}, nil
		}
		if err := input.SendKeyCombo(args.Keys); err != nil {
			return toolError(err), computeruse.ActionResult{}, nil
		}
		return &mcp.CallToolResult{}, computeruse.ActionResult{
			SessionID: state.SessionID,
			StateID:   state.StateID,
			Action:    "press_key",
			Target:    args.Keys,
			Message:   fmt.Sprintf("pressed %s", args.Keys),
		}, nil
	})
}

func registerSetValue(s *mcp.Server, rt *runtimeState) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "set_value",
		Description: "Set the AX value of an indexed element when the element supports it.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args setValueInput) (*mcp.CallToolResult, computeruse.ActionResult, error) {
		state, ok := rt.sessions.Get(args.StateID)
		if !ok {
			err := fmt.Errorf("unknown or stale state_id %q; call get_app_state again", args.StateID)
			return toolError(err), computeruse.ActionResult{}, nil
		}
		el, node, err := rt.sessions.Resolve(args.StateID, args.ElementIndex)
		if err != nil {
			return toolError(err), computeruse.ActionResult{}, nil
		}
		if err := el.SetValue(args.Value); err != nil {
			return toolError(err), computeruse.ActionResult{}, nil
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

func currentPermissions() computeruse.PermissionState {
	accessibility := ui.IsTrusted()
	screenRecording := ui.IsScreenRecordingTrusted()
	state := computeruse.PermissionState{
		AccessibilityGranted:   accessibility,
		ScreenRecordingGranted: screenRecording,
	}
	if accessibility && screenRecording {
		return state
	}
	state.Pending = true
	var missing []string
	if !accessibility {
		missing = append(missing, "Accessibility")
	}
	if !screenRecording {
		missing = append(missing, "Screen Recording")
	}
	state.Message = fmt.Sprintf("permissions pending: grant %s and call get_app_state again", strings.Join(missing, " and "))
	return state
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
