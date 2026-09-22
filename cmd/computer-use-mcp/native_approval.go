package main

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/computeruse"
)

type nativeRequestApprovalInput struct {
	App       string `json:"app"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type nativeRequestApprovalOutput struct {
	App         computeruse.AppInfo         `json:"app"`
	Permissions computeruse.PermissionState `json:"permissions"`
	Approval    computeruse.ApprovalState   `json:"approval"`
	ErrorText   string                      `json:"error_text,omitempty"`
}

func (r *nativeRunner) requestApproval(ctx context.Context, req *mcp.CallToolRequest, in nativeRequestApprovalInput) (nativeRequestApprovalOutput, error) {
	var out nativeRequestApprovalOutput
	err := r.run(ctx, in.TimeoutMS, func(ctx context.Context) error {
		app, err := r.backend.Resolve(ctx, in.App)
		out.App = app
		return err
	})
	if err != nil {
		return out, err
	}
	// The prompt waits on a person, so it runs outside the runner's gate and
	// deadline, which would otherwise stall other tools and cut the person
	// off. The client bounds it by cancelling the request.
	out.Permissions, out.Approval, err = r.backend.Authorize(ctx, req, out.App, true)
	if err != nil {
		out.ErrorText = err.Error()
	}
	// Preserve denied/canceled/persistence status without minting a window
	// handle or observation. Transport cancellation remains an error.
	return out, ctx.Err()
}

func registerNativeApprovalTool(server *mcp.Server, r *nativeRunner) {
	mcp.AddTool(server, &mcp.Tool{Name: "native_request_approval", Description: "Explicitly request persistent app approval for exactly one running app. Does not launch, activate, capture or act on the app, and does not grant macOS permissions. Accept stores approval for future sessions; decline or cancel grants no approval."},
		func(ctx context.Context, req *mcp.CallToolRequest, in nativeRequestApprovalInput) (*mcp.CallToolResult, nativeRequestApprovalOutput, error) {
			out, err := r.requestApproval(ctx, req, in)
			return nil, out, err
		})
}
