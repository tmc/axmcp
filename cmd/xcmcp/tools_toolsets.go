package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolset describes an optional group of tools that can be enabled at runtime.
type toolset struct {
	name        string
	description string
	register    func(s *mcp.Server)
	async       bool // register runs in a goroutine (e.g. network connection)
}

// toolsetRegistry tracks which optional toolsets are registered.
type toolsetRegistry struct {
	mu      sync.Mutex
	enabled map[string]bool
	sets    []toolset
}

var globalToolsets = &toolsetRegistry{
	enabled: map[string]bool{},
}

func (r *toolsetRegistry) add(ts toolset) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sets = append(r.sets, ts)
}

func (r *toolsetRegistry) enable(s *mcp.Server, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ts := range r.sets {
		if ts.name == name {
			if r.enabled[name] {
				return fmt.Errorf("toolset %q is already enabled", name)
			}
			r.enabled[name] = true
			if ts.async {
				go ts.register(s)
			} else {
				ts.register(s)
			}
			return nil
		}
	}
	names := make([]string, len(r.sets))
	for i, ts := range r.sets {
		names[i] = ts.name
	}
	return fmt.Errorf("unknown toolset %q; available: %s", name, strings.Join(names, ", "))
}

func (r *toolsetRegistry) list() []map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]map[string]string, len(r.sets))
	for i, ts := range r.sets {
		status := "available"
		if r.enabled[ts.name] {
			status = "enabled"
		}
		out[i] = map[string]string{
			"name":        ts.name,
			"description": ts.description,
			"status":      status,
		}
	}
	return out
}

// addXcodeBridgeToolset adds the Xcode mcpbridge as a named toolset. Call this
// before registerToolsetTools so it appears in the list and description.
func addXcodeBridgeToolset(prefix string, subscribeBuildErrors bool) {
	globalToolsets.add(toolset{
		name:        "xcode",
		description: "Xcode IDE tools via xcrun mcpbridge (build log, source navigation, diagnostics, etc.)",
		async:       true,
		register: func(s *mcp.Server) {
			proxy, err := newXcodeProxy(context.Background())
			if err != nil {
				slog.Warn("xcode tools unavailable", "err", err)
				return
			}
			n, err := registerXcodeTools(s, proxy, prefix)
			if err != nil {
				slog.Warn("error discovering xcode tools", "err", err)
			} else {
				slog.Info("registered xcode tools from mcpbridge", "count", n)
			}
			if subscribeBuildErrors {
				registerBuildErrorResource(s, proxy)
			}
		},
	})
}

// registerToolsetTools registers the toolset discovery/enable tools and declares
// the optional toolsets. Call this after registering the always-on tools.
func registerToolsetTools(s *mcp.Server) {
	// Declare optional toolsets
	globalToolsets.add(toolset{
		name:        "app",
		description: "App management tools: launch, terminate, install, uninstall, logs, list apps",
		register:    registerAppTools,
	})
	globalToolsets.add(toolset{
		name:        "ui",
		description: "UI automation tools: tap, type, swipe, tree, screenshot, query, inspect, drag",
		register:    registerUITools,
	})
	globalToolsets.add(toolset{
		name:        "device",
		description: "Simulator device control: orientation, appearance, location, biometry, privacy, screenshot",
		register:    registerDeviceTools,
	})
	globalToolsets.add(toolset{
		name:        "ios",
		description: "iOS-specific tools: accessibility tree, hit testing, simulator list, device info",
		register:    registerIOSTools,
	})
	globalToolsets.add(toolset{
		name:        "simulator_extras",
		description: "Simulator extras: app container path, open URL, add photos/videos to library",
		register:    registerExtraTools,
	})
	globalToolsets.add(toolset{
		name:        "physical_device",
		description: "Tools for managing physical iOS/macOS devices (install, run, logs, etc.)",
		register:    registerPhysicalDeviceTools,
	})
	globalToolsets.add(toolset{
		name:        "video",
		description: "Video recording tools for simulators",
		register:    registerVideoTools,
	})
	globalToolsets.add(toolset{
		name:        "crash",
		description: "Crash log collection and symbolication tools",
		register:    registerCrashTools,
	})
	globalToolsets.add(toolset{
		name:        "filesystem",
		description: "File system access tools for simulator and device containers",
		register:    registerFileSystemTools,
	})
	globalToolsets.add(toolset{
		name:        "dependency",
		description: "Dependency management tools (CocoaPods, Swift Package Manager)",
		register:    registerDependencyTools,
	})
	globalToolsets.add(toolset{
		name:        "asc",
		description: "App Store Connect and altool tools for distribution and TestFlight",
		register:    registerASCTools,
	})

	// list_toolsets — discover available optional tool groups
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_toolsets",
		Description: "List available optional tool categories. Use enable_toolset to add tools from a category to this session.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct {
		Toolsets []map[string]string `json:"toolsets"`
	}, error) {
		return &mcp.CallToolResult{}, struct {
			Toolsets []map[string]string `json:"toolsets"`
		}{Toolsets: globalToolsets.list()}, nil
	})

	// Build enable_toolset description dynamically so it lists all categories.
	var sb strings.Builder
	sb.WriteString("Enable an optional tool category, making its tools immediately available in this session.\n\nAvailable toolsets:\n")
	for _, ts := range globalToolsets.sets {
		fmt.Fprintf(&sb, "  • %s — %s\n", ts.name, ts.description)
	}
	sb.WriteString("\nAfter calling this, re-list tools to see the new additions.")

	// enable_toolset — dynamically register a category's tools
	mcp.AddTool(s, &mcp.Tool{
		Name:        "enable_toolset",
		Description: sb.String(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		Name string `json:"name" description:"The toolset name to enable (from list_toolsets)"`
	}) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		if err := globalToolsets.enable(s, args.Name); err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, SimulatorActionOutput{}, nil
		}
		return &mcp.CallToolResult{}, SimulatorActionOutput{
			Message: fmt.Sprintf("toolset %q enabled — tools are now available", args.Name),
		}, nil
	})
}
