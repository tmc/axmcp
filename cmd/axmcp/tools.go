package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ebitengine/purego"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/xcmcp/internal/ui"
)

// axTimeout is the AX messaging timeout applied to all opened apps.
// If an app's accessibility implementation doesn't respond within this
// duration, AX calls return kAXErrorCannotComplete instead of hanging.
const axTimeout = 5 // seconds

var (
	axSetMessagingTimeout     func(element uintptr, timeoutInSeconds float32) int32
	axSetMessagingTimeoutOnce sync.Once
)

func initAXSetMessagingTimeout() {
	axSetMessagingTimeoutOnce.Do(func() {
		lib, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			slog.Warn("failed to load ApplicationServices for AXUIElementSetMessagingTimeout", "err", err)
			return
		}
		purego.RegisterLibFunc(&axSetMessagingTimeout, lib, "AXUIElementSetMessagingTimeout")
	})
}

// setAXTimeout sets the messaging timeout on an AX element so that
// AXUIElementCopyAttributeValue returns kAXErrorCannotComplete instead
// of blocking indefinitely on unresponsive apps.
func setAXTimeout(app *axuiautomation.Application) {
	initAXSetMessagingTimeout()
	if axSetMessagingTimeout == nil {
		return
	}
	root := app.Root()
	if root == nil {
		return
	}
	axSetMessagingTimeout(root.Ref(), axTimeout)
}

