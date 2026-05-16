//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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

	cs := newLiveCommandClientSession(t, ctx)
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

func newLiveCommandClientSession(t *testing.T, ctx context.Context) *mcp.ClientSession {
	t.Helper()

	dir := t.TempDir()
	approvalDir := filepath.Join(dir, "approvals")
	if err := os.MkdirAll(approvalDir, 0700); err != nil {
		t.Fatalf("mkdir approval dir: %v", err)
	}
	approvalPath := filepath.Join(approvalDir, "approvals.json")
	approvalJSON := `{"version":1,"approvals":{"com.apple.calculator":{"approved_at":"2026-05-16T00:00:00Z"}}}`
	if err := os.WriteFile(approvalPath, []byte(approvalJSON), 0600); err != nil {
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
