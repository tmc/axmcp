package main

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/project"
	"github.com/tmc/axmcp/internal/xcodebuild"
)

// Discover Projects
type DiscoverProjectsInput struct {
	Path string `json:"path"`
}

type DiscoverProjectsOutput struct {
	Projects []map[string]interface{} `json:"projects"`
}

func registerDiscoverProjects(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "discover_projects",
		Title:       "Discover Projects",
		Description: "Find all .xcodeproj and .xcworkspace files in a directory",
		Annotations: readOnlyTool("Discover Projects"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, args DiscoverProjectsInput) (*mcp.CallToolResult, DiscoverProjectsOutput, error) {
		path := args.Path
		if path == "" {
			path = sessionProjectRoot(ctx, req.Session, ".")
		}
		projects, err := project.Discover(path)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Failed to discover projects: %v", err)},
				},
			}, DiscoverProjectsOutput{Projects: []map[string]interface{}{}}, nil
		}

		result := []map[string]interface{}{}
		for _, p := range projects {
			schemes, _ := p.GetSchemes(ctx)
			result = append(result, map[string]interface{}{
				"path":    p.Path,
				"name":    p.Name,
				"type":    p.Type.String(),
				"schemes": schemes,
			})
		}

		return &mcp.CallToolResult{}, DiscoverProjectsOutput{Projects: result}, nil
	})
}

// List Schemes
type ListSchemesInput struct {
	Path string `json:"path"`
}

type ListSchemesOutput struct {
	Schemes []string `json:"schemes"`
}

func registerListSchemes(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_schemes",
		Title:       "List Schemes",
		Description: "List available schemes for a project",
		Annotations: readOnlyTool("List Schemes"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, args ListSchemesInput) (*mcp.CallToolResult, ListSchemesOutput, error) {
		path, err := inferProjectPath(ctx, req.Session, args.Path)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Failed to infer project path: %v", err)},
				},
			}, ListSchemesOutput{Schemes: []string{}}, nil
		}
		p, err := project.Open(path)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Failed to open project: %v", err)},
				},
			}, ListSchemesOutput{Schemes: []string{}}, nil
		}
		schemes, err := p.GetSchemes(ctx)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Failed to list schemes: %v", err)},
				},
			}, ListSchemesOutput{Schemes: []string{}}, nil
		}
		if schemes == nil {
			schemes = []string{}
		}
		return &mcp.CallToolResult{}, ListSchemesOutput{Schemes: schemes}, nil
	})
}

// Show Build Settings
type ShowBuildSettingsInput struct {
	Path          string `json:"path"`
	Scheme        string `json:"scheme,omitempty"`
	Configuration string `json:"configuration,omitempty"`
}

type ShowBuildSettingsOutput struct {
	Settings map[string]string `json:"settings"`
}

func registerShowBuildSettings(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "show_build_settings",
		Title:       "Show Build Settings",
		Description: "Get build settings for scheme/config",
		Annotations: readOnlyTool("Show Build Settings"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, args ShowBuildSettingsInput) (*mcp.CallToolResult, ShowBuildSettingsOutput, error) {
		path, err := inferProjectPath(ctx, req.Session, args.Path)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Failed to infer project path: %v", err)},
				},
			}, ShowBuildSettingsOutput{Settings: map[string]string{}}, nil
		}

		p, err := project.Open(path)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Failed to open project: %v", err)},
				},
			}, ShowBuildSettingsOutput{Settings: map[string]string{}}, nil
		}

		settings, err := p.BuildSettings(ctx, args.Scheme, args.Configuration)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Failed to get build settings: %v", err)},
				},
			}, ShowBuildSettingsOutput{Settings: map[string]string{}}, nil
		}

		if settings == nil {
			settings = map[string]string{}
		}
		return &mcp.CallToolResult{}, ShowBuildSettingsOutput{Settings: settings}, nil
	})
}

// Build
type BuildInput struct {
	Project       string `json:"project,omitempty"`
	Workspace     string `json:"workspace,omitempty"`
	Scheme        string `json:"scheme"`
	Configuration string `json:"configuration,omitempty"`
	Destination   string `json:"destination,omitempty"`
}

type BuildOutput struct {
	Result *xcodebuild.BuildResult `json:"result"`
}

func registerBuild(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "build",
		Title:       "Build",
		Description: "Build an Xcode project or workspace",
		Annotations: additiveTool("Build", false),
	}, func(ctx context.Context, req *mcp.CallToolRequest, args BuildInput) (*mcp.CallToolResult, BuildOutput, error) {
		projectPath, workspacePath, err := inferBuildLocator(ctx, req.Session, args.Project, args.Workspace)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Failed to infer project or workspace: %v", err)},
				},
			}, BuildOutput{}, nil
		}

		result, err := xcodebuild.Build(ctx, xcodebuild.BuildOptions{
			Project:       projectPath,
			Workspace:     workspacePath,
			Scheme:        args.Scheme,
			Configuration: args.Configuration,
			Destination:   args.Destination,
		})
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Build execution failed: %v", err)},
				},
			}, BuildOutput{}, nil
		}

		return &mcp.CallToolResult{}, BuildOutput{Result: result}, nil
	})
}

// Test
func registerTest(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "test",
		Title:       "Test",
		Description: "Run tests for an Xcode project or workspace",
		Annotations: additiveTool("Test", false),
	}, func(ctx context.Context, req *mcp.CallToolRequest, args BuildInput) (*mcp.CallToolResult, BuildOutput, error) {
		projectPath, workspacePath, err := inferBuildLocator(ctx, req.Session, args.Project, args.Workspace)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Failed to infer project or workspace: %v", err)},
				},
			}, BuildOutput{}, nil
		}

		result, err := xcodebuild.Test(ctx, xcodebuild.BuildOptions{
			Project:       projectPath,
			Workspace:     workspacePath,
			Scheme:        args.Scheme,
			Configuration: args.Configuration,
			Destination:   args.Destination,
		})
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Test execution failed: %v", err)},
				},
			}, BuildOutput{}, nil
		}

		return &mcp.CallToolResult{}, BuildOutput{Result: result}, nil
	})
}
