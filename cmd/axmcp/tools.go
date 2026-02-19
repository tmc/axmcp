package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/xcmcp/axuiautomation"
)

func registerAXTools(s *mcp.Server) {
	registerAXApps(s)
	registerAXTree(s)
	registerAXFind(s)
	registerAXPipe(s)
	registerAXClick(s)
	registerAXType(s)
	registerAXMenu(s)
	registerAXFocus(s)
	registerAXScreenshot(s)
}

// openApp opens an application by bundle ID or numeric PID string.
func openApp(arg string) (*axuiautomation.Application, error) {
	if pid, err := strconv.ParseInt(arg, 10, 32); err == nil {
		app := axuiautomation.NewApplicationFromPID(int32(pid))
		if app == nil {
			return nil, fmt.Errorf("cannot connect to PID %d", pid)
		}
		return app, nil
	}
	return axuiautomation.NewApplication(arg)
}

// spinAndOpen opens an app and spins the run loop to prime AX IPC.
func spinAndOpen(arg string) (*axuiautomation.Application, error) {
	app, err := openApp(arg)
	if err != nil {
		return nil, err
	}
	axuiautomation.SpinRunLoop(200 * time.Millisecond)
	return app, nil
}

func elementAttrs(e *axuiautomation.Element) map[string]any {
	x, y := e.Position()
	w, h := e.Size()
	return map[string]any{
		"role": e.Role(), "title": e.Title(), "value": e.Value(),
		"subrole": e.Subrole(), "enabled": e.IsEnabled(),
		"role_desc": e.RoleDescription(),
		"desc":      e.Description(), "identifier": e.Identifier(),
		"x": x, "y": y, "w": w, "h": h,
	}
}

func elementSummary(e *axuiautomation.Element) string {
	role := e.Role()
	title := e.Title()
	val := e.Value()
	ident := e.Identifier()
	desc := e.Description()
	var b strings.Builder
	b.WriteString(role)
	if ident != "" {
		fmt.Fprintf(&b, " id=%q", ident)
	}
	if title != "" {
		fmt.Fprintf(&b, " %q", title)
	}
	if val != "" && val != title {
		fmt.Fprintf(&b, " = %q", val)
	}
	if desc != "" && desc != title {
		fmt.Fprintf(&b, " (%s)", desc)
	}
	return b.String()
}

func treeText(e *axuiautomation.Element, indent, maxDepth int) string {
	if e == nil || indent > maxDepth {
		return ""
	}
	var b strings.Builder
	b.WriteString(strings.Repeat("  ", indent))
	b.WriteString(elementSummary(e))
	b.WriteString("\n")
	for _, c := range e.Children() {
		b.WriteString(treeText(c, indent+1, maxDepth))
	}
	return b.String()
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

// ── ax_apps ──────────────────────────────────────────────────────────────────

type axAppsInput struct{}

func registerAXApps(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "ax_apps",
		Description: "List running macOS applications with their bundle IDs and PIDs",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ axAppsInput) (*mcp.CallToolResult, any, error) {
		out, err := exec.Command("lsappinfo", "list").Output()
		if err != nil {
			return nil, nil, fmt.Errorf("lsappinfo: %w", err)
		}
		type appInfo struct {
			Name     string
			BundleID string
			PID      int
		}
		var apps []appInfo
		var cur appInfo
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if strings.Contains(line, ") \"") && strings.Contains(line, "ASN:") {
				if cur.BundleID != "" {
					apps = append(apps, cur)
				}
				cur = appInfo{}
				s := line[strings.Index(line, "\"")+1:]
				cur.Name = s[:strings.Index(s, "\"")]
			} else if strings.HasPrefix(line, "bundleID=") {
				id := strings.Trim(strings.TrimPrefix(line, "bundleID="), `"`)
				if id != "[ NULL ]" {
					cur.BundleID = id
				}
			} else if strings.HasPrefix(line, "pid = ") {
				rest := strings.TrimPrefix(line, "pid = ")
				if i := strings.IndexAny(rest, " \t"); i > 0 {
					rest = rest[:i]
				}
				cur.PID, _ = strconv.Atoi(rest)
			}
		}
		if cur.BundleID != "" {
			apps = append(apps, cur)
		}
		var buf bytes.Buffer
		for _, a := range apps {
			fmt.Fprintf(&buf, "%-45s  pid=%-6d  %s\n", a.BundleID, a.PID, a.Name)
		}
		return textResult(buf.String()), nil, nil
	})
}

// ── ax_tree ───────────────────────────────────────────────────────────────────

