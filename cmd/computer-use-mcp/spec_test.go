package main

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/computeruse"
	"github.com/tmc/axmcp/internal/computeruse/intervention"
	"github.com/tmc/axmcp/internal/computeruse/policy"
	"github.com/tmc/axmcp/internal/computeruse/session"
)

func TestComputerUseSpecParity(t *testing.T) {
	ctx := context.Background()
	cs := newTestClientSession(t, ctx)

	initRes := cs.InitializeResult()
	if initRes == nil {
		t.Fatal("InitializeResult() = nil")
	}
	if initRes.Instructions != computerUseInstructions() {
		t.Fatalf("initialize instructions mismatch\n got: %q\nwant: %q", initRes.Instructions, computerUseInstructions())
	}
	if initRes.Capabilities == nil || initRes.Capabilities.Tools == nil {
		t.Fatal("initialize capabilities missing tools")
	}
	if initRes.Capabilities.Tools.ListChanged {
		t.Fatal("tools.listChanged = true, want false")
	}
	if initRes.Capabilities.Resources == nil || !initRes.Capabilities.Resources.ListChanged {
		t.Fatal("resources.listChanged = false, want true")
	}

	got, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	want := orderedComputerUseTools()
	if len(got.Tools) != len(want)+3 {
		t.Fatalf("tool count=%d, want %d", len(got.Tools), len(want)+3)
	}
	extra := map[string]bool{}
	for _, tool := range got.Tools[len(want):] {
		extra[tool.Name] = true
	}
	if !extra["native_observe"] || !extra["native_act"] || !extra["native_discover"] {
		t.Fatalf("native tools missing: %v", extra)
	}
	if !reflect.DeepEqual(normalizeJSON(t, got.Tools[:len(want)]), normalizeJSON(t, want)) {
		gotJSON, _ := json.MarshalIndent(normalizeJSON(t, got.Tools), "", "  ")
		wantJSON, _ := json.MarshalIndent(normalizeJSON(t, want), "", "  ")
		t.Fatalf("tools/list mismatch\n got: %s\nwant: %s", gotJSON, wantJSON)
	}
}

func TestComputerUsePermissionsResource(t *testing.T) {
	ctx := context.Background()
	cs := newTestClientSession(t, ctx)

	res, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(res.Resources) != 1 || res.Resources[0].URI != "mcp://permissions/status" {
		t.Fatalf("ListResources = %#v, want permissions status resource", res.Resources)
	}
	read, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "mcp://permissions/status"})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(read.Contents) != 1 || !strings.Contains(read.Contents[0].Text, "\"accessibility\"") {
		t.Fatalf("ReadResource contents = %#v, want JSON snapshot", read.Contents)
	}
	if _, err := cs.ListResourceTemplates(ctx, nil); err == nil || !strings.Contains(err.Error(), "Method not found") {
		t.Fatalf("ListResourceTemplates error = %v, want method not found", err)
	}
}

