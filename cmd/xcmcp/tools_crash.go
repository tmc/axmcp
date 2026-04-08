package main

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/crash"
)

type CrashListOutput struct {
	Reports []crash.Report `json:"reports"`
}

func registerCrashTools(s *mcp.Server) {
	// crash_list tool
	mcp.AddTool(s, &mcp.Tool{
		Name:        "crash_list",
		Description: "List crash reports from ~/Library/Logs/DiagnosticReports",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		Query string `json:"query,omitempty"`
		Limit int    `json:"limit,omitempty"`
	}) (*mcp.CallToolResult, CrashListOutput, error) {
		opts := crash.ListOptions{
			Query: args.Query,
			Limit: args.Limit,
		}
		reports, err := crash.List(ctx, opts)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, CrashListOutput{}, nil
		}
		return &mcp.CallToolResult{}, CrashListOutput{Reports: reports}, nil
	})

	// crash_read tool
	mcp.AddTool(s, &mcp.Tool{
		Name:        "crash_read",
		Description: "Read a crash report file",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		Path string `json:"path"`
	}) (*mcp.CallToolResult, map[string]string, error) {
		content, err := crash.Read(ctx, args.Path)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
		}
		return &mcp.CallToolResult{}, map[string]string{"content": content}, nil
	})
}
