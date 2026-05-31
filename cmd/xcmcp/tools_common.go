//go:build darwin

package main

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type SimulatorActionOutput struct {
	Message string `json:"message"`
}

type ScreenshotOutput struct {
	Message  string `json:"message"`
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

type UITreeOutput struct {
	Tree string `json:"tree"`
}

// SafeTool wraps a tool handler with panic recovery.
func SafeTool[T any, R any](name string, handler func(context.Context, *mcp.CallToolRequest, T) (*mcp.CallToolResult, R, error)) func(context.Context, *mcp.CallToolRequest, T) (*mcp.CallToolResult, R, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, args T) (res *mcp.CallToolResult, out R, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic in %s: %v", name, r)
				// Return a valid error result to MCP if possible, or just the error
				if res == nil {
					res = &mcp.CallToolResult{
						IsError: true,
						Content: []mcp.Content{
							&mcp.TextContent{Text: fmt.Sprintf("Tool execution panicked: %v", r)},
						},
					}
				}
			}
		}()
		return handler(ctx, req, args)
	}
}
