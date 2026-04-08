package main

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/simctl"
	"github.com/tmc/axmcp/internal/ui"
)

// App Lifecycle
type AppLifecycleInput struct {
	BundleID string `json:"bundle_id"`
}

func registerAppTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "app_launch",
		Description: "Launch an application by Bundle ID",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args AppLifecycleInput) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		broadcastLog(s, mcp.LoggingLevel("info"), "app_launch", fmt.Sprintf("Launching app %s...", args.BundleID))

		// Use background context to avoid MCP request context cancellation
		launchCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		if err := simctl.Launch(launchCtx, "booted", args.BundleID); err != nil {
			broadcastLog(s, mcp.LoggingLevel("error"), "app_launch", fmt.Sprintf("Launch failed: %v", err))
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, SimulatorActionOutput{}, nil
		}
		broadcastLog(s, mcp.LoggingLevel("info"), "app_launch", "Launch successful")
		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: "Launched " + args.BundleID}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "app_terminate",
		Description: "Terminate an application by Bundle ID",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args AppLifecycleInput) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		broadcastLog(s, mcp.LoggingLevel("info"), "app_terminate", fmt.Sprintf("Terminating app %s...", args.BundleID))

		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		if err := simctl.Terminate(ctx, "booted", args.BundleID); err != nil {
			broadcastLog(s, mcp.LoggingLevel("error"), "app_terminate", fmt.Sprintf("Terminate failed: %v", err))
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, SimulatorActionOutput{}, nil
		}
		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: "Terminated " + args.BundleID}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "app_list",
		Description: "List running applications/processes on booted simulator",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, map[string][]string, error) {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()

		apps, err := simctl.ListRunningApps(ctx, "booted")
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
		}
		if apps == nil {
			apps = []string{}
		}
		return &mcp.CallToolResult{}, map[string][]string{"apps": apps}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "app_list_installed",
		Description: "List all installed applications on booted simulator",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, map[string]string, error) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		out, err := simctl.ListApps(ctx, "booted")
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
		}
		return &mcp.CallToolResult{}, map[string]string{"output": out}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "app_install",
		Description: "Install an application to the booted simulator",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		Path string `json:"path"`
	}) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		if err := simctl.InstallApp(ctx, "booted", args.Path); err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, SimulatorActionOutput{}, nil
		}
		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: "Installed " + args.Path}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "app_uninstall",
		Description: "Uninstall an application from the booted simulator",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		BundleID string `json:"bundle_id"`
	}) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		if err := simctl.UninstallApp(ctx, "booted", args.BundleID); err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, SimulatorActionOutput{}, nil
		}
		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: "Uninstalled " + args.BundleID}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "app_logs",
		Description: "Get logs for an application (snapshot)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		Query    string `json:"query" description:"Bundle ID or Process Name"`
		Duration string `json:"duration" description:"Lookback duration (e.g. 5m, 1h)"`
	}) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		logs, err := simctl.GetAppLogs(ctx, "booted", args.Query, args.Duration)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, SimulatorActionOutput{}, nil
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: logs},
			},
		}, SimulatorActionOutput{Message: fmt.Sprintf("Retrieved logs for %s", args.Query)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "app_get_state",
		Description: "Get application state (Running/NotRunning)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args AppLifecycleInput) (*mcp.CallToolResult, map[string]string, error) {
		app := ui.NewApp(args.BundleID)
		state := "NotRunning"
		if app.Exists() {
			state = "Running"
		}
		return &mcp.CallToolResult{}, map[string]string{"state": state}, nil
	})
}
