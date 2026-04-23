package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/ui/permissions"
)

func newComputerUseServer(rt *runtimeState) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "computer-use-mcp",
		Version: "0.1.0",
	}, &mcp.ServerOptions{
		Instructions: computerUseInstructions(),
		Capabilities: &mcp.ServerCapabilities{
			Tools:     &mcp.ToolCapabilities{ListChanged: false},
			Resources: &mcp.ResourceCapabilities{ListChanged: true},
		},
	})
	registerComputerUseTools(server, rt)
	registerPermissionResource(server)
	server.AddReceivingMiddleware(computerUseCompatibilityMiddleware())
	return server
}

func computerUseCompatibilityMiddleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case "tools/list":
				return &mcp.ListToolsResult{Tools: orderedComputerUseTools()}, nil
			case "resources/templates/list":
				return nil, methodNotFoundError(method)
			default:
				return next(ctx, method, req)
			}
		}
	}
}

func registerPermissionResource(server *mcp.Server) {
	server.AddResource(&mcp.Resource{
		URI:         "mcp://permissions/status",
		Name:        "permissions-status",
		Description: "Current aggregated permission status for computer-use-mcp",
		MIMEType:    "application/json",
	}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		snapshot := permissions.CurrentSnapshot(permissions.ReqAccessibility, permissions.ReqScreenRecording)
		data, err := json.MarshalIndent(snapshot, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshal permissions status: %w", err)
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      "mcp://permissions/status",
					MIMEType: "application/json",
					Text:     string(data),
				},
			},
		}, nil
	})
}

func methodNotFoundError(method string) error {
	detail := fmt.Sprintf("Unknown method: %s", method)
	data, err := json.Marshal(map[string]any{"detail": detail})
	if err != nil {
		data = nil
	}
	return &jsonrpc.Error{
		Code:    jsonrpc.CodeMethodNotFound,
		Message: "Method not found: " + detail,
		Data:    data,
	}
}

func orderedComputerUseTools() []*mcp.Tool {
	return []*mcp.Tool{
		{
			Name:        "list_apps",
			Description: "List the apps on this computer. Returns the set of apps that are currently running, as well as any that have been used in the last 14 days, including details on usage frequency",
			Annotations: readOnlyToolAnnotations(),
			InputSchema: exactObjectSchema(map[string]any{}),
		},
		{
			Name:        "get_app_state",
			Description: "Start an app use session if needed, then get the state of the app's key window and return a screenshot and accessibility tree. This must be called once per assistant turn before interacting with the app",
			Annotations: readOnlyToolAnnotations(),
			InputSchema: exactObjectSchema(map[string]any{
				"app": stringProperty("App name or bundle identifier"),
			}, "app"),
		},
		{
			Name:        "click",
			Description: "Click an element by index or pixel coordinates from screenshot",
			Annotations: actionToolAnnotations(),
			InputSchema: exactObjectSchema(map[string]any{
				"app":           stringProperty("App name or bundle identifier"),
				"click_count":   integerProperty("Number of clicks. Defaults to 1"),
				"element_index": stringProperty("Element index to click"),
				"mouse_button":  enumStringProperty("Mouse button to click. Defaults to left.", "left", "right", "middle"),
				"x":             numberProperty("X coordinate in screenshot pixel coordinates"),
				"y":             numberProperty("Y coordinate in screenshot pixel coordinates"),
			}, "app"),
		},
		{
			Name:        "perform_secondary_action",
			Description: "Invoke a secondary accessibility action exposed by an element",
			Annotations: actionToolAnnotations(),
			InputSchema: exactObjectSchema(map[string]any{
				"action":        stringProperty("Secondary accessibility action name"),
				"app":           stringProperty("App name or bundle identifier"),
				"element_index": stringProperty("Element identifier"),
			}, "app", "element_index", "action"),
		},
		{
			Name:        "set_value",
			Description: "Set the value of a settable accessibility element",
			Annotations: actionToolAnnotations(),
			InputSchema: exactObjectSchema(map[string]any{
				"app":           stringProperty("App name or bundle identifier"),
				"element_index": stringProperty("Element identifier"),
				"value":         stringProperty("Value to assign"),
			}, "app", "element_index", "value"),
		},
		{
			Name:        "scroll",
			Description: "Scroll an element in a direction by a number of pages",
			Annotations: actionToolAnnotations(),
			InputSchema: exactObjectSchema(map[string]any{
				"app":           stringProperty("App name or bundle identifier"),
				"direction":     stringProperty("Scroll direction: up, down, left, or right"),
				"element_index": stringProperty("Element identifier"),
				"pages":         numberProperty("Number of pages to scroll. Fractional values are supported. Defaults to 1"),
			}, "app", "element_index", "direction"),
		},
		{
			Name:        "drag",
			Description: "Drag from one point to another using pixel coordinates",
			Annotations: actionToolAnnotations(),
			InputSchema: exactObjectSchema(map[string]any{
				"app":    stringProperty("App name or bundle identifier"),
				"from_x": numberProperty("Start X coordinate"),
				"from_y": numberProperty("Start Y coordinate"),
				"to_x":   numberProperty("End X coordinate"),
				"to_y":   numberProperty("End Y coordinate"),
			}, "app", "from_x", "from_y", "to_x", "to_y"),
		},
		{
			Name:        "press_key",
			Description: "Press a key or key-combination on the keyboard, including modifier and navigation keys.\n  - This supports xdotool's `key` syntax.\n  - Examples: \"a\", \"Return\", \"Tab\", \"super+c\", \"Up\", \"KP_0\" (for the numpad 0 key).",
			Annotations: actionToolAnnotations(),
			InputSchema: exactObjectSchema(map[string]any{
				"app": stringProperty("App name or bundle identifier"),
				"key": stringProperty("Key or key combination to press"),
			}, "app", "key"),
		},
		{
			Name:        "type_text",
			Description: "Type literal text using keyboard input",
			Annotations: actionToolAnnotations(),
			InputSchema: exactObjectSchema(map[string]any{
				"app":  stringProperty("App name or bundle identifier"),
				"text": stringProperty("Literal text to type"),
			}, "app", "text"),
		},
	}
}
