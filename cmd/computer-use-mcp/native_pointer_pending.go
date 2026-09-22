package main

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/computeruse/session"
)

type nativePointerRecoveryKey struct{}
type nativePointerRecoveryStart func(nativePointerEvent) error

// recoverPointer is called under the runner gate after synchronous posting has
// finished. The worker owns an independent lease and never reenters run.
func (r *nativeRunner) recoverPointer(ctx context.Context, owner *mcp.ServerSession, handle string, target nativeTarget, lease *session.Lease, event nativePointerEvent) error {
	retained, err := lease.Retain()
	if err != nil {
		return err
	}
	root, _, err := retained.Resolve(0)
	if err != nil {
		retained.Close()
		return err
	}
	info := retained.State().ScreenshotMetadata
	err = r.pointerRecovery.start(ctx, func(ctx context.Context) (bool, error) {
		select {
		case r.gate <- struct{}{}:
			defer func() { <-r.gate }()
		case <-ctx.Done():
			return false, ctx.Err()
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		return postNativePointer(ctx, target, root, info, event, true, nil)
	}, retained.Close)
	if err != nil {
		retained.Close()
		return err
	}
	r.recoveryOwner, r.recoveryHandle = owner, handle
	r.watchNativeSession(owner)
	return nil
}

// Compatibility actions share the runner gate, including their full lease use.
func beginLegacyNativeAction(ctx context.Context, rt *runtimeState) (func(), error) {
	if rt == nil || rt.native == nil {
		return func() {}, nil
	}
	r := rt.native
	select {
	case r.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	finish := func() { <-r.gate }
	if r.closed {
		finish()
		return nil, fmt.Errorf("native runner is closed")
	}
	if err := ctx.Err(); err != nil {
		finish()
		return nil, err
	}
	if err := r.pointerRecovery.blocked(); err != nil {
		finish()
		return nil, err
	}
	return finish, nil
}
