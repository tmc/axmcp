package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/computeruse"
)

const nativeTargetLimit = 64

// A discovery and its selected targets share a window group. All ownership
// changes happen under the runner gate; the last owner closes the native set.
type nativeWindowGroup struct {
	nativeWindowSet
	refs int
}

func (g *nativeWindowGroup) Close() error {
	g.refs--
	if g.refs == 0 {
		return g.nativeWindowSet.Close()
	}
	return nil
}

type nativeRetainedTarget struct {
	windows *nativeWindowGroup
	index   int
	target  nativeTarget
}
type nativeSelectInput struct {
	SelectionID string `json:"selection_id"`
	TimeoutMS   int    `json:"timeout_ms,omitempty"`
}
type nativeSelectOutput struct {
	TargetHandle string                 `json:"target_handle"`
	App          computeruse.AppInfo    `json:"app"`
	Window       computeruse.WindowInfo `json:"window"`
	ProcessStart nativeStart            `json:"process_start"`
}
type nativeReleaseInput struct {
	TargetHandle string `json:"target_handle"`
}
type nativeReleaseOutput struct {
	Released bool `json:"released"`
}

func (r *nativeRunner) discoveryCandidate(owner *mcp.ServerSession, id string) (*nativeDiscovery, int, error) {
	d := r.discoveries[owner]
	if d == nil {
		return nil, 0, fmt.Errorf("unknown selection_id; call native_discover")
	}
	if !r.now().Before(d.expires) {
		r.clearDiscovery(owner)
		return nil, 0, fmt.Errorf("selection_id expired; call native_discover")
	}
	index, ok := d.selections[id]
	if !ok {
		return nil, 0, fmt.Errorf("unknown selection_id; call native_discover")
	}
	return d, index, nil
}
func (r *nativeRunner) selectTarget(ctx context.Context, req *mcp.CallToolRequest, in nativeSelectInput) (nativeSelectOutput, error) {
	var out nativeSelectOutput
	err := r.run(ctx, in.TimeoutMS, func(ctx context.Context) error {
		owner := nativeOwner(req)
		if len(r.targets[owner]) >= nativeTargetLimit {
			return fmt.Errorf("too many retained native targets; release a target_handle")
		}
		d, index, err := r.discoveryCandidate(owner, in.SelectionID)
		if err != nil {
			return err
		}
		backend := r.backend.(nativeDiscoveryBackend)
		access := func(app computeruse.AppInfo) error {
			permissions, approval, err := backend.DiscoveryAccess(ctx, app)
			if err != nil {
				return err
			}
			if permissions.Pending || !approval.Approved {
				return fmt.Errorf("native selection requires current permissions and app approval")
			}
			return nil
		}
		expected := d.targets[index]
		if err := access(expected.App); err != nil {
			return err
		}
		target, err := d.windows.Validate(ctx, index)
		if err != nil {
			return err
		}
		if !sameNativeInstance(target, expected) {
			return fmt.Errorf("native selection instance changed")
		}
		if err := access(target.App); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !r.now().Before(d.expires) {
			r.clearDiscovery(owner)
			return fmt.Errorf("selection_id expired during selection")
		}
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return fmt.Errorf("target handle: %w", err)
		}
		id := hex.EncodeToString(raw[:])
		if r.targets[owner] == nil {
			r.targets[owner] = make(map[string]*nativeRetainedTarget)
		}
		d.windows.refs++
		r.targets[owner][id] = &nativeRetainedTarget{windows: d.windows, index: index, target: target}
		out = nativeSelectOutput{TargetHandle: id, App: target.App, Window: target.Window, ProcessStart: target.Start}
		return nil
	})
	return out, err
}
func sameNativeInstance(a, b nativeTarget) bool {
	return a.App.PID == b.App.PID && a.Start == b.Start && a.Window.WindowID == b.Window.WindowID
}

func (r *nativeRunner) releaseTarget(owner *mcp.ServerSession, id string) bool {
	target := r.targets[owner][id]
	if target == nil {
		return false
	}
	delete(r.targets[owner], id)
	if len(r.targets[owner]) == 0 {
		delete(r.targets, owner)
	}
	if old := r.observation; old != nil && old.handle == id && old.owner == owner {
		r.observation = nil
		_ = r.store.InvalidateSession(old.output.SessionID)
	}
	_ = target.windows.Close()
	return true
}
func (r *nativeRunner) clearTargets(owner *mcp.ServerSession) {
	for id := range r.targets[owner] {
		r.releaseTarget(owner, id)
	}
}
func (r *nativeRunner) release(ctx context.Context, req *mcp.CallToolRequest, in nativeReleaseInput) (nativeReleaseOutput, error) {
	var out nativeReleaseOutput
	err := r.run(ctx, 0, func(context.Context) error {
		out.Released = r.releaseTarget(nativeOwner(req), in.TargetHandle)
		return nil
	})
	return out, err
}
func (r *nativeRunner) observeTarget(ctx context.Context, req *mcp.CallToolRequest, id string) (nativeObservationOutput, error) {
	var out nativeObservationOutput
	owner := nativeOwner(req)
	selected := r.targets[owner][id]
	if selected == nil {
		return out, fmt.Errorf("unknown target_handle; call native_select")
	}
	out.App = selected.target.App
	var err error
	out.Permissions, out.Approval, err = r.backend.Authorize(ctx, req, out.App, true)
	if err != nil || out.Permissions.Pending || !out.Approval.Approved {
		return out, err
	}
	snapshot, target, err := selected.windows.Capture(ctx, selected.index)
	if err != nil {
		return out, err
	}
	if !sameNativeInstance(target, selected.target) {
		snapshot.Close()
		return out, fmt.Errorf("native selected target instance changed")
	}
	out.Permissions, out.Approval, err = r.backend.(nativeDiscoveryBackend).DiscoveryAccess(ctx, target.App)
	if err != nil || out.Permissions.Pending || !out.Approval.Approved {
		snapshot.Close()
		return out, err
	}
	if err := ctx.Err(); err != nil {
		snapshot.Close()
		return out, err
	}
	out, err = r.publish(snapshot, target, out.Permissions, out.Approval)
	if err == nil {
		r.observation.handle = id
		r.observation.owner = owner
	}
	return out, err
}
func registerNativeTargetTools(server *mcp.Server, r *nativeRunner) {
	mcp.AddTool(server, &mcp.Tool{Name: "native_select", Description: "Retain an exact window from this client's unexpired native_discover selection_id. The target_handle survives discovery expiry and new observations until native_release or client disconnect. No screenshot, activation, or action. At most 64 retained targets per client.", Annotations: readOnlyToolAnnotations()}, func(ctx context.Context, req *mcp.CallToolRequest, in nativeSelectInput) (*mcp.CallToolResult, nativeSelectOutput, error) {
		out, err := r.selectTarget(ctx, req, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "native_release", Description: "Release this client's retained target_handle and invalidate its current observation if any. Does not close the app or window. Returns released=false when no matching handle belongs to this client.", Annotations: readOnlyToolAnnotations()}, func(ctx context.Context, req *mcp.CallToolRequest, in nativeReleaseInput) (*mcp.CallToolResult, nativeReleaseOutput, error) {
		out, err := r.release(ctx, req, in)
		return nil, out, err
	})
}
