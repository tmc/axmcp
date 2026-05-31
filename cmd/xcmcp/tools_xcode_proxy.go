//go:build darwin

package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/apple/x/axuiautomation"
)

const xcodeToolDiscoveryTimeout = 10 * time.Second

var hasRunningXcodeProcess = detectRunningXcodeProcess

// xcodeProxy manages a child mcpbridge process and client session.
// It automatically reconnects when the connection is lost (e.g. Xcode killed)
// and re-discovers tools from the new session.
type xcodeProxy struct {
	mu          sync.Mutex
	client      *mcp.Client
	session     *mcp.ClientSession
	allowCancel context.CancelFunc // cancels the auto-allow dialog clicker
	server      *mcp.Server        // for re-registering tools on reconnect
	prefix      string             // tool name prefix for proxied tools
}

// newXcodeProxy starts xcrun mcpbridge as a child process, connects an MCP
// client, and returns the proxy. Returns an error if the process cannot be
// started or the MCP handshake fails.
func newXcodeProxy(ctx context.Context) (*xcodeProxy, error) {
	cmd := exec.Command("xcrun", "mcpbridge")
	// Pass through Xcode environment variables if set.
	if v := os.Getenv("MCP_XCODE_PID"); v != "" {
		cmd.Env = append(os.Environ(), "MCP_XCODE_PID="+v)
	}
	if v := os.Getenv("MCP_XCODE_SESSION_ID"); v != "" {
		cmd.Env = append(os.Environ(), "MCP_XCODE_SESSION_ID="+v)
	}
	cmd.Stderr = os.Stderr

	transport := &mcp.CommandTransport{Command: cmd}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "xcmcp-xcode-proxy",
		Version: "0.1.0",
	}, nil)

	// Start auto-clicker for the Allow dialog before connecting.
	// The dialog blocks tools/list, so keep the clicker alive until
	// explicitly cancelled by the caller after tool discovery completes.
	allowCtx, allowCancel := context.WithCancel(ctx)
	go autoAllowXcodeDialog(allowCtx)

	slog.Debug("connecting to mcpbridge via xcrun")
	connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	session, err := client.Connect(connectCtx, transport, nil)
	if err != nil {
		allowCancel()
		return nil, fmt.Errorf("connect to mcpbridge: %w", err)
	}
	slog.Debug("mcpbridge connected")
	return &xcodeProxy{
		client:      client,
		session:     session,
		allowCancel: allowCancel,
	}, nil
}

func xcodeBridgeAvailable() bool {
	return shouldAttemptXcodeBridge(os.Getenv("MCP_XCODE_PID"), hasRunningXcodeProcess())
}

func shouldAttemptXcodeBridge(mcpXcodePID string, hasRunningXcode bool) bool {
	return mcpXcodePID != "" || hasRunningXcode
}

func detectRunningXcodeProcess() bool {
	cmd := exec.Command("pgrep", "-x", "Xcode")
	return cmd.Run() == nil
}

// registerXcodeTools discovers tools from the mcpbridge session and registers
// each one as a proxy tool on the server. The prefix, if non-empty, is
// prepended to each tool name with an underscore separator.
func registerXcodeTools(s *mcp.Server, proxy *xcodeProxy, prefix string) (int, error) {
	slog.Debug("registerXcodeTools: discovering tools from mcpbridge", "prefix", prefix)
	// Cancel the auto-allow dialog clicker once tool discovery finishes.
	if proxy.allowCancel != nil {
		defer proxy.allowCancel()
	}
	// Stash server and prefix so reconnect can re-discover tools.
	proxy.server = s
	proxy.prefix = prefix
	return proxy.discoverAndRegisterTools()
}

// discoverAndRegisterTools enumerates tools from the current mcpbridge
// session and registers (or re-registers) each as a proxy tool.
func (proxy *xcodeProxy) discoverAndRegisterTools() (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), xcodeToolDiscoveryTimeout)
	defer cancel()
	n := 0
	for tool, err := range proxy.session.Tools(ctx, nil) {
		if err != nil {
			return n, fmt.Errorf("list xcode tools: %w", err)
		}
		slog.Debug("registerXcodeTools: registering tool", "name", tool.Name)

		name := tool.Name
		if proxy.prefix != "" {
			name = proxy.prefix + "_" + tool.Name
		}

		// Ensure the tool has an InputSchema. The mcpbridge tools should
		// already provide one, but guard against nil to avoid a panic in AddTool.
		if tool.InputSchema == nil {
			tool.InputSchema = map[string]any{"type": "object"}
		}

		registered := &mcp.Tool{
			Name:         name,
			Description:  tool.Description,
			InputSchema:  tool.InputSchema,
			OutputSchema: tool.OutputSchema,
			Annotations:  tool.Annotations,
			Title:        tool.Title,
			Icons:        tool.Icons,
		}

		// Capture the original tool name for the proxy call.
		originalName := tool.Name
		handler := makeProxyHandler(proxy, originalName)
		// AddTool replaces existing tools with the same name,
		// so this is safe to call on reconnect.
		proxy.server.AddTool(registered, handler)
		n++
	}
	return n, nil
}

