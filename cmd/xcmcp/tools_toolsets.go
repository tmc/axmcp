package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/xcmcp/internal/ui"
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

// xcodeReady is signalled when the xcode bridge toolset finishes discovery
// (or fails). When --wait-for-xcode is set, server.Run blocks on this.
var xcodeReady sync.WaitGroup

func (r *toolsetRegistry) add(ts toolset) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.sets {
		if existing.name == ts.name {
			panic(fmt.Sprintf("duplicate toolset %q", ts.name))
		}
	}
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
func addXcodeBridgeToolset(prefix string, subscribeBuildErrors bool, wait bool) {
	if wait {
		xcodeReady.Add(1)
	}
	globalToolsets.add(toolset{
		name:        "xcode",
		description: "Xcode IDE tools via xcrun mcpbridge plus native Xcode workflows such as preview batches and target creation",
		async:       true,
		register: func(s *mcp.Server) {
			if wait {
				defer xcodeReady.Done()
			}

			registerXcodeTargetTools(s)

			// Wait for Accessibility trust before attempting to auto-allow
			// the Xcode MCP permission dialog. Without AX permission, the
			// auto-clicker cannot interact with Xcode's UI.
			for !ui.IsTrusted() {
				time.Sleep(500 * time.Millisecond)
			}

			const maxRetries = 5
			var proxy *xcodeProxy
			for attempt := range maxRetries {
				var err error
				proxy, err = newXcodeProxy(context.Background())
				if err == nil {
					break
				}
				backoff := time.Duration(attempt+1) * 3 * time.Second
				slog.Warn("xcode bridge connect failed, retrying", "err", err, "attempt", attempt+1, "backoff", backoff)
				time.Sleep(backoff)
			}
			if proxy == nil {
				slog.Warn("xcode tools unavailable after retries")
				return
			}
			setXcodeBridge(proxy)
			registerXcodeWorkflowTools(s)
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
	for _, ts := range standardToolsets() {
		globalToolsets.add(ts)
	}

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
