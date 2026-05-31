//go:build !darwin

package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/computeruse"
)

func registerPermissionResource(server *mcp.Server) {
	server.AddResource(&mcp.Resource{
		URI:         "mcp://platform/status",
		Name:        "platform-status",
		Description: "Current native automation backend status for computer-use-mcp",
		MIMEType:    "application/json",
	}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		data, err := json.MarshalIndent(computeruse.PlatformStatus(), "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshal platform status: %w", err)
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      "mcp://platform/status",
					MIMEType: "application/json",
					Text:     string(data),
				},
			},
		}, nil
	})
}
