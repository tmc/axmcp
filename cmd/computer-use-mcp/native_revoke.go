package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type nativeRevokeApprovalInput struct {
	BundleID  string `json:"bundle_id"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type nativeRevokeApprovalOutput struct {
	BundleID  string `json:"bundle_id"`
	Revoked   bool   `json:"revoked"`
	ErrorText string `json:"error_text,omitempty"`
}

type nativeApprovalRevoker interface {
	RevokeApproval(context.Context, string) error
}

func (b *nativeOSBackend) RevokeApproval(ctx context.Context, bundleID string) error {
	if b.rt == nil || b.rt.approvals == nil {
		return fmt.Errorf("app approval store unavailable")
	}
	return b.rt.approvals.Revoke(ctx, bundleID)
}

func (r *nativeRunner) revokeApproval(ctx context.Context, in nativeRevokeApprovalInput) (nativeRevokeApprovalOutput, error) {
	out := nativeRevokeApprovalOutput{BundleID: strings.ToLower(strings.TrimSpace(in.BundleID))}
	if out.BundleID == "" {
		return out, fmt.Errorf("bundle_id required")
	}
	err := r.run(ctx, in.TimeoutMS, func(ctx context.Context) error {
		revoker, ok := r.backend.(nativeApprovalRevoker)
		if !ok {
			out.ErrorText = "app approval revocation unavailable"
			return nil
		}
		if err := revoker.RevokeApproval(ctx, out.BundleID); err != nil {
			out.ErrorText = err.Error()
			return ctx.Err()
		}
		// Other backends reauthorize against shared authority. This backend can also
		// discard its current matching observation immediately, under the runner gate.
		if current := r.observation; current != nil && strings.EqualFold(current.target.App.BundleID, out.BundleID) {
			r.observation = nil
			if err := r.store.InvalidateSession(current.output.SessionID); err != nil {
				out.ErrorText = fmt.Sprintf("approval revoked; invalidate local observation: %v", err)
				return nil
			}
		}
		out.Revoked = true
		return nil
	})
	return out, err
}

func registerNativeRevokeTool(server *mcp.Server, r *nativeRunner) {
	mcp.AddTool(server, &mcp.Tool{Name: "native_revoke_approval", Description: "Withdraw app approval by exact bundle_id, including stopped apps. Revokes persistent and session grants for cooperating backends sharing the approval store. Does not prompt, launch, capture, grant or revoke macOS permissions, or undo dispatched actions. revoked=false with error_text means withdrawal was not confirmed; inspect state and never automatically retry."}, func(ctx context.Context, _ *mcp.CallToolRequest, in nativeRevokeApprovalInput) (*mcp.CallToolResult, nativeRevokeApprovalOutput, error) {
		out, err := r.revokeApproval(ctx, in)
		return nil, out, err
	})
}
