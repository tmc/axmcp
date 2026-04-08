package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/sdef"
)

// ── ascript_list_apps ────────────────────────────────────────────────────────────

type listAppsInput struct{}

func registerListApps(st *state) {
	mcp.AddTool(st.server, &mcp.Tool{
		Name:        "ascript_list_apps",
		Description: "List scriptable macOS applications in /Applications that have an sdef",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ listAppsInput) (*mcp.CallToolResult, any, error) {
		entries, err := os.ReadDir("/Applications")
		if err != nil {
			return nil, nil, fmt.Errorf("read /Applications: %w", err)
		}
		var buf bytes.Buffer
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".app") {
				continue
			}
			appPath := "/Applications/" + e.Name()
			appName := sdef.AppName(appPath)
			st.mu.Lock()
			exposed := st.exposed[strings.ToLower(appName)]
			st.mu.Unlock()
			status := ""
			if exposed {
				status = " [exposed]"
			}
			fmt.Fprintf(&buf, "%-40s  %s%s\n", e.Name(), appPath, status)
		}
		return textResult(buf.String()), nil, nil
	})
}

// ── ascript_expose_app ───────────────────────────────────────────────────────────

type exposeAppInput struct {
	// App is the application name (e.g. "Xcode") or full path
	// (e.g. "/Applications/Xcode.app").
	App string `json:"app"`
}

func registerExposeApp(st *state) {
	mcp.AddTool(st.server, &mcp.Tool{
		Name: "ascript_expose_app",
		Description: `Parse a scriptable macOS application's sdef and dynamically register
MCP tools for each of its AppleScript commands.

After calling this tool with app="Xcode", new tools become available:
  xcode_build, xcode_test, xcode_run, xcode_clean, xcode_stop, ...

The app argument can be a short name ("Xcode", "Finder", "TextEdit") or a
full path ("/Applications/Xcode.app").`,
	}, func(_ context.Context, _ *mcp.CallToolRequest, args exposeAppInput) (*mcp.CallToolResult, any, error) {
		appPath := resolveAppPath(args.App)
		appName := sdef.AppName(appPath)

		st.mu.Lock()
		key := strings.ToLower(appName)
		already := st.exposed[key]
		st.mu.Unlock()

		if already {
			return textResult(fmt.Sprintf("%s tools already registered", appName)), nil, nil
		}

		d, err := sdef.Parse(appPath)
		if err != nil {
			return nil, nil, fmt.Errorf("parse sdef for %s: %w", appPath, err)
		}

		cmds := d.Commands()
		var registered []string
		for _, cmd := range cmds {
			toolName := sdef.ToolName(appName, cmd.Name)
			registerCommandTool(st.server, appName, cmd, toolName)
			registered = append(registered, toolName)
		}

		// Also register a get-property tool and a raw-script tool for the app.
		registerGetPropTool(st.server, appName)
		registerRawScriptTool(st.server, appName)
		registered = append(registered, sdef.ToolName(appName, "get_property"))
		registered = append(registered, sdef.ToolName(appName, "run_script"))

		st.mu.Lock()
		st.exposed[key] = true
		st.mu.Unlock()

		var buf bytes.Buffer
		fmt.Fprintf(&buf, "Exposed %d tools for %s:\n", len(registered), appName)
		for _, t := range registered {
			fmt.Fprintf(&buf, "  %s\n", t)
		}
		return textResult(buf.String()), nil, nil
	})
}

// resolveAppPath turns a short name like "Xcode" into "/Applications/Xcode.app".
func resolveAppPath(app string) string {
	if strings.Contains(app, "/") {
		return app
	}
	// Try exact match first, then case-insensitive.
	exact := "/Applications/" + app + ".app"
	if _, err := os.Stat(exact); err == nil {
		return exact
	}
	entries, _ := os.ReadDir("/Applications")
	lower := strings.ToLower(app)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".app") {
			if strings.ToLower(strings.TrimSuffix(e.Name(), ".app")) == lower {
				return "/Applications/" + e.Name()
			}
		}
	}
	return exact // best guess
}

// ── per-command tool ──────────────────────────────────────────────────────────

type commandInput struct {
	// DirectParam is the direct (unnamed) AppleScript parameter.
	DirectParam string `json:"direct_param,omitempty"`
	// Params holds named parameters as key=value pairs, e.g. ["saving=no"].
	Params []string `json:"params,omitempty"`
	// Object is the AppleScript object expression to target instead of the
	// application itself, e.g. "front workspace document".
	Object string `json:"object,omitempty"`
}

