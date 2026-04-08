package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/purego/coresim"
)

// IOSTreeOutput represents the output of ios_tree.
type IOSTreeOutput struct {
	Tree     string           `json:"tree"`
	Elements []map[string]any `json:"elements,omitempty"`
}

// IOSQueryOutput represents the output of ios_query.
type IOSQueryOutput struct {
	Count    int              `json:"count"`
	Matches  []map[string]any `json:"matches"`
	DeviceID string           `json:"device_id,omitempty"`
}

func registerIOSTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "ios_tree",
		Description: "Get iOS UI accessibility tree from booted simulator. Queries actual iOS app content, not the Simulator.app window.",
	}, SafeTool("ios_tree", func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		UDID string `json:"udid,omitempty" description:"Simulator UDID (uses first booted if not specified)"`
	}) (*mcp.CallToolResult, IOSTreeOutput, error) {
		if !coresim.Available() {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "CoreSimulator.framework not available"}},
			}, IOSTreeOutput{}, nil
		}

		device, err := getSimDevice(args.UDID)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, IOSTreeOutput{}, nil
		}

		elements, err := device.GetAccessibilityElements()
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Failed to get accessibility tree: %v", err)}},
			}, IOSTreeOutput{}, nil
		}

		jsonData, _ := json.MarshalIndent(elements, "", "  ")

		// Convert to []map[string]any to ensure stricter object schema
		var elementsAny []map[string]any
		// Marshal/Unmarshal to convert arbitrary struct to map without deep reflection manual implementation
		// This is inefficient but safe for the schema generator
		elementsJSON, _ := json.Marshal(elements)
		_ = json.Unmarshal(elementsJSON, &elementsAny)

		return &mcp.CallToolResult{}, IOSTreeOutput{
			Tree:     string(jsonData),
			Elements: elementsAny,
		}, nil
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "ios_hit_test",
		Description: "Get iOS accessibility element at a specific screen coordinate. Useful for identifying elements by position.",
	}, SafeTool("ios_hit_test", func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		X    float64 `json:"x" description:"X coordinate in screen points"`
		Y    float64 `json:"y" description:"Y coordinate in screen points"`
		UDID string  `json:"udid,omitempty" description:"Simulator UDID (uses first booted if not specified)"`
	}) (*mcp.CallToolResult, IOSQueryOutput, error) {
		if !coresim.Available() {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "CoreSimulator.framework not available"}},
			}, IOSQueryOutput{}, nil
		}

		device, err := getSimDevice(args.UDID)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, IOSQueryOutput{}, nil
		}

		element, err := device.GetAccessibilityElementAtPoint(args.X, args.Y, "")
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Failed to get element at point: %v", err)}},
			}, IOSQueryOutput{}, nil
		}

		var matches []*coresim.AccessibilityElement
		if element != nil {
			matches = []*coresim.AccessibilityElement{element}
		}

		jsonData, _ := json.MarshalIndent(map[string]interface{}{
			"point":   map[string]float64{"x": args.X, "y": args.Y},
			"element": element,
		}, "", "  ")

		// Convert to []map[string]any to ensure stricter object schema
		var matchesAny []map[string]any
		matchesJSON, _ := json.Marshal(matches)
		_ = json.Unmarshal(matchesJSON, &matchesAny)

		return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: string(jsonData)}},
			}, IOSQueryOutput{
				Count:    len(matches),
				Matches:  matchesAny,
				DeviceID: device.UDID(),
			}, nil
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "ios_simulators",
		Description: "List iOS simulators with their state (booted/shutdown).",
	}, SafeTool("ios_simulators", func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		State string `json:"state,omitempty" description:"Filter by state: 'booted', 'shutdown', or empty for all"`
	}) (*mcp.CallToolResult, map[string]interface{}, error) {
		if !coresim.Available() {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "CoreSimulator.framework not available"}},
			}, nil, nil
		}

		set, err := coresim.DefaultSet()
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Failed to get device set: %v", err)}},
			}, nil, nil
		}

		devices := set.Devices()
		var result []map[string]interface{}

		for _, dev := range devices {
			state := dev.State()
			stateStr := state.String()

			// Filter by state if specified
			if args.State != "" {
				if args.State == "booted" && state != coresim.SimDeviceStateBooted {
					continue
				}
				if args.State == "shutdown" && state != coresim.SimDeviceStateShutdown {
					continue
				}
			}

			result = append(result, map[string]interface{}{
				"udid":       dev.UDID(),
				"name":       dev.Name(),
				"state":      stateStr,
				"deviceType": dev.DeviceTypeIdentifier(),
				"runtimeId":  dev.RuntimeIdentifier(),
			})
		}

		return &mcp.CallToolResult{}, map[string]interface{}{
			"count":      len(result),
			"simulators": result,
		}, nil
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "ios_device_info",
		Description: "Get detailed information about a specific iOS simulator device.",
	}, SafeTool("ios_device_info", func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		UDID string `json:"udid,omitempty" description:"Simulator UDID (uses first booted if not specified)"`
	}) (*mcp.CallToolResult, map[string]interface{}, error) {
		if !coresim.Available() {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "CoreSimulator.framework not available"}},
			}, nil, nil
		}

		device, err := getSimDevice(args.UDID)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, nil, nil
		}

		info := map[string]interface{}{
			"udid":       device.UDID(),
			"name":       device.Name(),
			"state":      device.State().String(),
			"deviceType": device.DeviceTypeIdentifier(),
			"runtimeId":  device.RuntimeIdentifier(),
		}

		return &mcp.CallToolResult{}, info, nil
	}))
}

// getSimDevice returns a simulator device by UDID, or the first booted device if no UDID specified.
func getSimDevice(udid string) (coresim.SimDevice, error) {
	if udid != "" {
		device, found := coresim.FindDeviceByUDID(udid)
		if !found {
			return coresim.SimDevice{}, fmt.Errorf("simulator with UDID %s not found", udid)
		}
		return device, nil
	}

	// Use first booted device
	booted := coresim.ListBootedDevices()
	if len(booted) == 0 {
		return coresim.SimDevice{}, fmt.Errorf("no booted simulators found")
	}
	return booted[0], nil
}
