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
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/xcmcp/axuiautomation"
)

// xcodeProxy manages a child mcpbridge process and client session.
type xcodeProxy struct {
	client  *mcp.Client
	session *mcp.ClientSession
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

	// Drain any Allow dialogs that may have built up from previous launches.
	drainXcodeAllowDialogs()

	// Watch for Xcode's "Allow" permission dialog and auto-dismiss it.
	// Give the AppKit run loop a moment to start before launching mcpbridge,
	// so the permission sheet can render and be clicked.
	allowCtx, allowCancel := context.WithCancel(ctx)
	go autoAllowXcodeDialog(allowCtx)
	slog.Debug("waiting for run loop before launching mcpbridge")
	time.Sleep(500 * time.Millisecond)

	slog.Debug("connecting to mcpbridge via xcrun")
	connectCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	session, err := client.Connect(connectCtx, transport, nil)
	// Keep polling for the allow dialog a little longer after connect —
	// sometimes the sheet appears after the handshake.
	time.AfterFunc(3*time.Second, allowCancel)
	if err != nil {
		allowCancel()
		return nil, fmt.Errorf("connect to mcpbridge: %w", err)
	}
	slog.Debug("mcpbridge connected")
	return &xcodeProxy{
		client:  client,
		session: session,
	}, nil
}

// registerXcodeTools discovers tools from the mcpbridge session and registers
// each one as a proxy tool on the server. The prefix, if non-empty, is
// prepended to each tool name with an underscore separator.
func registerXcodeTools(s *mcp.Server, proxy *xcodeProxy, prefix string) (int, error) {
	slog.Debug("registerXcodeTools: discovering tools from mcpbridge", "prefix", prefix)
	ctx := context.Background()
	n := 0
	for tool, err := range proxy.session.Tools(ctx, nil) {
		if err != nil {
			return n, fmt.Errorf("list xcode tools: %w", err)
		}
		slog.Debug("registerXcodeTools: registering tool", "name", tool.Name)

		name := tool.Name
		if prefix != "" {
			name = prefix + "_" + tool.Name
		}

		// Ensure the tool has an InputSchema. The mcpbridge tools should
		// already provide one, but guard against nil to avoid a panic in AddTool.
		if tool.InputSchema == nil {
			tool.InputSchema = map[string]any{"type": "object"}
		}

		registered := &mcp.Tool{
			Name:        name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
			Annotations: tool.Annotations,
		}

		// Capture the original tool name for the proxy call.
		originalName := tool.Name
		handler := makeProxyHandler(proxy, originalName)
		s.AddTool(registered, handler)
		n++
	}
	return n, nil
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
		result, err := proxy.session.CallTool(ctx, &mcp.CallToolParams{
			Name:      toolName,
			Arguments: args,
		})
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("xcode tool error: %v", err)}},
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

// clickXcodeAllowButton looks for an "Allow" button in an Xcode dialog
// window and clicks it. The MCP permission prompt appears as an AXWindow
// with subrole AXDialog containing "Allow" and "Don't Allow" buttons.
func clickXcodeAllowButton() bool {
	app, err := axuiautomation.NewApplication("com.apple.dt.Xcode")
	if err != nil {
		return false
	}
	defer app.Close()

	// The permission dialog is an AXWindow with subrole AXDialog.
	for _, win := range app.Windows().AllElements() {
		if win.Subrole() != "AXDialog" {
			continue
		}
		btn := win.Buttons().ByTitle("Allow").First()
		if btn != nil {
			slog.Debug("clicking Xcode Allow button")
			if err := btn.Click(); err == nil {
				return true
			}
		}
	}
	return false
}

// drainXcodeAllowDialogs clicks all pending Allow buttons in Xcode for a
// short window at startup, handling any dialogs that have built up from
// previous launches.
func drainXcodeAllowDialogs() {
	slog.Debug("drainXcodeAllowDialogs: sweeping for queued Allow dialogs")
	deadline := time.Now().Add(3 * time.Second)
	clicked := 0
	for time.Now().Before(deadline) {
		if clickXcodeAllowButton() {
			clicked++
			slog.Info("dismissed queued Xcode Allow dialog", "total", clicked)
			time.Sleep(300 * time.Millisecond) // let the next sheet appear
		} else {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if clicked > 0 {
		slog.Info("drainXcodeAllowDialogs: done", "dismissed", clicked)
	} else {
		slog.Debug("drainXcodeAllowDialogs: no queued dialogs found")
	}
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
	// First discover open windows/tabs via XcodeListWindows if available,
	// otherwise try a well-known tab identifier.
	result, err := proxy.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "GetBuildLog",
		Arguments: map[string]any{"tabIdentifier": "windowtab1", "severity": "error"},
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

// Close shuts down the mcpbridge session and waits for the child process to exit.
func (p *xcodeProxy) Close() error {
	if p.session != nil {
		if err := p.session.Close(); err != nil {
			log.Printf("xcodeProxy: session close: %v", err)
		}
	}
	return nil
}
