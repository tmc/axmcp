//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/ui/permissions"
)

func registerPermissionResource(server *mcp.Server) {
	server.AddResource(&mcp.Resource{
		URI:         "mcp://permissions/status",
		Name:        "permissions-status",
		Description: "Current aggregated permission status for computer-use-mcp",
		MIMEType:    "application/json",
	}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		snapshot := permissions.CurrentSnapshot(permissions.ReqAccessibility, permissions.ReqScreenRecording)
		data, err := json.MarshalIndent(snapshot, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshal permissions status: %w", err)
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      "mcp://permissions/status",
					MIMEType: "application/json",
					Text:     string(data),
				},
			},
		}, nil
	})
}
