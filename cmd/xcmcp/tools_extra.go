package main

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/simctl"
)

// App Container
type AppContainerInput struct {
	BundleID      string `json:"bundle_id"`
	ContainerType string `json:"container_type,omitempty" description:"app, data, groups, sile (default: data)"`
	UDID          string `json:"udid,omitempty"`
}

type AppContainerOutput struct {
	Path string `json:"path"`
}

func registerExtraTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "app_container_path",
		Description: "Get the path to an app's container on the simulator",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args AppContainerInput) (*mcp.CallToolResult, AppContainerOutput, error) {
		udid := args.UDID
		if udid == "" {
			udid = "booted"
		}
		path, err := simctl.GetAppContainer(ctx, udid, args.BundleID, args.ContainerType)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, AppContainerOutput{}, nil
		}
		return &mcp.CallToolResult{}, AppContainerOutput{Path: path}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "sim_open_url",
		Description: "Open a URL on the simulator",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		URL  string `json:"url"`
		UDID string `json:"udid,omitempty"`
	}) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		udid := args.UDID
		if udid == "" {
			udid = "booted"
		}
		if err := simctl.OpenURL(ctx, udid, args.URL); err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, SimulatorActionOutput{}, nil
		}
		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: fmt.Sprintf("Opened URL %s", args.URL)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "sim_add_media",
		Description: "Add photos/videos to the simulator's library",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		Path string `json:"path"`
		UDID string `json:"udid,omitempty"`
	}) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		udid := args.UDID
		if udid == "" {
			udid = "booted"
		}
		if err := simctl.AddMedia(ctx, udid, args.Path); err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, SimulatorActionOutput{}, nil
		}
		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: fmt.Sprintf("Added media %s", args.Path)}, nil
	})
}
