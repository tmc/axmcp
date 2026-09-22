package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/input"
	"github.com/tmc/axmcp/internal/computeruse/session"
)

type nativeStart struct {
	Seconds      int64 `json:"seconds"`
	Microseconds int64 `json:"microseconds"`
}
type nativeTarget struct {
	App    computeruse.AppInfo
	Start  nativeStart
	Window computeruse.WindowInfo
}
type nativeObserveInput struct {
	App         string `json:"app,omitempty"`
	SelectionID string `json:"selection_id,omitempty"`
	WindowID    uint32 `json:"window_id,omitempty"`
	TimeoutMS   int    `json:"timeout_ms,omitempty"`
}
type nativeObservationOutput struct {
	computeruse.AppState
	TargetID     string      `json:"target_id,omitempty"`
	ProcessStart nativeStart `json:"process_start"`
}
type nativeExpect struct {
	Identifier string `json:"identifier"`
	Attribute  string `json:"attribute"`
	Text       string `json:"text"`
}
type nativeActInput struct {
	Point           *nativePoint  `json:"point,omitempty"`
	FromPoint       *nativePoint  `json:"from_point,omitempty"`
	ToPoint         *nativePoint  `json:"to_point,omitempty"`
	ImageID         string        `json:"image_id,omitempty"`
	MouseButton     string        `json:"mouse_button,omitempty"`
	ClickCount      int           `json:"click_count,omitempty"`
	DurationMS      int           `json:"duration_ms,omitempty"`
	StateID         string        `json:"state_id"`
	TargetID        string        `json:"target_id"`
	Action          string        `json:"action"`
	ElementIndex    *int          `json:"element_index,omitempty"`
	Text            string        `json:"text,omitempty"`
	Value           *string       `json:"value,omitempty"`
	Key             string        `json:"key,omitempty"`
	Direction       string        `json:"direction,omitempty"`
	Pages           float64       `json:"pages,omitempty"`
	SecondaryAction string        `json:"secondary_action,omitempty"`
	Expect          *nativeExpect `json:"expect,omitempty"`
	TimeoutMS       int           `json:"timeout_ms,omitempty"`
}
type nativeActOutput struct {
	Execution     string                   `json:"execution"`
	Observation   string                   `json:"observation"`
	Postcondition string                   `json:"postcondition"`
	FreshState    *nativeObservationOutput `json:"fresh_state,omitempty"`
	ErrorText     string                   `json:"error_text,omitempty"`
}

// nativeBackend is used by the real OS adapter and deterministic tests alike.
// Perform reports whether a side effect was attempted; nil error means that the
// complete requested native call sequence returned, not that the app processed it.
type nativeBackend interface {
	Resolve(context.Context, string) (computeruse.AppInfo, error)
	Authorize(context.Context, *mcp.CallToolRequest, computeruse.AppInfo, bool) (computeruse.PermissionState, computeruse.ApprovalState, error)
	Capture(context.Context, computeruse.AppInfo, uint32) (session.Snapshot, nativeTarget, error)
	Check(context.Context, nativeTarget, *session.Lease, nativeActInput) error
	Perform(context.Context, nativeTarget, *session.Lease, nativeActInput) (bool, error)
}
type nativeObservation struct {
	output nativeObservationOutput
	target nativeTarget
}
type nativeRunner struct {
	backend      nativeBackend
	store        *session.Store
	gate         chan struct{}
	observation  *nativeObservation
	discoveries  map[*mcp.ServerSession]*nativeDiscovery
	watched      map[*mcp.ServerSession]bool
	now          func() time.Time
	discoveryTTL time.Duration
	closed       bool
}

