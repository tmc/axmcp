package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/purego/coresim"
	"github.com/tmc/axmcp/internal/ui"
)

// UI Interaction
type UITargetInput struct {
	ID       string  `json:"id,omitempty"`
	Label    string  `json:"label,omitempty"`
	X        float64 `json:"x,omitempty" description:"X coordinate (for coordinate-based tap)"`
	Y        float64 `json:"y,omitempty" description:"Y coordinate (for coordinate-based tap)"`
	UDID     string  `json:"udid,omitempty" description:"Simulator UDID (optional)"`
	Platform string  `json:"platform,omitempty" description:"Target platform: 'mac' or 'ios' (default: auto)"`
}

func registerUITools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "ui_tap",
		Description: "Tap a macOS element by id or label, or tap iOS simulator coordinates. Examples: ui_tap(id='login'), ui_tap(udid='booted', x=100, y=200)",
	}, SafeTool("ui_tap", func(ctx context.Context, req *mcp.CallToolRequest, args UITargetInput) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		isIOS := args.Platform == "ios" || args.UDID != ""

		if isIOS {
			udid := args.UDID
			if udid == "" {
				udid = "booted"
			}
			device, ok := coresim.FindDeviceByUDID(udid)
			if !ok {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Device not found"}}}, SimulatorActionOutput{}, nil
			}

			// Coordinate Tap
			if args.X != 0 || args.Y != 0 {
				err := device.Tap(args.X, args.Y)
				if err != nil {
					return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("iOS Tap failed: %v", err)}}}, SimulatorActionOutput{}, nil
				}
				return &mcp.CallToolResult{}, SimulatorActionOutput{Message: fmt.Sprintf("Tapped at %.1f, %.1f on %s", args.X, args.Y, udid)}, nil
			}

			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "iOS Tap by ID not yet supported (requires deep tree traversal). Use coordinates (X, Y)."}}}, SimulatorActionOutput{}, nil
		}

		if args.X != 0 || args.Y != 0 {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "macOS coordinate taps are not supported; use id or label"}},
			}, SimulatorActionOutput{}, nil
		}
		if args.ID == "" && args.Label == "" {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "macOS taps require id or label"}},
			}, SimulatorActionOutput{}, nil
		}

		app := ui.Application()
		if !app.Exists() {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Simulator app not found"}}}, SimulatorActionOutput{}, nil
		}
		root := app.Element()
		if root == nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Simulator element not accessible"}}}, SimulatorActionOutput{}, nil
		}

		var el *ui.Element
		if args.ID != "" {
			el = ui.ElementByID(args.ID)
			if el == nil {
				matches := root.Query(ui.QueryParams{Label: args.ID})
				if len(matches) > 0 {
					el = matches[0]
				}
			}
			if el == nil {
				matches := root.Query(ui.QueryParams{Title: args.ID})
				if len(matches) > 0 {
					el = matches[0]
				}
			}
		}
		if el == nil && args.Label != "" {
			matches := root.Query(ui.QueryParams{Label: args.Label})
			if len(matches) > 0 {
				el = matches[0]
			}
		}
		if el == nil {
			target := args.ID
			if target == "" {
				target = args.Label
			}
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Element '%s' not found", target)}}}, SimulatorActionOutput{}, nil
		}
		el.Tap()
		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: "Tapped"}, nil
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "ui_tree",
		Description: "Get UI hierarchy debug description. Example: ui_tree(bundle_id='com.apple.finder')",
	}, SafeTool("ui_tree", func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		BundleID string `json:"bundle_id,omitempty"`
	}) (*mcp.CallToolResult, UITreeOutput, error) {
		bundleID := args.BundleID
		if bundleID == "" {
			bundleID = "com.apple.iphonesimulator"
		}
		app := ui.ApplicationWithBundleID(bundleID)
		return &mcp.CallToolResult{}, UITreeOutput{Tree: app.Tree()}, nil
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "ui_inspect",
		Description: "Inspect element attributes. Examples: ui_inspect(id='my_button'), ui_inspect()",
	}, SafeTool("ui_inspect", func(ctx context.Context, req *mcp.CallToolRequest, args UITargetInput) (*mcp.CallToolResult, map[string]interface{}, error) {
		// Re-using UITargetInput as it has optional ID
		var el *ui.Element
		if args.ID != "" {
			el = ui.ElementByID(args.ID)
		} else {
			el = ui.Application().Element()
		}

		if el == nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Element not found"}}}, nil, nil
		}

		attrs := el.Attributes()
		return &mcp.CallToolResult{}, map[string]interface{}{
			"label":      attrs.Label,
			"identifier": attrs.Identifier,
			"title":      attrs.Title,
			"value":      attrs.Value,
			"frame":      attrs.Frame,
			"enabled":    attrs.Enabled,
			"selected":   attrs.Selected,
			"has_focus":  attrs.HasFocus,
		}, nil
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "ui_wait",
		Description: "Wait for element to exist. Example: ui_wait(id='loaded_content', timeout=10.0)",
	}, SafeTool("ui_wait", func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		ID      string  `json:"id,omitempty"`
		Timeout float64 `json:"timeout"`
	}) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		if args.ID == "" {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "id is required"}}}, SimulatorActionOutput{}, nil
		}
		timeout := args.Timeout
		if timeout == 0 {
			timeout = 5.0
		}

		deadline := time.Now().Add(time.Duration(timeout * float64(time.Second)))
		for time.Now().Before(deadline) {
			el := ui.ElementByID(args.ID)
			if el != nil && el.Exists() {
				return &mcp.CallToolResult{}, SimulatorActionOutput{Message: "Element exists"}, nil
			}
			time.Sleep(200 * time.Millisecond)
		}
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Element did not appear within timeout"}}}, SimulatorActionOutput{}, nil
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "ui_list_windows",
		Description: "List windows of an application",
	}, SafeTool("ui_list_windows", func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		BundleID string `json:"bundle_id"`
	}) (*mcp.CallToolResult, map[string]interface{}, error) {
		app := ui.ApplicationWithBundleID(args.BundleID)
		windows := app.Element().Windows()
		var windowList []string
		for _, w := range windows {
			windowList = append(windowList, w.Title())
		}
		return &mcp.CallToolResult{}, map[string]interface{}{"windows": windowList}, nil
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "ui_list_buttons",
		Description: "List buttons in a window/element",
	}, SafeTool("ui_list_buttons", func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		ID string `json:"id"`
	}) (*mcp.CallToolResult, map[string]interface{}, error) {
		el := ui.ElementByID(args.ID)
		if el == nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Element not found"}}}, nil, nil
		}
		buttons := el.Buttons()
		var buttonList []string
		for _, b := range buttons {
			buttonList = append(buttonList, b.Title())
		}
		return &mcp.CallToolResult{}, map[string]interface{}{"buttons": buttonList}, nil
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "ui_query",
		Description: "Query UI elements (generic)",
	}, SafeTool("ui_query", func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		BundleID string `json:"bundle_id,omitempty"`
		Role     string `json:"role,omitempty"`
		Label    string `json:"label,omitempty"`
		Title    string `json:"title,omitempty"`
		ID       string `json:"id,omitempty"`
	}) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		bundleID := args.BundleID
		if bundleID == "" {
			bundleID = "com.apple.iphonesimulator"
		}
		app := ui.ApplicationWithBundleID(bundleID)
		matches := app.Element().Query(ui.QueryParams{
			Role:       args.Role,
			Label:      args.Label,
			Title:      args.Title,
			Identifier: args.ID,
		})

		// Convert to basic info
		var resultMatches []map[string]interface{}
		for _, m := range matches {
			resultMatches = append(resultMatches, map[string]interface{}{
				"role":       m.Role(),
				"label":      m.Label(),
				"title":      m.Title(),
				"identifier": m.Identifier(),
				"frame":      m.Frame(),
			})
		}

		jsonBytes, _ := json.Marshal(map[string]interface{}{
			"count":   len(matches),
			"matches": resultMatches,
		})

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(jsonBytes)},
			},
		}, SimulatorActionOutput{}, nil
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "ui_screenshot",
		Description: "Take a screenshot of an element. Example: ui_screenshot(id='login')",
	}, SafeTool("ui_screenshot", func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		BundleID string `json:"bundle_id,omitempty"`
		ID       string `json:"id,omitempty"`
	}) (*mcp.CallToolResult, map[string]string, error) {
		bundleID := args.BundleID
		id := args.ID
		var el *ui.Element

		if bundleID != "" {
			app := ui.ApplicationWithBundleID(bundleID)
			if !app.Exists() {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "App not found or not running"}}}, map[string]string{}, nil
			}
			if id != "" {
				matches := app.Element().Query(ui.QueryParams{Identifier: id})
				if len(matches) > 0 {
					el = matches[0]
				}
			} else {
				el = app.Element()
			}
		} else if id != "" {
			el = ui.ElementByID(id)
		} else {
			el = ui.Application().Element()
		}

		if el == nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Element not found"}}}, map[string]string{}, nil
		}

		// Use a persistent path for the user to view
		filename := fmt.Sprintf("/tmp/xcmcp_screenshot_%s.png", id)
		if id == "" {
			filename = "/tmp/xcmcp_screenshot_app.png"
		}

		// Create file to ensure permissions? screencapture creates it.
		// remove old
		os.Remove(filename)

		// We need to implement ScreenshotToFile in Element because Screenshot() reads it into memory.
		// Or just write 'data' to file.
		data, err := el.Screenshot()
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, map[string]string{}, nil
		}

		if err := os.WriteFile(filename, data, 0644); err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Failed to write screenshot file: " + err.Error()}}}, nil, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("Screenshot saved to: %s", filename)},
				// Optional: Send a small preview or just the text
			},
		}, map[string]string{"message": "Screenshot taken", "path": filename}, nil
	}))
}