func TestRequiresRefreshResult(t *testing.T) {
	res, payload, err := requiresRefreshResult("click", "Brave")
	if err != nil {
		t.Fatalf("requiresRefreshResult error = %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("requiresRefreshResult result = %#v, want tool error", res)
	}
	action, ok := payload.(computeruse.ActionResult)
	if !ok {
		t.Fatalf("payload type = %T, want ActionResult", payload)
	}
	if !action.RequiresRefresh {
		t.Fatalf("RequiresRefresh = false, want true")
	}
	if !strings.Contains(action.Message, "call get_app_state again") {
		t.Fatalf("Message = %q, want refresh guidance", action.Message)
	}
}

func TestActionBlockedForIntervention(t *testing.T) {
	monitor := intervention.New(intervention.Config{Enabled: true, QuietPeriod: time.Second})
	monitor.Record("KCGEventKeyDown", time.Now())
	rt := &runtimeState{intervention: monitor}

	res, payload, ok := actionBlockedForIntervention(rt, "click")
	if !ok {
		t.Fatalf("actionBlockedForIntervention ok = false, want true")
	}
	if res == nil || !res.IsError {
		t.Fatalf("result = %#v, want tool error", res)
	}
	if !payload.RequiresRefresh {
		t.Fatalf("RequiresRefresh = false, want true")
	}
}

func TestStateForActionRequiresFreshStateID(t *testing.T) {
	rt := &runtimeState{sessions: session.NewStore()}
	if _, err := stateForAction(rt, "click", "Finder", ""); err == nil {
		t.Fatalf("stateForAction without state_id = nil, want error")
	}

	state, err := rt.sessions.Bind(fakeActionSnapshot{state: computeruse.AppState{
		App: computeruse.AppInfo{Name: "Finder", BundleID: "com.apple.finder", PID: 123},
	}})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	got, err := stateForAction(rt, "click", "Finder", state.StateID)
	if err != nil {
		t.Fatalf("stateForAction fresh state: %v", err)
	}
	defer got.Close()
	if got.State().StateID != state.StateID {
		t.Fatalf("StateID = %q, want %q", got.State().StateID, state.StateID)
	}
	if _, err := stateForAction(rt, "click", "Safari", state.StateID); err == nil {
		t.Fatalf("stateForAction mismatched app = nil, want error")
	}
}

func TestStateForActionAppliesURLPolicy(t *testing.T) {
	rt := &runtimeState{
		sessions:  session.NewStore(),
		urlPolicy: policy.NewURLPolicy([]string{"example.com"}),
	}
	state, err := rt.sessions.Bind(fakeActionSnapshot{state: computeruse.AppState{
		App: computeruse.AppInfo{Name: "Brave Browser", BundleID: "com.brave.Browser", PID: 123},
		Tree: []computeruse.ElementNode{{
			Role:        "AXTextField",
			Description: "Address and search bar",
			Value:       "https://example.com",
		}},
	}})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if _, err := stateForAction(rt, "click", "Brave", state.StateID); err == nil {
		t.Fatalf("stateForAction with blocked URL = nil, want error")
	}
}

type fakeActionSnapshot struct {
	state computeruse.AppState
}

func (f fakeActionSnapshot) State() computeruse.AppState {
	return f.state
}

func (f fakeActionSnapshot) Resolve(index int) (*axuiautomation.Element, computeruse.ElementNode, error) {
	return nil, computeruse.ElementNode{Index: index}, nil
}

func (f fakeActionSnapshot) Close() error {
	return nil
}

func newTestClientSession(t *testing.T, ctx context.Context) *mcp.ClientSession {
	t.Helper()

	server := newComputerUseServer(&runtimeState{})

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
	})
	return cs
}

func normalizeJSON(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	return out
}

func TestStateForActionRetainsSnapshot(t *testing.T) {
	rt := &runtimeState{sessions: session.NewStore()}
	defer rt.sessions.Close()
	snapshot := &leasedActionSnapshot{state: computeruse.AppState{App: computeruse.AppInfo{Name: "Finder", BundleID: "com.apple.finder", PID: 123}}}
	state, err := rt.sessions.Bind(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := stateForAction(rt, "click", "Finder", state.StateID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if _, err := rt.sessions.Bind(fakeActionSnapshot{state: snapshot.state}); err != nil {
		t.Fatal(err)
	}
	if snapshot.closed {
		t.Fatal("snapshot released while action holds lease")
	}
	if _, _, err := lease.Resolve(1); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if !snapshot.closed {
		t.Fatal("snapshot not released after action")
	}
}

type leasedActionSnapshot struct {
	state  computeruse.AppState
	closed bool
}

func (s *leasedActionSnapshot) State() computeruse.AppState { return s.state }
func (s *leasedActionSnapshot) Resolve(index int) (*axuiautomation.Element, computeruse.ElementNode, error) {
	if s.closed {
		return nil, computeruse.ElementNode{}, fmt.Errorf("closed snapshot")
	}
	return nil, computeruse.ElementNode{Index: index}, nil
}
func (s *leasedActionSnapshot) Close() error { s.closed = true; return nil }

func TestStateForActionRejectionReleasesLease(t *testing.T) {
	for _, name := range []string{"app", "url"} {
		t.Run(name, func(t *testing.T) {
			rt := &runtimeState{sessions: session.NewStore(), urlPolicy: policy.NewURLPolicy([]string{"example.com"})}
			defer rt.sessions.Close()
			snapshot := &leasedActionSnapshot{state: computeruse.AppState{
				App: computeruse.AppInfo{Name: "Brave Browser", BundleID: "com.brave.Browser", PID: 123},
			}}
			app := "wrong app"
			if name == "url" {
				app = "Brave"
				snapshot.state.Tree = []computeruse.ElementNode{{Role: "AXTextField", Description: "Address and search bar", Value: "https://example.com"}}
			}
			state, err := rt.sessions.Bind(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if lease, err := stateForAction(rt, "click", app, state.StateID); err == nil {
				lease.Close()
				t.Fatal("invalid action accepted")
			}
			if err := rt.sessions.InvalidateSession(state.SessionID); err != nil {
				t.Fatal(err)
			}
			if !snapshot.closed {
				t.Fatal("rejected action leaked its lease")
			}
		})
	}
}
