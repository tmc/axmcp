package main

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type nativeRecoverPointerInput struct {
	Mode       string `json:"mode"`
	RecoveryID string `json:"recovery_id,omitempty"`
	TimeoutMS  int    `json:"timeout_ms,omitempty"`
}

type nativeRecoverPointerOutput struct {
	RecoveryID string `json:"recovery_id,omitempty"`
	Status     string `json:"status"`
	Retryable  bool   `json:"retryable"`
	ErrorText  string `json:"error_text,omitempty"`
}

func (r *nativeRunner) recoverPointerTool(ctx context.Context, req *mcp.CallToolRequest, in nativeRecoverPointerInput) (nativeRecoverPointerOutput, error) {
	var out nativeRecoverPointerOutput
	if in.Mode != "status" && in.Mode != "retry" {
		return out, fmt.Errorf("mode must be status or retry")
	}
	err := r.run(ctx, in.TimeoutMS, func(ctx context.Context) error {
		id, status, _, _ := r.pointerRecovery.details()
		if id != "" && r.recoveryOwner != nativeOwner(req) {
			return fmt.Errorf("pointer recovery belongs to another client")
		}
		if in.RecoveryID != "" && in.RecoveryID != id {
			return fmt.Errorf("unknown pointer recovery")
		}
		if in.Mode == "retry" {
			if err := r.pointerRecovery.retry(ctx, in.RecoveryID); err != nil {
				return err
			}
		} else if id == "" {
			out.Status = "none"
			return nil
		}
		var err error
		out.RecoveryID, status, out.Retryable, err = r.pointerRecovery.details()
		out.Status = status
		if err != nil {
			out.ErrorText = err.Error()
		}
		return nil
	})
	return out, err
}

func registerNativePointerRecoveryTool(server *mcp.Server, r *nativeRunner) {
	mcp.AddTool(server, &mcp.Tool{Name: "native_recover_pointer", Description: "Inspect this client's owned pointer recovery (mode=status) or explicitly retry the same confirmed-unsent mouse-up (mode=retry with exact recovery_id). Retry starts another bounded 30-second round and returns current status; poll status to observe completion. It never issues a new mouse-down, changes targets, or retries a possibly sent event. Pending or unresolved recovery blocks further actions. Target release and disconnect close the retained recovery and prohibit further retries; an unresolved recovery then keeps blocking actions until the server restarts."}, func(ctx context.Context, req *mcp.CallToolRequest, in nativeRecoverPointerInput) (*mcp.CallToolResult, nativeRecoverPointerOutput, error) {
		out, err := r.recoverPointerTool(ctx, req, in)
		return nil, out, err
	})
}
