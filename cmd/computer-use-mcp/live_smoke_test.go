//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/apple/appkit"
	"github.com/tmc/axmcp/internal/computeruse"
)

func TestLiveCalculatorMCPClickSmoke(t *testing.T) {
	if os.Getenv("COMPUTER_USE_MCP_LIVE_SMOKE") == "" {
		t.Skip("set COMPUTER_USE_MCP_LIVE_SMOKE=1 to drive Calculator through computer-use-mcp")
	}
	if os.Getenv("CI") != "" {
		t.Skip("headed Calculator smoke test is not for CI")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if out, err := exec.CommandContext(ctx, "open", "-g", "-a", "Calculator").CombinedOutput(); err != nil {
		t.Fatalf("open Calculator: %v: %s", err, strings.TrimSpace(string(out)))
	}
	time.Sleep(500 * time.Millisecond)

	cs := newLiveCommandClientSession(t, ctx, "com.apple.calculator")
	state := callStateTool(t, ctx, cs, "Calculator")
	if state.Permissions.Pending {
		t.Skipf("computer-use permissions pending: %s", state.Permissions.Message)
	}
	index, ok := findCalculatorButton(state, "1")
	if !ok {
		t.Fatalf("Calculator button 1 not found in %d nodes", len(state.Tree))
	}
	click := callTool[computeruse.ActionResult](t, ctx, cs, "click", map[string]any{
		"app":           "Calculator",
		"state_id":      state.StateID,
		"element_index": index,
	})
	if click.RequiresRefresh {
		t.Fatalf("click unexpectedly requires refresh: %#v", click)
	}
	if click.Action != "click" {
		t.Fatalf("click.Action = %q, want click", click.Action)
	}
	if !strings.Contains(click.Message, "clicked") {
		t.Fatalf("click.Message = %q, want clicked", click.Message)
	}
}

func TestLiveBraveBackgroundAXSmoke(t *testing.T) {
	if os.Getenv("COMPUTER_USE_MCP_LIVE_BRAVE_SMOKE") == "" {
		t.Skip("set COMPUTER_USE_MCP_LIVE_BRAVE_SMOKE=1 to read Brave through computer-use-mcp")
	}
	if os.Getenv("CI") != "" {
		t.Skip("headed Brave smoke test is not for CI")
	}

	bin := "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser"
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("Brave Browser not installed at %s", bin)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	page := filepath.Join(t.TempDir(), "axmcp-brave-smoke.html")
	const marker = "AXMCP Brave background smoke"
	html := `<!doctype html><meta charset="utf-8"><title>` + marker + `</title>` +
		`<body style="font:16px system-ui;padding:40px">` +
		`<button id="ready">` + marker + `</button>` +
		`<p><input id="target" aria-label="AXMCP input target" style="font:20px system-ui;width:360px;height:40px"></p>` +
		`</body>`
	if err := os.WriteFile(page, []byte(html), 0600); err != nil {
		t.Fatalf("write smoke page: %v", err)
	}

	profileDir := filepath.Join(t.TempDir(), "brave-profile")
	args := []string{
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-sync",
		"--app=" + (&url.URL{Scheme: "file", Path: page}).String(),
	}
	brave := exec.CommandContext(ctx, bin, args...)
	if err := brave.Start(); err != nil {
		t.Fatalf("start Brave: %v", err)
	}
	t.Cleanup(func() {
		if brave.Process != nil {
			_ = brave.Process.Kill()
			_, _ = brave.Process.Wait()
		}
	})

	if out, err := exec.CommandContext(ctx, "open", "-a", "Calculator").CombinedOutput(); err != nil {
		t.Fatalf("open Calculator: %v: %s", err, strings.TrimSpace(string(out)))
	}
	waitForFrontmostNotPID(t, ctx, brave.Process.Pid)

	cs := newLiveCommandClientSession(t, ctx, "com.brave.Browser")
	state := waitForAppStateNode(t, ctx, cs, fmt.Sprint(brave.Process.Pid), marker)
	if state.Permissions.Pending {
		t.Skipf("computer-use permissions pending: %s", state.Permissions.Message)
	}
	if state.App.PID == 0 {
		t.Fatalf("Brave state has no PID: %#v", state.App)
	}
	if got := frontmostPID(); got == state.App.PID {
		t.Fatalf("Brave became frontmost during background AX read; pid=%d", got)
	}
	if _, ok := findNodeContaining(state, "AXWebArea", marker); !ok {
		t.Fatalf("Brave AXWebArea with marker not found in %d nodes", len(state.Tree))
	}
	field, ok := findInputField(state)
	if !ok {
		t.Fatalf("Brave input field not found in %d nodes", len(state.Tree))
	}
	x, y := nodeCenterScreenshotPoint(state.Window, field)
	click := callTool[computeruse.ActionResult](t, ctx, cs, "click", map[string]any{
		"app":      fmt.Sprint(state.App.PID),
		"state_id": state.StateID,
		"x":        x,
		"y":        y,
	})
	if click.RequiresRefresh {
		t.Fatalf("Brave pixel click unexpectedly requires refresh: %#v", click)
	}
	if got := frontmostPID(); got == state.App.PID {
		t.Fatalf("Brave became frontmost during background pixel click; pid=%d", got)
	}
	key := callTool[computeruse.ActionResult](t, ctx, cs, "press_key", map[string]any{
		"app":      fmt.Sprint(state.App.PID),
		"state_id": state.StateID,
		"key":      "a",
	})
	if key.RequiresRefresh {
		t.Fatalf("Brave key press unexpectedly requires refresh: %#v", key)
	}
	if got := frontmostPID(); got == state.App.PID {
		t.Fatalf("Brave became frontmost during background key press; pid=%d", got)
	}
	typed := waitForAppStateNode(t, ctx, cs, fmt.Sprint(state.App.PID), "a")
	if _, ok := findNodeContaining(typed, "AXTextField", "a"); !ok {
		t.Fatalf("Brave text field did not receive background key input")
	}
}

func newLiveCommandClientSession(t *testing.T, ctx context.Context, approvedBundleIDs ...string) *mcp.ClientSession {
	t.Helper()

	dir := t.TempDir()
	approvalDir := filepath.Join(dir, "approvals")
	if err := os.MkdirAll(approvalDir, 0700); err != nil {
		t.Fatalf("mkdir approval dir: %v", err)
	}
	approvalPath := filepath.Join(approvalDir, "approvals.json")
	approvalJSON := struct {
		Version   int                          `json:"version"`
		Approvals map[string]map[string]string `json:"approvals"`
	}{
		Version:   1,
		Approvals: make(map[string]map[string]string),
	}
	for _, bundleID := range approvedBundleIDs {
		approvalJSON.Approvals[bundleID] = map[string]string{"approved_at": "2026-05-16T00:00:00Z"}
	}
	data, err := json.Marshal(approvalJSON)
	if err != nil {
		t.Fatalf("marshal approvals: %v", err)
	}
	if err := os.WriteFile(approvalPath, data, 0600); err != nil {
		t.Fatalf("write approvals: %v", err)
	}
	bin := filepath.Join(dir, "computer-use-mcp")
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build computer-use-mcp: %v: %s", err, strings.TrimSpace(string(out)))
	}
	cmd := exec.CommandContext(ctx, bin, "--ghost-cursor=false")
	cmd.Env = append(os.Environ(), "MACGO_NO_RELAUNCH=1", "COMPUTER_USE_MCP_APPROVALS_PATH="+approvalPath)
	cmd.Dir = "."
	client := mcp.NewClient(&mcp.Implementation{Name: "live-smoke-client", Version: "0.0.1"}, nil)
	cs, err := client.Connect(ctx, &mcp.CommandTransport{
		Command:           cmd,
		TerminateDuration: time.Second,
	}, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func waitForAppStateNode(t *testing.T, ctx context.Context, cs *mcp.ClientSession, app, text string) computeruse.AppState {
	t.Helper()

	var last computeruse.AppState
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		last = callStateTool(t, ctx, cs, app)
		if last.Permissions.Pending {
			return last
		}
		if _, ok := findNodeContaining(last, "", text); ok {
			return last
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for %s AX node: %v", app, ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
	t.Fatalf("%s AX node containing %q not found in %d nodes", app, text, len(last.Tree))
	return computeruse.AppState{}
}

func callStateTool(t *testing.T, ctx context.Context, cs *mcp.ClientSession, app string) computeruse.AppState {
	t.Helper()
	return callTool[computeruse.AppState](t, ctx, cs, "get_app_state", map[string]any{"app": app})
}

func callTool[T any](t *testing.T, ctx context.Context, cs *mcp.ClientSession, name string, args map[string]any) T {
	t.Helper()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s returned tool error: %s", name, toolText(res))
	}
	var out T
	data, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("%s structured marshal: %v", name, err)
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("%s structured unmarshal: %v; json=%s", name, err, data)
	}
	return out
}

func toolText(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	var parts []string
	for _, content := range res.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func findCalculatorButton(state computeruse.AppState, title string) (string, bool) {
	for _, node := range state.Tree {
		if node.Role == "AXButton" && (node.Title == title || node.Value == title || node.Description == title) {
			return fmt.Sprint(node.Index), true
		}
	}
	return "", false
}

func findNodeContaining(state computeruse.AppState, role, text string) (computeruse.ElementNode, bool) {
	for _, node := range state.Tree {
		if role != "" && node.Role != role {
			continue
		}
		if strings.Contains(node.Title, text) || strings.Contains(node.Value, text) || strings.Contains(node.Description, text) {
			return node, true
		}
	}
	return computeruse.ElementNode{}, false
}

func findInputField(state computeruse.AppState) (computeruse.ElementNode, bool) {
	for _, node := range state.Tree {
		if node.Role == "AXTextField" || node.Role == "AXTextArea" {
			return node, true
		}
	}
	return computeruse.ElementNode{}, false
}

func nodeCenterScreenshotPoint(window computeruse.WindowInfo, node computeruse.ElementNode) (float64, float64) {
	scaleX := float64(window.ScreenshotWidth) / float64(window.Width)
	scaleY := float64(window.ScreenshotHeight) / float64(window.Height)
	x := float64(node.X+node.Width/2) * scaleX
	y := float64(node.Y+node.Height/2) * scaleY
	return x, y
}

func frontmostPID() int {
	app := appkit.GetNSWorkspaceClass().SharedWorkspace().FrontmostApplication()
	if app == nil {
		return 0
	}
	return int(app.ProcessIdentifier())
}

func waitForFrontmostNotPID(t *testing.T, ctx context.Context, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := frontmostPID(); got != pid {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for non-Brave frontmost app: %v", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatalf("Brave remained frontmost before background AX read; pid=%d", pid)
}
