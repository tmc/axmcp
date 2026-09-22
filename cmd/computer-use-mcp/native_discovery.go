package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/session"
)

const nativeDiscoveryLimit = 128

type nativeDiscoverInput struct {
	App       string `json:"app"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type nativeDiscoveredWindow struct {
	computeruse.WindowInfo
	SelectionID string `json:"selection_id"`
}

type nativeDiscoverOutput struct {
	App          computeruse.AppInfo         `json:"app"`
	ProcessStart nativeStart                 `json:"process_start"`
	Permissions  computeruse.PermissionState `json:"permissions"`
	Approval     computeruse.ApprovalState   `json:"approval"`
	Windows      []nativeDiscoveredWindow    `json:"windows"`
	Truncated    bool                        `json:"truncated"`
}

// A window set owns the discovery handles. Captures independently own their
// snapshots; replacing or expiring a set cannot close a published snapshot.
type nativeWindowSet interface {
	Targets() []nativeTarget
	ProcessStart() nativeStart
	Truncated() bool
	Capture(context.Context, int) (session.Snapshot, nativeTarget, error)
	Close() error
}

type nativeDiscoveryBackend interface {
	DiscoveryAccess(context.Context, computeruse.AppInfo) (computeruse.PermissionState, computeruse.ApprovalState, error)
	Discover(context.Context, computeruse.AppInfo, int) (nativeWindowSet, error)
}

type nativeDiscovery struct {
	windows    nativeWindowSet
	targets    []nativeTarget
	selections map[string]int
	expires    time.Time
	timer      *time.Timer
}

func nativeOwner(req *mcp.CallToolRequest) *mcp.ServerSession {
	if req == nil {
		return nil
	}
	return req.Session
}

// watchNativeSession is called under the runner gate. There is one waiter per
// connected client, not one per discovery refresh.
func (r *nativeRunner) watchNativeSession(owner *mcp.ServerSession) {
	if owner == nil || r.watched[owner] {
		return
	}
	r.watched[owner] = true
	go func() {
		_ = owner.Wait()
		r.gate <- struct{}{}
		defer func() { <-r.gate }()
		r.clearDiscovery(owner)
		delete(r.watched, owner)
	}()
}

func (r *nativeRunner) clearDiscovery(owner *mcp.ServerSession) {
	if d := r.discoveries[owner]; d != nil {
		delete(r.discoveries, owner)
		if d.timer != nil {
			d.timer.Stop()
		}
		_ = d.windows.Close()
	}
}

func (r *nativeRunner) expireDiscovery(owner *mcp.ServerSession, d *nativeDiscovery) {
	r.gate <- struct{}{}
	defer func() { <-r.gate }()
	if r.discoveries[owner] == d && !r.now().Before(d.expires) {
		r.clearDiscovery(owner)
	}
}

func (r *nativeRunner) close() error {
	r.gate <- struct{}{}
	defer func() { <-r.gate }()
	if r.closed {
		return nil
	}
	r.closed = true
	for owner := range r.discoveries {
		r.clearDiscovery(owner)
	}
	r.observation = nil
	return r.store.Close()
}

func (r *nativeRunner) discover(ctx context.Context, req *mcp.CallToolRequest, in nativeDiscoverInput) (nativeDiscoverOutput, error) {
	out := nativeDiscoverOutput{Windows: []nativeDiscoveredWindow{}}
	err := r.run(ctx, in.TimeoutMS, func(ctx context.Context) error {
		backend, ok := r.backend.(nativeDiscoveryBackend)
		if !ok {
			return fmt.Errorf("native discovery is unavailable")
		}
		owner := nativeOwner(req)
		app, err := r.backend.Resolve(ctx, in.App)
		if err != nil {
			return err
		}
		out.App = app
		out.Permissions, out.Approval, err = backend.DiscoveryAccess(ctx, app)
		if err != nil {
			return err
		}
		if out.Permissions.Pending || !out.Approval.Approved {
			if err := ctx.Err(); err != nil {
				return err
			}
			r.clearDiscovery(owner)
			return nil
		}
		windows, err := backend.Discover(ctx, app, nativeDiscoveryLimit)
		if err != nil {
			return err
		}
		keep := false
		defer func() {
			if !keep {
				windows.Close()
			}
		}()
		targets := windows.Targets()
		out.ProcessStart = windows.ProcessStart()
		if len(targets) > nativeDiscoveryLimit {
			return fmt.Errorf("native discovery exceeded window limit")
		}
		d := &nativeDiscovery{windows: windows, targets: targets, selections: make(map[string]int), expires: r.now().Add(r.discoveryTTL)}
		for i, target := range targets {
			if target.App.PID != app.PID || target.Window.WindowID == 0 || target.Start != out.ProcessStart {
				return fmt.Errorf("invalid discovery target")
			}
			if i > 0 && target.Start != targets[0].Start {
				return fmt.Errorf("process changed during discovery")
			}
			var raw [16]byte
			if _, err := rand.Read(raw[:]); err != nil {
				return fmt.Errorf("selection id: %w", err)
			}
			id := hex.EncodeToString(raw[:])
			d.selections[id] = i
			out.Windows = append(out.Windows, nativeDiscoveredWindow{WindowInfo: target.Window, SelectionID: id})
			out.ProcessStart = target.Start
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		out.Truncated = windows.Truncated()
		r.clearDiscovery(owner)
		r.discoveries[owner] = d
		keep = true
		r.watchNativeSession(owner)
		d.timer = time.AfterFunc(r.discoveryTTL, func() { r.expireDiscovery(owner, d) })
		return nil
	})
	return out, err
}

// observeSelection runs under the runner gate, which keeps candidate ownership
// through capture and prevents expiry/disconnect from releasing live handles.
func (r *nativeRunner) observeSelection(ctx context.Context, req *mcp.CallToolRequest, id string) (nativeObservationOutput, error) {
	var out nativeObservationOutput
	owner := nativeOwner(req)
	d := r.discoveries[owner]
	if d == nil {
		return out, fmt.Errorf("unknown selection_id; call native_discover")
	}
	if !r.now().Before(d.expires) {
		r.clearDiscovery(owner)
		return out, fmt.Errorf("selection_id expired; call native_discover")
	}
	index, ok := d.selections[id]
	if !ok {
		return out, fmt.Errorf("unknown selection_id; call native_discover")
	}
	expected := d.targets[index]
	out.App = expected.App
	var err error
	out.Permissions, out.Approval, err = r.backend.Authorize(ctx, req, expected.App, true)
	if err != nil {
		return out, err
	}
	if out.Permissions.Pending || !out.Approval.Approved {
		return out, nil
	}
	snapshot, target, err := d.windows.Capture(ctx, index)
	if err != nil {
		return out, err
	}
	if target.App.PID != expected.App.PID || target.Start != expected.Start || target.Window.WindowID != expected.Window.WindowID {
		snapshot.Close()
		return out, fmt.Errorf("native selection instance changed")
	}
	backend := r.backend.(nativeDiscoveryBackend)
	out.Permissions, out.Approval, err = backend.DiscoveryAccess(ctx, target.App)
	if err != nil || out.Permissions.Pending || !out.Approval.Approved {
		snapshot.Close()
		return out, err
	}
	if err := ctx.Err(); err != nil {
		snapshot.Close()
		return out, err
	}
	if !r.now().Before(d.expires) {
		snapshot.Close()
		r.clearDiscovery(owner)
		return out, fmt.Errorf("selection_id expired during capture")
	}
	return r.publish(snapshot, target, out.Permissions, out.Approval)
}