func registerAXTools(s *mcp.Server) {
	registerAXApps(s)
	registerAXTree(s)
	registerAXFind(s)
	registerAXPipe(s)
	registerAXClick(s)
	registerAXType(s)
	registerAXMenu(s)
	registerAXFocus(s)
	registerAXListWindows(s)
	registerAXScreenshot(s)
	registerAXOCR(s)
	registerAXOCRDiff(s)
	registerAXInteractionTools(s)
	registerAXWindowTools(s)
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

// spinAndOpen opens an app, sets an AX messaging timeout, and spins
// the run loop to prime AX IPC.
func spinAndOpen(arg string) (*axuiautomation.Application, error) {
	app, err := openApp(arg)
	if err != nil {
		return nil, err
	}
	setAXTimeout(app)
	axuiautomation.SpinRunLoop(200 * time.Millisecond)
	return app, nil
}

func elementAttrs(e *axuiautomation.Element) map[string]any {
	x, y := e.Position()
	w, h := e.Size()
	// For checkboxes and switches, AXValue is a CFNumber (0/1) which
	// Value() can't read (returns ""). Use IsChecked() instead.
	var val any
	role := e.Role()
	if role == "AXCheckBox" || role == "AXSwitch" || role == "AXRadioButton" {
		if e.IsChecked() {
			val = 1
		} else {
			val = 0
		}
	} else {
		val = e.Value()
	}
	return map[string]any{
		"role": role, "title": e.Title(), "value": val,
		"subrole": e.Subrole(), "enabled": e.IsEnabled(),
		"role_desc": e.RoleDescription(),
		"desc":      e.Description(), "identifier": e.Identifier(),
		"x": x, "y": y, "w": w, "h": h,
	}
}

func elementSummary(e *axuiautomation.Element) string {
	return formatSnapshot(snapshotElement(e, 0, 0))
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
	Window   string `json:"window,omitempty"`
	Role     string `json:"role,omitempty"`
	Title    string `json:"title,omitempty"`
	Contains string `json:"contains,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

func registerAXFind(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "ax_find",
		Description: "Find AX elements in an app by role, exact text, or substring across title, description, value, and identifier. " +
			"Set window to scope the search to a specific window title substring.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axFindInput) (*mcp.CallToolResult, any, error) {
		app, err := spinAndOpen(args.App)
		if err != nil {
			return nil, nil, err
		}
		defer app.Close()

		limit := args.Limit
		if limit <= 0 {
			limit = 500
		}
		root, _, err := resolveSearchRoot(app, args.Window)
		if err != nil {
			return nil, nil, err
		}
		result := findElements(root, searchOptions{
			Role:     args.Role,
			Title:    args.Title,
			Contains: args.Contains,
			Limit:    limit,
		})
		var buf bytes.Buffer
		if len(result.matches) == 0 {
			buf.WriteString(noMatchMessage(result))
			if hint := ocrNoMatchHint(args.App, args.Window, primaryQuery(result.options)); hint != "" {
				buf.WriteString(hint)
			}
			return textResult(buf.String()), nil, nil
		}
		if note := selectionReason(result); note != "" {
			buf.WriteString(note)
			buf.WriteByte('\n')
		}
		for i, match := range result.matches {
			fmt.Fprintf(&buf, "[%d] %s\n", i, formatMatch(match))
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
  find [--role R] [--title T]      search descendants using normalized text matching
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
	Window   string `json:"window,omitempty"`
	Contains string `json:"contains"`
	Role     string `json:"role,omitempty"`
	XOffset  *int   `json:"x_offset,omitempty"`
	YOffset  *int   `json:"y_offset,omitempty"`
}

func registerAXClick(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "ax_click",
		Description: "Click an element in an app found by normalized text lookup across title, description, value, and identifier. " +
			"Set window to scope the search to a specific window title substring. " +
			"Provide x_offset and y_offset to click at a specific point relative to the element's top-left corner. " +
			"Use the window parameter to avoid matching system menu items when targeting in-window elements.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axClickInput) (*mcp.CallToolResult, any, error) {
		app, err := spinAndOpen(args.App)
		if err != nil {
			return nil, nil, err
		}
		defer app.Close()

		root, _, err := resolveSearchRoot(app, args.Window)
		if err != nil {
			return nil, nil, err
		}
		result := findElements(root, searchOptions{
			Role:     args.Role,
			Contains: args.Contains,
			Limit:    500,
		})
		if len(result.matches) == 0 {
			msg := noMatchMessage(result)
			if hint := ocrNoMatchHint(args.App, args.Window, primaryQuery(result.options)); hint != "" {
				msg += hint
			}
			return nil, nil, fmt.Errorf("%s", msg)
		}

		match := result.matches[0]
		resolution := resolveClickTarget(match, 500)
		target := resolution.target.element
		if target == nil {
			return nil, nil, fmt.Errorf("click target disappeared: %s", formatMatch(match))
		}

		if args.XOffset != nil && args.YOffset != nil {
			if err := clickLocalPoint(target, *args.XOffset, *args.YOffset); err != nil {
				return nil, nil, fmt.Errorf("click_at %s: %w", formatSnapshot(resolution.target), err)
			}
			var buf bytes.Buffer
			fmt.Fprintf(&buf, "clicked %s at offset %d,%d", formatSnapshot(resolution.target), *args.XOffset, *args.YOffset)
			if note := selectionReason(result); note != "" {
				fmt.Fprintf(&buf, "\n%s", note)
			}
			if resolution.reason != "" {
				fmt.Fprintf(&buf, "\n%s", resolution.reason)
			}
			return textResult(buf.String()), nil, nil
		}

		clickSummary, err := performDefaultClick(resolution.target)
		if err != nil {
			if !match.snapshot.record.actionable && len(resolution.actionableDescendants) > 1 {
				var b strings.Builder
				fmt.Fprintf(&b, "click %s: %v\n", formatMatch(match), err)
				b.WriteString("actionable descendants:\n")
				for _, descendant := range resolution.actionableDescendants[:min(len(resolution.actionableDescendants), 5)] {
					fmt.Fprintf(&b, "  - %s\n", formatSnapshot(descendant))
				}
				return nil, nil, fmt.Errorf("%s", strings.TrimRight(b.String(), "\n"))
			}
			return nil, nil, fmt.Errorf("click %s: %w", formatSnapshot(resolution.target), err)
		}
		var buf bytes.Buffer
		buf.WriteString(clickSummary)
		if note := selectionReason(result); note != "" {
			fmt.Fprintf(&buf, "\n%s", note)
		}
		if resolution.reason != "" {
			fmt.Fprintf(&buf, "\n%s", resolution.reason)
		}
		return textResult(buf.String()), nil, nil
	})
}

// ── ax_type ───────────────────────────────────────────────────────────────────

type axTypeInput struct {
	App      string `json:"app"`
	Contains string `json:"contains,omitempty"`
	Text     string `json:"text"`
}

func registerAXType(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "ax_type",
		Description: "Type text into an element found by normalized text lookup across title, description, value, and identifier. " +
			"When contains is omitted, types into the currently focused element.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axTypeInput) (*mcp.CallToolResult, any, error) {
		app, err := spinAndOpen(args.App)
		if err != nil {
			return nil, nil, err
		}
		defer app.Close()

		// When contains is omitted, type into the focused element.
		if args.Contains == "" {
			el := app.FocusedElement()
			if el == nil {
				return nil, nil, fmt.Errorf("no focused element found")
			}
			role := el.Role()
			useSetValue := role == "AXTextField" || role == "AXTextArea" || role == "AXComboBox"
			if useSetValue {
				if err := el.SetValue(args.Text); err == nil {
					return textResult(fmt.Sprintf("set value on focused %s", elementSummary(el))), nil, nil
				}
			}
			if err := el.TypeText(args.Text); err != nil {
				return nil, nil, fmt.Errorf("type into focused element: %w", err)
			}
			return textResult(fmt.Sprintf("typed into focused %s", elementSummary(el))), nil, nil
		}

		result := findElements(app.Root(), searchOptions{
			Contains: args.Contains,
			Limit:    500,
		})
		if len(result.matches) == 0 {
			return nil, nil, fmt.Errorf("%s", noMatchMessage(result))
		}
		el := result.matches[0].snapshot.element
		if el == nil {
			return nil, nil, fmt.Errorf("type target disappeared: %s", formatMatch(result.matches[0]))
		}
		// For text fields, prefer SetValue to avoid cursor warp from CGEvent click.
		role := el.Role()
		useSetValue := role == "AXTextField" || role == "AXTextArea" || role == "AXComboBox"
		if useSetValue {
			if err := el.SetValue(args.Text); err == nil {
				var buf bytes.Buffer
				fmt.Fprintf(&buf, "set value on %s", formatMatch(result.matches[0]))
				if note := selectionReason(result); note != "" {
					fmt.Fprintf(&buf, "\n%s", note)
				}
				return textResult(buf.String()), nil, nil
			}
		}
		if err := el.Click(); err != nil {
			return nil, nil, fmt.Errorf("focus %s: %w", formatMatch(result.matches[0]), err)
		}
		if err := el.TypeText(args.Text); err != nil {
			return nil, nil, fmt.Errorf("type %s: %w", formatMatch(result.matches[0]), err)
		}
		var buf bytes.Buffer
		fmt.Fprintf(&buf, "typed into %s", formatMatch(result.matches[0]))
		if note := selectionReason(result); note != "" {
			fmt.Fprintf(&buf, "\n%s", note)
		}
		return textResult(buf.String()), nil, nil
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

// ── ax_list_windows ───────────────────────────────────────────────────────────

type axListWindowsInput struct {
	App string `json:"app"`
}

func registerAXListWindows(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "ax_list_windows",
		Description: "List windows for an application by name or bundle ID. Returns window IDs, titles, and bounds.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axListWindowsInput) (*mcp.CallToolResult, any, error) {
		app, err := spinAndOpen(args.App)
		if err != nil {
			return nil, nil, err
		}
		defer app.Close()

		wins := app.WindowList()
		if len(wins) == 0 {
			return nil, nil, fmt.Errorf("no windows found for %q", args.App)
		}
		type winInfo struct {
			Title  string `json:"title"`
			X      int    `json:"x"`
			Y      int    `json:"y"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
		}
		result := make([]winInfo, 0, len(wins))
		for _, w := range wins {
			x, y := w.Position()
			width, height := w.Size()
			result = append(result, winInfo{
				Title:  w.Title(),
				X:      x,
				Y:      y,
				Width:  width,
				Height: height,
			})
		}
		data, err := json.Marshal(result)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal: %w", err)
		}
		return textResult(string(data)), nil, nil
	})
}