func registerCommandTool(s *mcp.Server, appName string, cmd sdef.Command, toolName string) {
	desc := buildCommandDescription(appName, cmd)
	mcp.AddTool(s, &mcp.Tool{
		Name:        toolName,
		Description: desc,
	}, func(_ context.Context, _ *mcp.CallToolRequest, args commandInput) (*mcp.CallToolResult, any, error) {
		params := map[string]string{}
		for _, kv := range args.Params {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				return nil, nil, fmt.Errorf("invalid param %q: expected key=value", kv)
			}
			params[k] = v
		}

		var script string
		if args.Object != "" {
			// Target a specific object within the app.
			var b strings.Builder
			fmt.Fprintf(&b, "tell application %q\n", appName)
			fmt.Fprintf(&b, "\ttell %s\n", args.Object)
			fmt.Fprintf(&b, "\t\t%s", cmd.Name)
			if args.DirectParam != "" {
				fmt.Fprintf(&b, " %s", args.DirectParam)
			}
			for k, v := range params {
				fmt.Fprintf(&b, " %s %s", k, v)
			}
			fmt.Fprintf(&b, "\n\tend tell\nend tell")
			script = b.String()
		} else {
			script = sdef.BuildScript(appName, cmd.Name, args.DirectParam, params)
		}

		result, err := sdef.RunScript(script)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", cmd.Name, err)
		}
		return textResult(result), nil, nil
	})
}

func buildCommandDescription(appName string, cmd sdef.Command) string {
	var b strings.Builder
	if cmd.Description != "" {
		b.WriteString(cmd.Description)
	} else {
		fmt.Fprintf(&b, "Run the %q AppleScript command on %s.", cmd.Name, appName)
	}
	b.WriteString("\n\nParameters:")
	if cmd.DirectParameter != nil && cmd.DirectParameter.Type != "" {
		fmt.Fprintf(&b, "\n  direct_param (%s)", cmd.DirectParameter.Type)
		if cmd.DirectParameter.Description != "" {
			fmt.Fprintf(&b, ": %s", cmd.DirectParameter.Description)
		}
	}
	for _, p := range cmd.Parameters {
		opt := ""
		if p.Optional == "yes" {
			opt = " [optional]"
		}
		fmt.Fprintf(&b, "\n  params entry %q=%s%s", p.Name, p.Type, opt)
		if p.Description != "" {
			fmt.Fprintf(&b, ": %s", p.Description)
		}
	}
	b.WriteString(`

Use object="front workspace document" to target a specific document or element.
Params are key=value pairs, e.g. params=["saving=no"].`)
	return b.String()
}

// ── per-app get-property tool ─────────────────────────────────────────────────

type getPropInput struct {
	// Property is the AppleScript property name, e.g. "name", "version".
	Property string `json:"property"`
	// Object is an optional object expression, e.g. "front workspace document".
	Object string `json:"object,omitempty"`
}

func registerGetPropTool(s *mcp.Server, appName string) {
	toolName := sdef.ToolName(appName, "get_property")
	mcp.AddTool(s, &mcp.Tool{
		Name:        toolName,
		Description: fmt.Sprintf("Get a property of %s or one of its objects via AppleScript.", appName),
	}, func(_ context.Context, _ *mcp.CallToolRequest, args getPropInput) (*mcp.CallToolResult, any, error) {
		if args.Property == "" {
			return nil, nil, fmt.Errorf("property is required")
		}
		script := sdef.GetPropertyScript(appName, args.Object, args.Property)
		result, err := sdef.RunScript(script)
		if err != nil {
			return nil, nil, err
		}
		return textResult(result), nil, nil
	})
}

// ── per-app raw-script tool ───────────────────────────────────────────────────

type rawScriptInput struct {
	// Script is the AppleScript to run. It will be wrapped in a tell block
	// for the app unless it already contains "tell application".
	Script string `json:"script"`
}

func registerRawScriptTool(s *mcp.Server, appName string) {
	toolName := sdef.ToolName(appName, "run_script")
	mcp.AddTool(s, &mcp.Tool{
		Name: toolName,
		Description: fmt.Sprintf(`Run arbitrary AppleScript targeting %s.

If the script does not already contain "tell application", it is automatically
wrapped in a tell block for %s. This gives full control for complex queries
like getting the active scheme name, listing build settings, etc.

Example script values:
  "name of front workspace document"
  "tell front workspace document\n\tget name of active scheme\nend tell"`, appName, appName),
	}, func(_ context.Context, _ *mcp.CallToolRequest, args rawScriptInput) (*mcp.CallToolResult, any, error) {
		if args.Script == "" {
			return nil, nil, fmt.Errorf("script is required")
		}
		script := args.Script
		if !strings.Contains(script, "tell application") {
			script = fmt.Sprintf("tell application %q\n\t%s\nend tell",
				appName, strings.ReplaceAll(script, "\n", "\n\t"))
		}
		result, err := sdef.RunScript(script)
		if err != nil {
			return nil, nil, err
		}
		return textResult(result), nil, nil
	})
}

// ── helpers ───────────────────────────────────────────────────────────────────

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}
