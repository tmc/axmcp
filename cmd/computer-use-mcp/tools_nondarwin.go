//go:build !darwin

package main

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/computeruse"
)

type listAppsOutput struct {
	Apps []computeruse.AppInfo `json:"apps"`
}

func registerComputerUseTools(s *mcp.Server, rt *runtimeState) {
	registerListApps(s, rt)
	registerGetAppState(s, rt)
	registerSetRecording(s, rt)
	registerReplayTrajectory(s, rt)
	registerUnsupportedActionTools(s, rt)
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

func registerUnsupportedActionTools(s *mcp.Server, rt *runtimeState) {
	unsupported := map[string]bool{
		"click":                    true,
		"perform_secondary_action": true,
		"set_value":                true,
		"scroll":                   true,
		"drag":                     true,
		"press_key":                true,
		"type_text":                true,
		"evaluate_javascript":      true,
		"evaluate_cdp_javascript":  true,
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

func cloneTool(tool *mcp.Tool) *mcp.Tool {
	clone := *tool
	return &clone
}