// isConnectionError returns true if the error indicates the mcpbridge
// connection is dead (EOF, closed, broken pipe, etc.).
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "closed") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "process exited") ||
		strings.Contains(msg, "signal: killed") ||
		strings.Contains(msg, "signal: terminated")
}

// reconnect tears down the existing session and establishes a new one.
// It retries up to 3 times with backoff. Caller must hold proxy.mu.
func (proxy *xcodeProxy) reconnect(ctx context.Context) error {
	if proxy.session != nil {
		proxy.session.Close()
	}
	slog.Info("reconnecting to mcpbridge")

	const maxRetries = 3
	var lastErr error
	for attempt := range maxRetries {
		if attempt > 0 {
			backoff := time.Duration(attempt) * 2 * time.Second
			slog.Debug("reconnect backoff", "attempt", attempt+1, "wait", backoff)
			time.Sleep(backoff)
		}

		connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)

		cmd := exec.Command("xcrun", "mcpbridge")
		if v := os.Getenv("MCP_XCODE_PID"); v != "" {
			cmd.Env = append(os.Environ(), "MCP_XCODE_PID="+v)
		}
		if v := os.Getenv("MCP_XCODE_SESSION_ID"); v != "" {
			cmd.Env = append(os.Environ(), "MCP_XCODE_SESSION_ID="+v)
		}
		cmd.Stderr = os.Stderr

		transport := &mcp.CommandTransport{Command: cmd}
		client := mcp.NewClient(&mcp.Implementation{
			Name:    "xcmcp-xcode-proxy",
			Version: "0.1.0",
		}, nil)

		allowCtx, allowCancel := context.WithCancel(ctx)
		go autoAllowXcodeDialog(allowCtx)

		session, err := client.Connect(connectCtx, transport, nil)
		cancel()
		if err != nil {
			allowCancel()
			lastErr = err
			slog.Warn("reconnect attempt failed", "attempt", attempt+1, "err", err)
			continue
		}
		proxy.client = client
		proxy.session = session
		slog.Info("mcpbridge reconnected", "attempt", attempt+1)

		// Re-discover tools from the new session.
		if proxy.server != nil {
			n, err := proxy.discoverAndRegisterTools()
			allowCancel()
			if err != nil {
				slog.Warn("failed to re-discover tools after reconnect", "err", err)
			} else {
				slog.Info("re-registered xcode tools after reconnect", "count", n)
			}
		} else {
			time.AfterFunc(30*time.Second, allowCancel)
		}
		return nil
	}
	return fmt.Errorf("reconnect to mcpbridge after %d attempts: %w", maxRetries, lastErr)
}

// callTool forwards a tool call, reconnecting once on connection errors.
func (proxy *xcodeProxy) callTool(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	proxy.mu.Lock()
	session := proxy.session
	proxy.mu.Unlock()

	if session == nil {
		// Initial connection never succeeded — try connecting now.
		proxy.mu.Lock()
		if proxy.session == nil {
			if err := proxy.reconnect(ctx); err != nil {
				proxy.mu.Unlock()
				return nil, fmt.Errorf("xcode unavailable: %w", err)
			}
		}
		session = proxy.session
		proxy.mu.Unlock()
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err == nil {
		return result, nil
	}
	if !isConnectionError(err) {
		return nil, fmt.Errorf("xcode tool error: %w", err)
	}

	// Connection lost — attempt reconnect.
	proxy.mu.Lock()
	defer proxy.mu.Unlock()

	// Only reconnect if the session hasn't already been replaced by
	// another goroutine.
	if proxy.session == session {
		if reconnErr := proxy.reconnect(ctx); reconnErr != nil {
			return nil, fmt.Errorf("xcode unavailable (Xcode may not be running): %w", reconnErr)
		}
	}

	return proxy.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
}

// makeProxyHandler returns a ToolHandler that forwards calls to the mcpbridge session.
func makeProxyHandler(proxy *xcodeProxy, toolName string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args map[string]any
		if req.Params.Arguments != nil {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return &mcp.CallToolResult{
					IsError: true,
					Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("invalid arguments: %v", err)}},
				}, nil
			}
		}
		result, err := proxy.callTool(ctx, toolName, args)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, nil
		}
		return result, nil
	}
}