// ── ax_screenshot ─────────────────────────────────────────────────────────────

type axScreenshotInput struct {
	App          string `json:"app"`
	Window       string `json:"window,omitempty"`
	Contains     string `json:"contains,omitempty"`
	Role         string `json:"role,omitempty"`
	ArtifactPath string `json:"artifact_path,omitempty"`
	FullScreen   bool   `json:"full_screen,omitempty"`
}

func registerAXScreenshot(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "ax_screenshot",
		Description: `Capture a screenshot of an app window or specific element.

Prefer targeting a specific element with contains/role for smaller, faster, more token-efficient results. Set window to a title substring when an app has multiple windows and you want a specific one. Full app window captures are larger and should only be used when you need to see the complete window layout.

Set full_screen=true to capture the entire display (requires explicit opt-in due to large image size). Set artifact_path to save the PNG to a durable file.`,
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axScreenshotInput) (*mcp.CallToolResult, any, error) {
		var png []byte
		if args.FullScreen {
			var err error
			png, err = captureFullScreen()
			if err != nil {
				return nil, nil, err
			}
		} else if args.Contains != "" || args.Role != "" {
			// Element screenshot: need AX to find the element.
			app, err := spinAndOpen(args.App)
			if err != nil {
				return nil, nil, err
			}
			defer app.Close()

			root := app.Root()
			if args.Window != "" {
				win, _, err := resolveWindow(app, args.Window)
				if err != nil {
					return nil, nil, err
				}
				root = win
			}
			result := findElements(root, searchOptions{
				Role:     args.Role,
				Contains: args.Contains,
				Limit:    500,
			})
			if len(result.matches) == 0 {
				return nil, nil, fmt.Errorf("%s", noMatchMessage(result))
			}
			el := result.matches[0].snapshot.element
			png, err = captureElementOrWindow(args.App, true, el)
			if err != nil {
				return nil, nil, err
			}
		} else {
			// Full window screenshot: try SCK/CGWindowList first (no AX IPC needed,
			// avoids hanging on apps with unresponsive accessibility implementations).
			var err error
			png, err = captureWindowByTitle(args.App, args.Window)
			if err != nil {
				return nil, nil, err
			}
		}

		content := []mcp.Content{&mcp.ImageContent{Data: png, MIMEType: "image/png"}}
		if args.ArtifactPath != "" {
			if err := writePNGArtifact(args.ArtifactPath, png); err != nil {
				return nil, nil, err
			}
			content = append([]mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("saved screenshot to %s", filepath.Clean(args.ArtifactPath))},
			}, content...)
		}
		return &mcp.CallToolResult{
			Content: content,
		}, nil, nil
	})
}

