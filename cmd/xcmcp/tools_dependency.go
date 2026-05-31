//go:build darwin

package main

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Dependency Management Tools

type SwiftPackageInput struct {
	ProjectPath string `json:"project_path" description:"Path to the project directory containing Package.swift"`
}

func registerDependencyTools(s *mcp.Server) {
	// Swift Package Manager Tools
	mcp.AddTool(s, &mcp.Tool{
		Name:        "swift_package_resolve",
		Description: "Resolve Swift Package dependencies",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args SwiftPackageInput) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		if args.ProjectPath == "" {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "project_path is required"}}}, SimulatorActionOutput{}, nil
		}

		cmd := exec.CommandContext(ctx, "swift", "package", "resolve")
		cmd.Dir = args.ProjectPath
		output, err := cmd.CombinedOutput()
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Failed to resolve packages: %v\nOutput: %s", err, output)}}}, SimulatorActionOutput{}, nil
		}

		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: fmt.Sprintf("Packages resolved successfully:\n%s", output)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "swift_package_update",
		Description: "Update Swift Package dependencies",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args SwiftPackageInput) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		if args.ProjectPath == "" {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "project_path is required"}}}, SimulatorActionOutput{}, nil
		}

		cmd := exec.CommandContext(ctx, "swift", "package", "update")
		cmd.Dir = args.ProjectPath
		output, err := cmd.CombinedOutput()
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Failed to update packages: %v\nOutput: %s", err, output)}}}, SimulatorActionOutput{}, nil
		}

		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: fmt.Sprintf("Packages updated successfully:\n%s", output)}, nil
	})
}