// autoAllowXcodeDialog polls Xcode's UI for the MCP permission sheet and
// clicks the "Allow" button when it appears. It runs until ctx is cancelled.
func autoAllowXcodeDialog(ctx context.Context) {
	slog.Debug("autoAllowXcodeDialog: starting poll")
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Debug("autoAllowXcodeDialog: context done")
			return
		case <-ticker.C:
			if clickXcodeAllowButton() {
				log.Println("Auto-allowed Xcode MCP access dialog")
				return
			}
		}
	}
}

// clickXcodeAllowButton looks for an "Allow" button in an Xcode MCP
// permission dialog and clicks it. The dialog may appear as an AXSheet,
// AXDialog, or standard AXWindow depending on the Xcode version. We search
// all Xcode windows for a button titled "Allow" that sits alongside a
// "Don't Allow" button to avoid false positives.
func clickXcodeAllowButton() bool {
	app, err := axuiautomation.NewApplication("com.apple.dt.Xcode")
	if err != nil {
		slog.Debug("clickXcodeAllowButton: cannot open Xcode AX", "err", err)
		return false
	}
	defer app.Close()

	// Use WindowList (AXWindows attribute) which is more reliable than
	// the element query traversal used by Windows().
	windows := app.WindowList()
	if len(windows) == 0 {
		// Fallback to query-based enumeration.
		windows = app.Windows().AllElements()
	}
	slog.Debug("clickXcodeAllowButton: scanning windows", "count", len(windows))

	for _, win := range windows {
		// Enumerate buttons once per window.
		allButtons := win.Descendants().WithLimit(100).ByRole("AXButton").AllElements()

		// Look for an "Allow" button and a "Don't Allow" button.
		// Use substring matching for "Don't Allow" because Xcode uses a
		// curly apostrophe (U+2019) that doesn't match a straight quote.
		var allow, dontAllow *axuiautomation.Element
		for _, btn := range allButtons {
			t := btn.Title()
			if t == "Allow" {
				allow = btn
			} else if strings.Contains(t, "Allow") && strings.Contains(t, "Don") {
				dontAllow = btn
			}
		}
		if allow == nil || dontAllow == nil {
			continue
		}
		slog.Debug("clicking Xcode Allow button", "window", win.Title(), "subrole", win.Subrole())
		if err := allow.Click(); err == nil {
			return true
		}
	}
	return false
}

// registerBuildErrorResource registers a subscribable resource that exposes
// Xcode build errors. It polls GetBuildLog every few seconds and notifies
// subscribers when the error list changes.
func registerBuildErrorResource(s *mcp.Server, proxy *xcodeProxy) {
	const uri = "xcmcp://xcode/build-errors"

	s.AddResource(&mcp.Resource{
		URI:         uri,
		Name:        "xcode_build_errors",
		Description: "Current Xcode build errors (polls GetBuildLog from mcpbridge)",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		text, err := fetchBuildErrors(ctx, proxy)
		if err != nil {
			return nil, err
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      uri,
				MIMEType: "application/json",
				Text:     text,
			}},
		}, nil
	})

	// Start background poller that notifies subscribers on change.
	go pollBuildErrors(s, proxy, uri)
	log.Println("Build error subscription resource registered")
}

// fetchBuildErrors calls GetBuildLog for all open tabs and returns the combined result.
func fetchBuildErrors(ctx context.Context, proxy *xcodeProxy) (string, error) {
	result, err := proxy.callTool(ctx, "GetBuildLog", map[string]any{
		"tabIdentifier": "windowtab1",
		"severity":      "error",
	})
	if err != nil {
		return "", fmt.Errorf("get build log: %w", err)
	}
	// Extract text content from result.
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text, nil
		}
	}
	return "{}", nil
}

// pollBuildErrors periodically checks for build error changes and notifies subscribers.
func pollBuildErrors(s *mcp.Server, proxy *xcodeProxy, uri string) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var lastHash [sha256.Size]byte

	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		text, err := fetchBuildErrors(ctx, proxy)
		cancel()
		if err != nil {
			continue
		}
		hash := sha256.Sum256([]byte(text))
		if hash != lastHash {
			lastHash = hash
			if err := s.ResourceUpdated(context.Background(), &mcp.ResourceUpdatedNotificationParams{URI: uri}); err != nil {
				log.Printf("build error notify: %v", err)
			}
		}
	}
}
