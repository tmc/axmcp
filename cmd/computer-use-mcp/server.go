package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/buildversion"
	"github.com/tmc/axmcp/internal/ui/permissions"
)

func newComputerUseServer(rt *runtimeState) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "computer-use-mcp",
		Version: buildversion.String(),
	}, &mcp.ServerOptions{
		Instructions: computerUseInstructions(),
		Capabilities: &mcp.ServerCapabilities{
			Tools:     &mcp.ToolCapabilities{ListChanged: false},
			Resources: &mcp.ResourceCapabilities{ListChanged: true},
		},
		SupportedProtocolVersions: handshakeProtocolVersions(),
	})
	registerComputerUseTools(server, rt)
	if rt.native == nil {
		rt.native = newNativeRunner(&nativeOSBackend{rt: rt})
	}
	registerNativeTools(server, rt.native)
	registerPermissionResource(server)
	server.AddReceivingMiddleware(computerUseCompatibilityMiddleware())
	return server
}

// handshakeProtocolVersions returns the MCP protocol versions that predate
// 2026-07-28. Approval prompts elicit from inside tool calls, which that
// revision forbids.
func handshakeProtocolVersions() []string {
	var versions []string
	for _, v := range mcp.SupportedProtocolVersions() {
		if v < "2026-07-28" {
			versions = append(versions, v)
		}
	}
	return versions
}

// nativeTools are the tools listed after the compatibility set.
var nativeTools = map[string]bool{
	"native_revoke_approval":  true,
	"native_request_approval": true,
	"native_discover":         true,
	"native_select":           true,
	"native_release":          true,
	"native_observe":          true,
	"native_act":              true,
	"native_recover_pointer":  true,
}

func computerUseCompatibilityMiddleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case "tools/list":
				result, err := next(ctx, method, req)
				if err != nil {
					return nil, err
				}
				listed, ok := result.(*mcp.ListToolsResult)
				if !ok {
					return nil, fmt.Errorf("unexpected tool listing")
				}
				tools := orderedComputerUseTools()
				for _, tool := range listed.Tools {
					if nativeTools[tool.Name] {
						tools = append(tools, tool)
					}
				}
				return &mcp.ListToolsResult{Tools: tools}, nil
			case "resources/templates/list":
				// The SDK replaces the message with its own.
				return nil, &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound}
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

func orderedComputerUseTools() []*mcp.Tool {
	return []*mcp.Tool{
		{
			Name:        "list_apps",
			Description: "List currently running apps with their names, bundle IDs, and process IDs.",
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
				"state_id":      stringProperty("State token returned by get_app_state"),
				"x":             numberProperty("X coordinate in screenshot pixel coordinates"),
				"y":             numberProperty("Y coordinate in screenshot pixel coordinates"),
			}, "app", "state_id"),
		},
		{
			Name:        "perform_secondary_action",
			Description: "Invoke a secondary accessibility action exposed by an element",
			Annotations: actionToolAnnotations(),
			InputSchema: exactObjectSchema(map[string]any{
				"action":        stringProperty("Secondary accessibility action name"),
				"app":           stringProperty("App name or bundle identifier"),
				"element_index": stringProperty("Element identifier"),
				"state_id":      stringProperty("State token returned by get_app_state"),
			}, "app", "state_id", "element_index", "action"),
		},
		{
			Name:        "set_value",
			Description: "Set the value of a settable accessibility element",
			Annotations: actionToolAnnotations(),
			InputSchema: exactObjectSchema(map[string]any{
				"app":           stringProperty("App name or bundle identifier"),
				"element_index": stringProperty("Element identifier"),
				"state_id":      stringProperty("State token returned by get_app_state"),
				"value":         stringProperty("Value to assign"),
			}, "app", "state_id", "element_index", "value"),
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
				"state_id":      stringProperty("State token returned by get_app_state"),
			}, "app", "state_id", "element_index", "direction"),
		},
		{
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
		},
		{
			Name:        "press_key",
			Description: "Press a key or key-combination on the keyboard, including modifier and navigation keys.\n  - This supports xdotool's `key` syntax.\n  - Examples: \"a\", \"Return\", \"Tab\", \"super+c\", \"Up\", \"KP_0\" (for the numpad 0 key).",
			Annotations: actionToolAnnotations(),
			InputSchema: exactObjectSchema(map[string]any{
				"app":      stringProperty("App name or bundle identifier"),
				"key":      stringProperty("Key or key combination to press"),
				"state_id": stringProperty("State token returned by get_app_state"),
			}, "app", "state_id", "key"),
		},
		{
			Name:        "type_text",
			Description: "Type literal text using keyboard input",
			Annotations: actionToolAnnotations(),
			InputSchema: exactObjectSchema(map[string]any{
				"app":           stringProperty("App name or bundle identifier"),
				"element_index": stringProperty("Element index to type into. When omitted, the app's focused element is used."),
				"state_id":      stringProperty("State token returned by get_app_state"),
				"text":          stringProperty("Literal text to type"),
			}, "app", "state_id", "text"),
		},
	}
}