// captureWindowByName captures a full window screenshot using CGWindowList,
// without any AX IPC. This avoids hanging on apps whose accessibility
// implementation is unresponsive (e.g. VM windows).
func captureWindowByName(appName string) ([]byte, error) {
	diagf("captureWindowByName: start app=%s\n", appName)

	diagf("captureWindowByName: listing windows\n")
	windows, err := listAppWindows(appName)
	if err != nil {
		return nil, fmt.Errorf("no windows found for %q: %w", appName, err)
	}
	diagf("captureWindowByName: found %d windows, firstID=%d\n", len(windows), windows[0].WindowID)

	png, err := captureWindow(windows[0])
	if err != nil {
		return nil, err
	}
	diagf("captureWindowByName: success, %d bytes\n", len(png))
	return png, nil
}

func captureWindowByTitle(appName, title string) ([]byte, error) {
	if title == "" {
		return captureWindowByName(appName)
	}
	windows, err := listAppWindows(appName)
	if err != nil {
		return nil, fmt.Errorf("no windows found for %q: %w", appName, err)
	}
	win, ok := matchWindowInfo(windows, title)
	if !ok {
		return nil, fmt.Errorf("no window matching %q found for %q", title, appName)
	}
	return captureWindow(win)
}

// captureElementOrWindow abstracts the logic to capture a screenshot.
// If isElement is true, it attempts an element screenshot.
// Otherwise it uses the dedicated window capture path and falls back to an AX
// rect screenshot if needed.
func captureElementOrWindow(appName string, isElement bool, el *axuiautomation.Element) ([]byte, error) {
	diagf("captureElementOrWindow: app=%s isElement=%v\n", appName, isElement)
	if !ui.IsScreenRecordingTrusted() {
		diagf("captureElementOrWindow: waiting for screen recording\n")
		if !ui.WaitForScreenRecording(30 * time.Second) {
			return nil, fmt.Errorf("screenshot failed: Screen Recording permission required — grant access in System Settings > Privacy & Security")
		}
	}

	if !isElement {
		// Try the window-specific capture path first. It stays on synchronous
		// capture APIs and avoids the ScreenCaptureKit process-exit edge cases.
		windows, err := listAppWindows(appName)
		if err == nil && len(windows) > 0 {
			diagf("captureElementOrWindow: trying window capture windowID=%d\n", windows[0].WindowID)
			if png, err := captureWindow(windows[0]); err == nil {
				diagf("captureElementOrWindow: window capture success %d bytes\n", len(png))
				return png, nil
			}
			diagf("captureElementOrWindow: window capture failed, falling back to AX element screenshot\n")
		}
	}

	// Fallback to accessibility element screenshot.
	diagf("captureElementOrWindow: falling back to AX element screenshot\n")
	png, err := el.Screenshot()
	if err != nil {
		diagf("captureElementOrWindow: AX screenshot failed: %v\n", err)
		return nil, fmt.Errorf("screenshot: %w", err)
	}
	diagf("captureElementOrWindow: AX screenshot success %d bytes\n", len(png))
	return png, nil
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
			win := app.MainWindow()
			if win == nil {
				windows := app.WindowList()
				if len(windows) > 0 {
					win = windows[0]
				}
			}
			if win != nil {
				return textResult(fmt.Sprintf("no focused element; window fallback is: %s", elementSummary(win))), nil, nil
			}
			root := app.Root()
			if root != nil {
				return textResult(fmt.Sprintf("no focused element; app root is: %s", elementSummary(root))), nil, nil
			}
			return nil, nil, fmt.Errorf("no focused element or window found")
		}
		return textResult(elementSummary(el)), nil, nil
	})
}

