package main

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/screen"
	"github.com/tmc/axmcp/internal/simctl"
	"github.com/tmc/axmcp/internal/ui"
)

// Device Action
type DeviceActionInput struct {
	Action string `json:"action" description:"Action to perform (home, volume_up, volume_down, lock)"`
}

func registerDeviceTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "device_act",
		Description: "Perform device hardware actions. Supports: home, volume_up, volume_down, lock, shake",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args DeviceActionInput) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		// Use AppleScript via simctl helper
		if err := simctl.TriggerSimulatorAction(args.Action); err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, SimulatorActionOutput{}, nil
		}
		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: "Action performed: " + args.Action}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "device_biometry",
		Description: "Control device biometry. Actions: match, fail, enroll. Example: device_biometry(action='match')",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		Action string `json:"action" description:"Action (match, fail, enroll)"`
	}) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		action := "biometry_" + args.Action
		if err := simctl.TriggerSimulatorAction(action); err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, SimulatorActionOutput{}, nil
		}
		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: "Biometry action performed: " + args.Action}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "device_location",
		Description: "Set device location. Example: device_location(lat=37.7749, lon=-122.4194)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		Lat  float64 `json:"lat"`
		Lon  float64 `json:"lon"`
		UDID string  `json:"udid,omitempty"`
	}) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		udid := args.UDID
		if udid == "" {
			udid = "booted"
		}
		if err := simctl.SetLocation(ctx, udid, args.Lat, args.Lon); err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, SimulatorActionOutput{}, nil
		}
		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: fmt.Sprintf("Location set to %f, %f", args.Lat, args.Lon)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "device_privacy",
		Description: "Manage privacy permissions. Example: device_privacy(action='grant', service='photos', bundle_id='com.example.app')",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		Action   string `json:"action" description:"grant, revoke, reset"`
		Service  string `json:"service" description:"all, calendar, contacts, photos, location, etc."`
		BundleID string `json:"bundle_id" description:"Target app bundle ID"`
		UDID     string `json:"udid,omitempty"`
	}) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		udid := args.UDID
		if udid == "" {
			udid = "booted"
		}
		if err := simctl.SetPrivacy(ctx, udid, args.Action, args.Service, args.BundleID); err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, SimulatorActionOutput{}, nil
		}
		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: fmt.Sprintf("Privacy %s %s for %s", args.Action, args.Service, args.BundleID)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "device_orientation",
		Description: "Set device orientation (portrait, landscape)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		Orientation string `json:"orientation" description:"Orientation (portrait, landscape, landscape-left, landscape-right, upside-down)"`
	}) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		val := 1
		switch args.Orientation {
		case "portrait":
			val = 1
		case "landscape":
			val = 3
		case "landscape-left":
			val = 3
		case "landscape-right":
			val = 4
		case "upside-down":
			val = 2
		}
		ui.SharedDevice().SetOrientation(val)
		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: "Set orientation to " + args.Orientation}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "device_appearance",
		Description: "Set device appearance (light, dark)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		Appearance string `json:"appearance" description:"Appearance (light, dark)"`
	}) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		if err := simctl.SetAppearance(ctx, "booted", args.Appearance); err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, SimulatorActionOutput{}, nil
		}
		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: "Set appearance to " + args.Appearance}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "screen_shot",
		Description: "Take a screenshot of the booted simulator",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, ScreenshotOutput, error) {
		data, err := screen.CaptureSimulator(ctx, "booted")
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, ScreenshotOutput{}, nil
		}

		return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "Screenshot of the booted simulator:"},
					&mcp.ImageContent{
						Data:     data,
						MIMEType: "image/png",
					},
				},
			}, ScreenshotOutput{
				Message:  "Screenshot captured",
				MIMEType: "image/png",
				// Data field omitted to save tokens
			}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_device_orientation",
		Description: "Get device orientation",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, map[string]interface{}, error) {
		val, err := simctl.GetOrientation(ctx, "booted")
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
		}
		return &mcp.CallToolResult{}, map[string]interface{}{"orientation": val}, nil
	})
}
