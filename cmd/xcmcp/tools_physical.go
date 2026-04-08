package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/devicectl"
)

// Physical Device Tools

type PhysicalDeviceListOutput struct {
	Devices []devicectl.Device `json:"devices"`
}

type PhysicalDeviceInput struct {
	Identifier string `json:"identifier"`
}

type PhysicalDeviceInfoOutput struct {
	Info string `json:"info"`
}

type PhysicalDeviceAppInput struct {
	Identifier string `json:"identifier"`
	AppPath    string `json:"app_path,omitempty"`
	BundleID   string `json:"bundle_id,omitempty"`
}

func registerPhysicalDeviceTools(s *mcp.Server) {
	// List Physical Devices
	mcp.AddTool(s, &mcp.Tool{
		Name:        "physical_devices_list",
		Description: "List all physical iOS/macOS devices connected to this machine",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, PhysicalDeviceListOutput, error) {
		devices, err := devicectl.ListDevices(ctx)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Failed to list devices: %v", err)},
				},
			}, PhysicalDeviceListOutput{}, nil
		}
		return &mcp.CallToolResult{}, PhysicalDeviceListOutput{Devices: devices}, nil
	})

	// Get Physical Device Info
	mcp.AddTool(s, &mcp.Tool{
		Name:        "physical_device_info",
		Description: "Get detailed information about a specific physical device",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args PhysicalDeviceInput) (*mcp.CallToolResult, PhysicalDeviceInfoOutput, error) {
		info, err := devicectl.DeviceInfo(ctx, args.Identifier)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Failed to get device info: %v", err)},
				},
			}, PhysicalDeviceInfoOutput{}, nil
		}

		// Format as JSON string for readability
		jsonBytes, _ := json.MarshalIndent(info, "", "  ")
		return &mcp.CallToolResult{}, PhysicalDeviceInfoOutput{Info: string(jsonBytes)}, nil
	})

	// Install App on Physical Device
	mcp.AddTool(s, &mcp.Tool{
		Name:        "physical_device_install",
		Description: "Install an application (.app or .ipa) on a physical device",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args PhysicalDeviceAppInput) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		if args.AppPath == "" {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: "app_path is required"},
				},
			}, SimulatorActionOutput{}, nil
		}

		if err := devicectl.InstallApp(ctx, args.Identifier, args.AppPath); err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Failed to install app: %v", err)},
				},
			}, SimulatorActionOutput{}, nil
		}
		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: "App installed successfully"}, nil
	})

	// Uninstall App from Physical Device
	mcp.AddTool(s, &mcp.Tool{
		Name:        "physical_device_uninstall",
		Description: "Uninstall an application from a physical device by bundle ID",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args PhysicalDeviceAppInput) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		if args.BundleID == "" {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: "bundle_id is required"},
				},
			}, SimulatorActionOutput{}, nil
		}

		if err := devicectl.UninstallApp(ctx, args.Identifier, args.BundleID); err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Failed to uninstall app: %v", err)},
				},
			}, SimulatorActionOutput{}, nil
		}
		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: "App uninstalled successfully"}, nil
	})

	// Launch App on Physical Device
	mcp.AddTool(s, &mcp.Tool{
		Name:        "physical_device_launch",
		Description: "Launch an application on a physical device by bundle ID",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args PhysicalDeviceAppInput) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		if args.BundleID == "" {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: "bundle_id is required"},
				},
			}, SimulatorActionOutput{}, nil
		}

		if err := devicectl.LaunchApp(ctx, args.Identifier, args.BundleID); err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Failed to launch app: %v", err)},
				},
			}, SimulatorActionOutput{}, nil
		}
		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: "App launched successfully"}, nil
	})

	// Terminate App on Physical Device
	mcp.AddTool(s, &mcp.Tool{
		Name:        "physical_device_terminate",
		Description: "Terminate an application on a physical device by bundle ID",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args PhysicalDeviceAppInput) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		if args.BundleID == "" {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: "bundle_id is required"},
				},
			}, SimulatorActionOutput{}, nil
		}

		if err := devicectl.TerminateApp(ctx, args.Identifier, args.BundleID); err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Failed to terminate app: %v", err)},
				},
			}, SimulatorActionOutput{}, nil
		}
		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: "App terminated successfully"}, nil
	})

	// Reboot Physical Device
	mcp.AddTool(s, &mcp.Tool{
		Name:        "physical_device_reboot",
		Description: "Reboot a physical device",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args PhysicalDeviceInput) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		if err := devicectl.RebootDevice(ctx, args.Identifier); err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Failed to reboot device: %v", err)},
				},
			}, SimulatorActionOutput{}, nil
		}
		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: "Device rebooting"}, nil
	})
}
