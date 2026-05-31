package main

import (
	"context"
	"encoding/json"
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
	if !reflect.DeepEqual(normalizeJSON(t, got.Tools), normalizeJSON(t, want)) {
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

func TestRefreshResultsRequireFreshState(t *testing.T) {
	tests := []struct {
		name    string
		call    func() (*mcp.CallToolResult, any, error)
		action  string
		message []string
	}{
		{
			name:   "missing app state",
			call:   func() (*mcp.CallToolResult, any, error) { return requiresRefreshResult("click", "Brave") },
			action: "click",
			message: []string{
				`no current app state for "Brave"`,
				"call get_app_state again",
			},
		},
		{
			name: "stale state id",
			call: func() (*mcp.CallToolResult, any, error) {
				return staleStateResult("press_key", session.StaleStateError("old"))
			},
			action: "press_key",
			message: []string{
				`unknown or stale state_id "old"`,
				"call get_app_state again",
				"retry with the fresh state_id",
			},
		},
	}
	for _, tt := range tests {
		res, payload, err := tt.call()
		if err != nil {
			t.Fatalf("%s: result error = %v", tt.name, err)
		}
		if res == nil || !res.IsError {
			t.Fatalf("%s: result = %#v, want tool error", tt.name, res)
		}
		action, ok := payload.(computeruse.ActionResult)
		if !ok {
			t.Fatalf("%s: payload type = %T, want ActionResult", tt.name, payload)
		}
		if action.Action != tt.action {
			t.Fatalf("%s: Action = %q, want %q", tt.name, action.Action, tt.action)
		}
		if !action.RequiresRefresh {
			t.Fatalf("%s: RequiresRefresh = false, want true", tt.name)
		}
		for _, want := range tt.message {
			if !strings.Contains(action.Message, want) {
				t.Fatalf("%s: Message = %q, want %q", tt.name, action.Message, want)
			}
		}
	}
}

func TestActionBlockedForIntervention(t *testing.T) {
	monitor := intervention.New(intervention.Config{Enabled: true, QuietPeriod: time.Second})
	monitor.RecordEvent("KCGEventKeyDown", "keyboard", 4321, time.Now())
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
	if payload.BlockReason != "physical_user_keyboard" {
		t.Fatalf("BlockReason = %q, want physical_user_keyboard", payload.BlockReason)
	}
	if payload.BlockEventType != "KCGEventKeyDown" {
		t.Fatalf("BlockEventType = %q, want KCGEventKeyDown", payload.BlockEventType)
	}
	if payload.BlockSourcePID != 4321 {
		t.Fatalf("BlockSourcePID = %d, want 4321", payload.BlockSourcePID)
	}
}

func TestCanUseSkyLightPixelClick(t *testing.T) {
	state := computeruse.AppState{
		App:    computeruse.AppInfo{PID: 123},
		Window: computeruse.WindowInfo{WindowID: 456},
	}
	tests := []struct {
		name       string
		button     string
		clickCount int
		state      computeruse.AppState
		want       bool
	}{
		{name: "default left", clickCount: 1, state: state, want: true},
		{name: "spaced left", button: " Left ", clickCount: 1, state: state, want: true},
		{name: "double click", clickCount: 2, state: state, want: true},
		{name: "right button", button: "right", clickCount: 1, state: state},
		{name: "triple click", clickCount: 3, state: state},
		{name: "missing pid", clickCount: 1, state: computeruse.AppState{Window: computeruse.WindowInfo{WindowID: 456}}},
		{name: "missing window id", clickCount: 1, state: computeruse.AppState{App: computeruse.AppInfo{PID: 123}}},
	}
	for _, tt := range tests {
		if got := canUseSkyLightPixelClick(tt.button, tt.clickCount, tt.state); got != tt.want {
			t.Fatalf("%s: canUseSkyLightPixelClick() = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestClickToolExposesForegroundHIDFallback(t *testing.T) {
	for _, tool := range orderedComputerUseTools() {
		if tool.Name != "click" {
			continue
		}
		schema := normalizeJSON(t, tool.InputSchema).(map[string]any)
		props := schema["properties"].(map[string]any)
		if _, ok := props["foreground_hid"]; !ok {
			t.Fatalf("click schema missing foreground_hid: %#v", props)
		}
		return
	}
	t.Fatalf("click tool missing")
}

func TestGetAppStateExposesOmitScreenshot(t *testing.T) {
	for _, tool := range orderedComputerUseTools() {
		if tool.Name != "get_app_state" {
			continue
		}
		schema := normalizeJSON(t, tool.InputSchema).(map[string]any)
		props := schema["properties"].(map[string]any)
		if _, ok := props["omit_screenshot"]; !ok {
			t.Fatalf("get_app_state schema missing omit_screenshot: %#v", props)
		}
		return
	}
	t.Fatalf("get_app_state tool missing")
}

func TestAppStateResponseOmitScreenshot(t *testing.T) {
	state := computeruse.AppState{
		ScreenshotPNGBase64: "base64",
		Window: computeruse.WindowInfo{
			ScreenshotWidth:  100,
			ScreenshotHeight: 50,
		},
	}
	got := appStateResponse(state, true)
	if got.ScreenshotPNGBase64 != "" {
		t.Fatalf("ScreenshotPNGBase64 = %q, want empty", got.ScreenshotPNGBase64)
	}
	if got.Window.ScreenshotWidth != 100 || got.Window.ScreenshotHeight != 50 {
		t.Fatalf("Window dimensions = %#v, want preserved", got.Window)
	}
	if full := appStateResponse(state, false); full.ScreenshotPNGBase64 != "base64" {
		t.Fatalf("full ScreenshotPNGBase64 = %q, want preserved", full.ScreenshotPNGBase64)
	}
}

func TestEvaluateJavascriptBuildsAppleScriptTarget(t *testing.T) {
	if got := browserScriptTarget(computeruse.AppInfo{BundleID: "com.brave.Browser", Name: "Brave Browser"}); got != `id "com.brave.Browser"` {
		t.Fatalf("browserScriptTarget bundle = %q", got)
	}
	if got := browserScriptTarget(computeruse.AppInfo{Name: "Safari"}); got != `"Safari"` {
		t.Fatalf("browserScriptTarget name = %q", got)
	}
	if got := appleScriptString("quote \" slash \\ newline\n"); got != `"quote \" slash \\ newline\n"` {
		t.Fatalf("appleScriptString = %q", got)
	}
	if _, err := evaluateJavascript(computeruse.AppInfo{Name: "Safari"}, "", 0, 0); err == nil {
		t.Fatalf("evaluateJavascript empty script = nil, want error")
	}
}

func TestEvaluateCDPJavascriptToolInSpec(t *testing.T) {
	tools := orderedComputerUseTools()
	for _, tool := range tools {
		if tool.Name == "evaluate_cdp_javascript" {
			return
		}
	}
	t.Fatalf("evaluate_cdp_javascript missing from ordered tool spec")
}

func TestTrajectoryRecorderRecordsActionArgs(t *testing.T) {
	rec := newTrajectoryRecorder()
	if out := rec.set(true, true); !out.Enabled || out.Count != 0 {
		t.Fatalf("set recording = %#v, want enabled empty recorder", out)
	}
	rec.record("press_key", pressKeyInput{App: "Brave", StateID: "stale", Key: "a"}, computeruse.ActionResult{Action: "press_key"})
	steps := rec.snapshot(1)
	if len(steps) != 1 {
		t.Fatalf("recorded steps = %d, want 1", len(steps))
	}
	if steps[0].Tool != "press_key" || steps[0].Args["key"] != "a" {
		t.Fatalf("step = %#v, want press_key a", steps[0])
	}
	if _, ok := steps[0].Args["state_id"]; ok {
		t.Fatalf("recorded args retained state_id: %#v", steps[0].Args)
	}
	if err := rec.replayingMode(func() error {
		rec.record("press_key", pressKeyInput{App: "Brave", Key: "b"}, nil)
		return nil
	}); err != nil {
		t.Fatalf("replayingMode: %v", err)
	}
	if got := len(rec.snapshot(1)); got != 1 {
		t.Fatalf("recorded during replay; steps = %d, want 1", got)
	}
}

func TestStateForActionRequiresFreshStateID(t *testing.T) {
	rt := &runtimeState{sessions: session.NewStore()}
	if _, err := stateForAction(rt, "click", "Finder", ""); err == nil {
		t.Fatalf("stateForAction without state_id = nil, want error")
	}
	if _, err := stateForAction(rt, "click", "Finder", "stale"); err == nil {
		t.Fatalf("stateForAction stale state_id = nil, want error")
	} else if got := err.Error(); !strings.Contains(got, `unknown or stale state_id "stale"`) || !strings.Contains(got, "retry with the fresh state_id") {
		t.Fatalf("stateForAction stale error = %q", got)
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
	if got.StateID != state.StateID {
		t.Fatalf("StateID = %q, want %q", got.StateID, state.StateID)
	}
	if _, err := stateForAction(rt, "click", "Safari", state.StateID); err == nil {
		t.Fatalf("stateForAction mismatched app = nil, want error")
	} else if got := err.Error(); !strings.Contains(got, "retry with the fresh state_id") {
		t.Fatalf("stateForAction mismatched app error = %q, want retry guidance", got)
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
