// Command ascriptmcp is an MCP server that dynamically exposes scriptable macOS
// applications as MCP tools by parsing their sdef (scripting definition).
//
// It starts with two meta-tools:
//
//   - ascript_expose_app: registers a full set of tools for a named app
//   - ascript_list_apps:  lists installed applications
//
// Calling ascript_expose_app for "Xcode" parses /Applications/Xcode.app's sdef
// and dynamically adds tools like xcode_build, xcode_test, xcode_run, etc.
// Each tool runs the corresponding AppleScript command via osascript.
package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/buildversion"
	"github.com/tmc/macgo"
)

func main() {
	cfg := macgo.NewConfig().
		WithAppName("ascriptmcp").
		WithCustom("com.apple.security.automation.apple-events").
		WithAdHocSign()
	cfg.BundleID = "dev.tmc.ascriptmcp"
	if err := macgo.Start(cfg); err != nil {
		log.Fatalf("macgo: %v", err)
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	if isTTY() {
		runCLI()
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "ascriptmcp",
		Version: buildversion.String(),
	}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{
			Tools: &mcp.ToolCapabilities{ListChanged: true},
		},
	})

	st := &state{server: server}
	st.registerMetaTools()

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Printf("server error: %v", err)
	}
}

// state holds the server and tracks which apps have been exposed.
type state struct {
	server  *mcp.Server
	mu      sync.Mutex
	exposed map[string]bool // appName → exposed
}

func (st *state) registerMetaTools() {
	st.exposed = make(map[string]bool)
	registerListApps(st)
	registerExposeApp(st)
}