func newNativeRunner(backend nativeBackend) *nativeRunner {
	return &nativeRunner{backend: backend, store: session.NewStore(), gate: make(chan struct{}, 1), discoveries: make(map[*mcp.ServerSession]*nativeDiscovery), watched: make(map[*mcp.ServerSession]bool), now: time.Now, discoveryTTL: 60 * time.Second}
}
func (r *nativeRunner) run(ctx context.Context, ms int, f func(context.Context) error) error {
	if ms < 0 || ms > 60000 {
		return fmt.Errorf("timeout_ms must be between 0 and 60000")
	}
	if ms == 0 {
		ms = 30000
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(ms)*time.Millisecond)
	defer cancel()
	select {
	case r.gate <- struct{}{}:
		defer func() { <-r.gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.closed {
		return fmt.Errorf("native runner is closed")
	}
	return f(ctx)
}
func (r *nativeRunner) observe(ctx context.Context, req *mcp.CallToolRequest, in nativeObserveInput) (nativeObservationOutput, error) {
	var out nativeObservationOutput
	err := r.run(ctx, in.TimeoutMS, func(ctx context.Context) error {
		if in.SelectionID != "" {
			if in.App != "" || in.WindowID != 0 {
				return fmt.Errorf("selection_id cannot be combined with app or window_id")
			}
			var err error
			out, err = r.observeSelection(ctx, req, in.SelectionID)
			return err
		}
		info, err := r.backend.Resolve(ctx, in.App)
		if err != nil {
			return err
		}
		out.App = info
		out.Permissions, out.Approval, err = r.backend.Authorize(ctx, req, info, true)
		if err != nil {
			return err
		}
		if out.Permissions.Pending || !out.Approval.Approved {
			return nil
		}
		snapshot, target, err := r.backend.Capture(ctx, info, in.WindowID)
		if err != nil {
			return err
		}
		out, err = r.publish(snapshot, target, out.Permissions, out.Approval)
		return err
	})
	return out, err
}

type nativeStateSnapshot struct {
	session.Snapshot
	state computeruse.AppState
}

func (s *nativeStateSnapshot) State() computeruse.AppState { return s.state }
func (r *nativeRunner) publish(snapshot session.Snapshot, target nativeTarget, permissions computeruse.PermissionState, approval computeruse.ApprovalState) (nativeObservationOutput, error) {
	var out nativeObservationOutput
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		snapshot.Close()
		return out, fmt.Errorf("target id: %w", err)
	}
	state := snapshot.State()
	state.Permissions, state.Approval = permissions, approval
	state, err := r.store.Bind(&nativeStateSnapshot{Snapshot: snapshot, state: state})
	if err != nil {
		return out, err
	}
	if old := r.observation; old != nil && old.output.SessionID != state.SessionID {
		_ = r.store.InvalidateSession(old.output.SessionID)
	}
	out = nativeObservationOutput{AppState: state, TargetID: hex.EncodeToString(raw[:]), ProcessStart: target.Start}
	r.observation = &nativeObservation{output: out, target: target}
	return out, nil
}
func (r *nativeRunner) act(ctx context.Context, req *mcp.CallToolRequest, in nativeActInput) nativeActOutput {
	out := nativeActOutput{Execution: "not_dispatched", Observation: "unavailable", Postcondition: "not_requested"}
	if in.Expect != nil {
		out.Postcondition = "unknown"
	}
	err := r.run(ctx, in.TimeoutMS, func(ctx context.Context) error {
		old := r.observation
		if old == nil || in.StateID == "" || old.output.StateID != in.StateID {
			return fmt.Errorf("unknown or consumed state_id; call native_observe")
		}
		r.observation = nil
		lease, err := r.store.Take(in.StateID)
		if err != nil {
			return err
		}
		defer lease.Close()
		if in.TargetID != old.output.TargetID {
			return fmt.Errorf("observation belongs to a different target")
		}
		if err := validateNativeInput(in); err != nil {
			return err
		}
		if err := validateNativePointerImage(in, lease.State()); err != nil {
			return err
		}
		permissions, approval, err := r.backend.Authorize(ctx, req, old.target.App, false)
		if err != nil {
			return err
		}
		if permissions.Pending || !approval.Approved {
			return fmt.Errorf("native action requires current permissions and app approval")
		}
		if err := r.backend.Check(ctx, old.target, lease, in); err != nil {
			return err
		}
		attempted, actionErr := r.backend.Perform(ctx, old.target, lease, in)
		if attempted {
			out.Execution = "dispatched_unknown"
		}
		if actionErr == nil {
			out.Execution = "completed"
		} else {
			out.ErrorText = actionErr.Error()
		}
		snapshot, target, captureErr := r.backend.Capture(ctx, old.target.App, old.target.Window.WindowID)
		if captureErr != nil {
			appendNativeError(&out, "post-action observation", captureErr)
			return nil
		}
		if target.Start != old.target.Start || target.App.PID != old.target.App.PID || target.Window.WindowID != old.target.Window.WindowID {
			snapshot.Close()
			appendNativeError(&out, "post-action observation", fmt.Errorf("native target instance changed"))
			return nil
		}
		permissions, approval, authErr := r.backend.Authorize(ctx, req, target.App, false)
		if authErr != nil || permissions.Pending || !approval.Approved {
			snapshot.Close()
			if authErr == nil {
				authErr = fmt.Errorf("native authorization changed")
			}
			appendNativeError(&out, "post-action observation", authErr)
			return nil
		}
		fresh, publishErr := r.publish(snapshot, target, permissions, approval)
		if publishErr != nil {
			appendNativeError(&out, "post-action observation", publishErr)
			return nil
		}
		out.Observation = "captured"
		out.FreshState = &fresh
		if in.Expect != nil {
			if nativeMatches(fresh.Tree, *in.Expect) {
				out.Postcondition = "met"
			} else {
				out.Postcondition = "unmet"
			}
		}
		return nil
	})
	if err != nil {
		appendNativeError(&out, "", err)
	}
	return out
}
func appendNativeError(out *nativeActOutput, where string, err error) {
	if out.ErrorText != "" {
		out.ErrorText += "; "
	}
	if where != "" {
		out.ErrorText += where + ": "
	}
	out.ErrorText += err.Error()
}
func validateNativeInput(in nativeActInput) error {
	if err := validateNativePointerInput(in); err != nil {
		return err
	}
	switch in.Action {
	case "click":
		if in.Point == nil && (in.ElementIndex == nil || *in.ElementIndex < 0) {
			return fmt.Errorf("click requires element_index or point")
		}
	case "drag":
	case "type_text", "set_value", "scroll", "secondary_action":
		if in.ElementIndex == nil || *in.ElementIndex < 0 {
			return fmt.Errorf("action requires a nonnegative element_index")
		}
	case "press_key":
		if _, err := input.ParseKeyCombo(in.Key); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported native action %q", in.Action)
	}
	if in.Action == "type_text" && in.Text == "" {
		return fmt.Errorf("type_text requires nonempty text")
	}
	if in.Action == "set_value" && in.Value == nil {
		return fmt.Errorf("set_value requires value")
	}
	if in.Action == "secondary_action" && in.SecondaryAction == "" {
		return fmt.Errorf("secondary_action requires its exact name")
	}
	if in.Action == "scroll" {
		if in.Direction != "up" && in.Direction != "down" && in.Direction != "left" && in.Direction != "right" {
			return fmt.Errorf("invalid scroll direction")
		}
		if in.Pages <= 0 || in.Pages > float64(math.MaxInt32)/12 || math.IsNaN(in.Pages) || math.IsInf(in.Pages, 0) {
			return fmt.Errorf("pages must be finite and positive")
		}
	}
	if in.Expect != nil && (in.Expect.Identifier == "" || (in.Expect.Attribute != "value" && in.Expect.Attribute != "title")) {
		return fmt.Errorf("expect requires an identifier and value or title attribute")
	}
	return nil
}
func nativeMatches(nodes []computeruse.ElementNode, expect nativeExpect) bool {
	matches := 0
	value := ""
	for _, node := range nodes {
		if node.Identifier != expect.Identifier {
			continue
		}
		matches++
		if expect.Attribute == "value" {
			value = node.Value
		} else {
			value = node.Title
		}
	}
	return matches == 1 && value == expect.Text
}
func registerNativeTools(server *mcp.Server, r *nativeRunner) {
	mcp.AddTool(server, &mcp.Tool{Name: "native_discover", Description: "List windows of exactly one running app without launching, activating, capturing screenshots, or requesting new approval. Returns session-scoped selection_id tokens valid for 60 seconds; a new discovery replaces this client’s previous candidates. At most 128 windows, with truncated=true when more exist. Discovery does not invalidate the current observation.", Annotations: readOnlyToolAnnotations()},
		func(ctx context.Context, req *mcp.CallToolRequest, in nativeDiscoverInput) (*mcp.CallToolResult, nativeDiscoverOutput, error) {
			out, err := r.discover(ctx, req, in)
			return nil, out, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "native_observe", Description: "Observe an exact running app and window. Use selection_id from native_discover, or a unique full app name, bundle ID or PID; never combine the modes. A missing window_id selects the focused window only. Returns opaque state_id and target_id; denied permissions grant no action token.", Annotations: readOnlyToolAnnotations()},
		func(ctx context.Context, req *mcp.CallToolRequest, in nativeObserveInput) (*mcp.CallToolResult, nativeObservationOutput, error) {
			out, err := r.observe(ctx, req, in)
			if err != nil {
				return nil, out, err
			}
			image, err := nativeImageContent(&out.AppState)
			if err != nil {
				return nil, out, err
			}
			result, err := nativeToolResult(out, image)
			return result, out, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "native_act", Description: "Consume a native_observe state_id and target_id for a native action. Uses an exact observed element_index or image_id plus PNG-pixel point for click, or from_point/to_point for drag. Pointer mode accepts left/right/middle mouse_button; click_count 1..3 or drag duration_ms 100..5000. Never retargets by name. Execution reports native call completion, not app effect. Read observation and postcondition separately; never replay an uncertain action. timeout_ms defaults to 30000, maximum 60000.", Annotations: actionToolAnnotations()},
		func(ctx context.Context, req *mcp.CallToolRequest, in nativeActInput) (*mcp.CallToolResult, nativeActOutput, error) {
			return nativeActResult(r.act(ctx, req, in))
		})
}