type axTreeInput struct {
	App   string `json:"app"`
	Depth int    `json:"depth,omitempty"`
}

func registerAXTree(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "ax_tree",
		Description: "Print the AX element tree for a running application. Returns role/title hierarchy.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axTreeInput) (*mcp.CallToolResult, any, error) {
		app, err := spinAndOpen(args.App)
		if err != nil {
			return nil, nil, err
		}
		defer app.Close()
		depth := args.Depth
		if depth <= 0 {
			depth = 4
		}
		return textResult(treeText(app.Root(), 0, depth)), nil, nil
	})
}

// ── ax_find ───────────────────────────────────────────────────────────────────

type axFindInput struct {
	App      string `json:"app"`
	Role     string `json:"role,omitempty"`
	Title    string `json:"title,omitempty"`
	Contains string `json:"contains,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

func registerAXFind(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "ax_find",
		Description: "Find AX elements in an app by role (e.g. AXButton), exact title, or title substring",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axFindInput) (*mcp.CallToolResult, any, error) {
		app, err := spinAndOpen(args.App)
		if err != nil {
			return nil, nil, err
		}
		defer app.Close()

		limit := args.Limit
		if limit <= 0 {
			limit = 50
		}
		q := app.Descendants().WithLimit(limit)
		if args.Role != "" {
			q = q.ByRole(args.Role)
		}
		if args.Title != "" {
			q = q.ByTitle(args.Title)
		}
		if args.Contains != "" {
			q = q.ByTitleContains(args.Contains)
		}

		els := q.AllElements()
		var buf bytes.Buffer
		for i, e := range els {
			fmt.Fprintf(&buf, "[%d] %s\n", i, elementSummary(e))
		}
		return textResult(buf.String()), nil, nil
	})
}

// ── ax_pipe ───────────────────────────────────────────────────────────────────

type axPipeInput struct {
	// Pipeline is a //-separated sequence of stages, e.g.:
	//   "app com.apple.dt.Xcode // windows"
	//   "app com.apple.finder // focus // attr AXTitle"
	Pipeline string `json:"pipeline"`
}

func registerAXPipe(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "ax_pipe",
		Description: `Execute an AX pipeline and return its text output.

Stages separated by // (double-slash):
  app <bundle-id|pid>              open application
  window [substr]                  focus first matching window
  windows                          list all windows
  focus                            get focused element
  children                         get children
  first                            take first element from list
  find [--role R] [--title T]      search descendants
  .                                print current context (default)
  tree [--depth N]                 element tree
  list                             element list
  json                             JSON output
  click                            click element
  type <text>                      type text
  attr <AXAttr>                    print attribute
  click-menu <A->B->C>             click menu path

Examples:
  app com.apple.dt.Xcode // windows
  app com.apple.dt.Xcode // window // find --role AXButton // list
  app com.apple.finder // focus // attr AXTitle`,
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axPipeInput) (*mcp.CallToolResult, any, error) {
		out, err := execPipeline(args.Pipeline)
		if err != nil {
			return nil, nil, err
		}
		return textResult(out), nil, nil
	})
}

// ── ax_click ──────────────────────────────────────────────────────────────────

type axClickInput struct {
	App      string `json:"app"`
	Contains string `json:"contains"`
	Role     string `json:"role,omitempty"`
	XOffset  *int   `json:"x_offset,omitempty"`
	YOffset  *int   `json:"y_offset,omitempty"`
}

func registerAXClick(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "ax_click",
		Description: "Click an element in an app found by title substring (optionally filtered by role). Provide x_offset and y_offset to click at a specific point relative to the element's top-left corner.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axClickInput) (*mcp.CallToolResult, any, error) {
		app, err := spinAndOpen(args.App)
		if err != nil {
			return nil, nil, err
		}
		defer app.Close()

		q := app.Descendants().WithLimit(100).ByTitleContains(args.Contains)
		if args.Role != "" {
			q = q.ByRole(args.Role)
		}
		el := q.First()
		if el == nil {
			return nil, nil, fmt.Errorf("element containing %q not found", args.Contains)
		}

		if args.XOffset != nil && args.YOffset != nil {
			if err := el.ClickAt(*args.XOffset, *args.YOffset); err != nil {
				return nil, nil, fmt.Errorf("click_at: %w", err)
			}
			return textResult(fmt.Sprintf("clicked %s %q at offset %d,%d", el.Role(), el.Title(), *args.XOffset, *args.YOffset)), nil, nil
		}

		if err := el.Click(); err != nil {
			return nil, nil, fmt.Errorf("click: %w", err)
		}
		return textResult(fmt.Sprintf("clicked %s %q", el.Role(), el.Title())), nil, nil
	})
}