type axOCRInput struct {
	App      string `json:"app"`
	Window   string `json:"window,omitempty"`
	Contains string `json:"contains,omitempty"`
	Role     string `json:"role,omitempty"`
	Find     string `json:"find,omitempty"`
	JSON     bool   `json:"json,omitempty"`
	Layout   bool   `json:"layout,omitempty"`
	Cols     int    `json:"cols,omitempty"`
	Rows     int    `json:"rows,omitempty"`
}

func registerAXOCR(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "ax_ocr",
		Description: "Run Apple Vision OCR on an app window or scoped AX element. " +
			"Returns recognized text with coordinates in the local space of the captured window or element, plus absolute screen coordinates derived from that target frame. " +
			"Set window to target a specific window title substring. Use contains/role to OCR a specific AX element such as a sidebar outline. " +
			"Use 'find' to search for specific text. " +
			"Use 'layout' for a spatial ASCII rendering that preserves text positions. " +
			"Useful for VMs, custom-drawn UIs, and elements without accessibility text.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axOCRInput) (*mcp.CallToolResult, any, error) {
		capture, err := captureOCRScope(args.App, args.Window, args.Contains, args.Role)
		if err != nil {
			return nil, nil, err
		}
		defer capture.Close()

		results := capture.result
		if args.Find != "" {
			results = findOCRText(results, args.Find)
			if len(results) == 0 {
				return nil, nil, fmt.Errorf("no text matching %q found", args.Find)
			}
		}
		if args.Layout {
			cols, rows := 120, 40
			if args.Cols > 0 {
				cols = args.Cols
			}
			if args.Rows > 0 {
				rows = args.Rows
			}
			return textResult(renderOCRLayout(results, capture.imgW, capture.imgH, cols, rows)), nil, nil
		}
		if args.JSON {
			out, err := formatOCRResultsJSON(results, capture.target)
			if err != nil {
				return nil, nil, err
			}
			return textResult(out), nil, nil
		}
		return textResult(formatOCRResults(results, capture.target)), nil, nil
	})
}