// ── ax_type ───────────────────────────────────────────────────────────────────

type axTypeInput struct {
	App      string `json:"app"`
	Contains string `json:"contains"`
	Text     string `json:"text"`
}

func registerAXType(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "ax_type",
		Description: "Type text into an element found by title substring",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axTypeInput) (*mcp.CallToolResult, any, error) {
		app, err := spinAndOpen(args.App)
		if err != nil {
			return nil, nil, err
		}
		defer app.Close()

		el := app.Descendants().WithLimit(100).ByTitleContains(args.Contains).First()
		if el == nil {
			el = app.Descendants().WithLimit(100).ByRole("AXTextField").ByTitleContains(args.Contains).First()
		}
		if el == nil {
			return nil, nil, fmt.Errorf("element containing %q not found", args.Contains)
		}
		if err := el.Click(); err != nil {
			return nil, nil, fmt.Errorf("focus: %w", err)
		}
		if err := el.TypeText(args.Text); err != nil {
			return nil, nil, fmt.Errorf("type: %w", err)
		}
		return textResult(fmt.Sprintf("typed into %s %q", el.Role(), el.Title())), nil, nil
	})
}

// ── ax_menu ───────────────────────────────────────────────────────────────────

type axMenuInput struct {
	App  string   `json:"app"`
	Path []string `json:"path"`
}

func registerAXMenu(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "ax_menu",
		Description: `Click a menu item by path array, e.g. ["File", "New", "Target..."]`,
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axMenuInput) (*mcp.CallToolResult, any, error) {
		app, err := spinAndOpen(args.App)
		if err != nil {
			return nil, nil, err
		}
		defer app.Close()

		if err := app.ClickMenuItem(args.Path); err != nil {
			return nil, nil, fmt.Errorf("menu: %w", err)
		}
		return textResult("clicked menu: " + strings.Join(args.Path, " > ")), nil, nil
	})
}

// ── ax_screenshot ─────────────────────────────────────────────────────────────

type axScreenshotInput struct {
	App      string `json:"app"`
	Contains string `json:"contains,omitempty"`
	Role     string `json:"role,omitempty"`
}

func registerAXScreenshot(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "ax_screenshot",
		Description: "Capture a screenshot of an app window or specific element. Omit contains/role to capture the first window.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axScreenshotInput) (*mcp.CallToolResult, any, error) {
		app, err := spinAndOpen(args.App)
		if err != nil {
			return nil, nil, err
		}
		defer app.Close()

		var el *axuiautomation.Element
		if args.Contains != "" || args.Role != "" {
			q := app.Descendants().WithLimit(100)
			if args.Role != "" {
				q = q.ByRole(args.Role)
			}
			if args.Contains != "" {
				q = q.ByTitleContains(args.Contains)
			}
			el = q.First()
			if el == nil {
				return nil, nil, fmt.Errorf("element not found (try being less specific)")
			}
		} else {
			wins := app.WindowList()
			if len(wins) == 0 {
				return nil, nil, fmt.Errorf("no windows found to screenshot")
			}
			el = wins[0]
		}

		png, err := el.Screenshot()
		if err != nil {
			if strings.Contains(err.Error(), "could not create image from rect") || strings.Contains(err.Error(), "screencapture: exit status") {
				return nil, nil, fmt.Errorf("screenshot failed: %w (Ensure Terminal/host has Screen Recording permissions in System Settings > Privacy & Security, and that the element is not fully occluded or off-screen)", err)
			}
			return nil, nil, fmt.Errorf("screenshot: %w", err)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.ImageContent{Data: png, MIMEType: "image/png"}},
		}, nil, nil
	})
}

// ── ax_focus ──────────────────────────────────────────────────────────────────

type axFocusInput struct {
	App string `json:"app"`
}

func registerAXFocus(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "ax_focus",
		Description: "Get the currently focused AX element in an application",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axFocusInput) (*mcp.CallToolResult, any, error) {
		app, err := spinAndOpen(args.App)
		if err != nil {
			return nil, nil, err
		}
		defer app.Close()

		el := app.FocusedElement()
		if el == nil {
			// Fallback: perhaps the app has no focused element, but has a main window
			win := app.MainWindow()
			if win != nil {
				return textResult(fmt.Sprintf("no focused element; main window is: %s", elementSummary(win))), nil, nil
			}
			return nil, nil, fmt.Errorf("no focused element and no main window found (app might be in background or has no standard UI)")
		}
		return textResult(elementSummary(el)), nil, nil
	})
}
