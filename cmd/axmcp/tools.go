package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/ghostcursor"
	"github.com/tmc/axmcp/internal/spacedetect"
	"github.com/tmc/axmcp/internal/ui"
)

// axTimeout is the AX messaging timeout applied to all opened apps.
// If an app's accessibility implementation doesn't respond within this
// duration, AX calls return kAXErrorCannotComplete instead of hanging.
const axTimeout = 5 // seconds

var (
	axSetMessagingTimeout     func(element uintptr, timeoutInSeconds float32) int32
	axCopyActionNames         func(element uintptr, names *uintptr) int32
	axSetMessagingTimeoutOnce sync.Once
	axCopyActionNamesOnce     sync.Once
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

func initAXCopyActionNames() {
	axCopyActionNamesOnce.Do(func() {
		lib, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			slog.Warn("failed to load ApplicationServices for AXUIElementCopyActionNames", "err", err)
			return
		}
		purego.RegisterLibFunc(&axCopyActionNames, lib, "AXUIElementCopyActionNames")
	})
}

func actionNamesForElement(element *axuiautomation.Element) []string {
	if element == nil {
		return nil
	}
	initAXCopyActionNames()
	if axCopyActionNames == nil {
		return nil
	}
	var names uintptr
	if axCopyActionNames(element.Ref(), &names) != 0 || names == 0 {
		return nil
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(names))

	count := corefoundation.CFArrayGetCount(corefoundation.CFArrayRef(names))
	out := make([]string, 0, count)
	for i := range count {
		ptr := corefoundation.CFArrayGetValueAtIndex(corefoundation.CFArrayRef(names), i)
		name := cfStringToGo(corefoundation.CFStringRef(uintptr(ptr)))
		if name != "" {
			out = append(out, name)
		}
	}
	return out
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
	registerAXSnapshot(s)
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

// openApp opens an application by bundle ID, numeric PID, or display name.
// When the standard lookup fails (common with Electron apps that register as
// "Electron" instead of their display name), falls back to scanning
// CGWindowList for windows whose title contains the query and uses the
// owning process's PID.
func openApp(arg string) (*axuiautomation.Application, error) {
	if pid, err := strconv.ParseInt(arg, 10, 32); err == nil {
		app := axuiautomation.NewApplicationFromPID(int32(pid))
		if app == nil {
			return nil, fmt.Errorf("cannot connect to PID %d", pid)
		}
		return app, nil
	}
	app, err := axuiautomation.NewApplication(arg)
	if err == nil {
		return app, nil
	}
	// Fallback: scan CGWindowList for a window title containing the query.
	// This handles Electron apps whose process name differs from the app name.
	pid, found := findPIDByWindowTitle(arg)
	if !found {
		return nil, err
	}
	slog.Debug("openApp: resolved via window title", "query", arg, "pid", pid)
	app = axuiautomation.NewApplicationFromPID(pid)
	if app == nil {
		return nil, fmt.Errorf("cannot connect to PID %d (found via window title %q)", pid, arg)
	}
	return app, nil
}

// findPIDByWindowTitle scans CGWindowList for a window whose title contains the
// given query (case-insensitive) and returns the owning process's PID. It first
// checks on-screen windows, then falls back to all windows (other Spaces/displays).
func findPIDByWindowTitle(query string) (int32, bool) {
	for _, option := range []coregraphics.CGWindowListOption{
		coregraphics.KCGWindowListOptionOnScreenOnly,
		coregraphics.KCGWindowListOptionAll,
	} {
		if pid, ok := findPIDByWindowTitleWithOption(query, option); ok {
			return pid, true
		}
	}
	return 0, false
}

func findPIDByWindowTitleWithOption(query string, option coregraphics.CGWindowListOption) (int32, bool) {
	windowList := coregraphics.CGWindowListCopyWindowInfo(option, 0)
	if windowList == 0 {
		return 0, false
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(windowList))

	lower := strings.ToLower(query)
	count := corefoundation.CFArrayGetCount(windowList)
	for i := range count {
		dictPtr := corefoundation.CFArrayGetValueAtIndex(windowList, i)
		dict := corefoundation.CFDictionaryRef(uintptr(dictPtr))
		title := dictGetString(dict, coregraphics.KCGWindowName)
		if title != "" && strings.Contains(strings.ToLower(title), lower) {
			pid, ok := dictGetNumber(dict, coregraphics.KCGWindowOwnerPID)
			if ok && pid > 0 {
				return int32(pid), true
			}
		}
	}
	return 0, false
}

// spinAndOpen opens an app, sets an AX messaging timeout, and spins
// the run loop to prime AX IPC.
func spinAndOpen(arg string) (*axuiautomation.Application, error) {
	if !ui.WaitForAccessibility(30 * time.Second) {
		return nil, fmt.Errorf("Accessibility permission required — grant access in System Settings > Privacy & Security")
	}
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
		"secondary_actions": actionNamesForElement(e),
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

type axTreeNode struct {
	Index            int      `json:"index"`
	ParentIndex      int      `json:"parent_index"`
	Depth            int      `json:"depth"`
	Role             string   `json:"role,omitempty"`
	Title            string   `json:"title,omitempty"`
	Value            string   `json:"value,omitempty"`
	Description      string   `json:"description,omitempty"`
	Identifier       string   `json:"identifier,omitempty"`
	RoleDescription  string   `json:"role_description,omitempty"`
	X                int      `json:"x,omitempty"`
	Y                int      `json:"y,omitempty"`
	Width            int      `json:"width,omitempty"`
	Height           int      `json:"height,omitempty"`
	Enabled          bool     `json:"enabled"`
	Visible          bool     `json:"visible"`
	Actionable       bool     `json:"actionable"`
	SecondaryActions []string `json:"secondary_actions,omitempty"`
}

type axWindowSnapshot struct {
	Title            string `json:"title,omitempty"`
	X                int    `json:"x,omitempty"`
	Y                int    `json:"y,omitempty"`
	Width            int    `json:"width,omitempty"`
	Height           int    `json:"height,omitempty"`
	ScreenshotWidth  int    `json:"screenshot_width,omitempty"`
	ScreenshotHeight int    `json:"screenshot_height,omitempty"`
}

type axTreePayload struct {
	App                 string            `json:"app,omitempty"`
	Scope               string            `json:"scope,omitempty"`
	Window              axWindowSnapshot  `json:"window"`
	Tree                []axTreeNode      `json:"tree"`
	OCR                 []ocrOutputResult `json:"ocr,omitempty"`
	ScreenshotPNGBase64 string            `json:"screenshot_png_base64,omitempty"`
}

func collectAXTreeNodes(root *axuiautomation.Element, maxDepth int) []axTreeNode {
	if root == nil {
		return nil
	}
	if maxDepth < 0 {
		maxDepth = 0
	}
	type queueItem struct {
		element *axuiautomation.Element
		parent  int
		depth   int
	}
	queue := []queueItem{{element: root, parent: -1}}
	nodes := make([]axTreeNode, 0, 128)
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		if item.element == nil || item.depth > maxDepth {
			continue
		}
		index := len(nodes)
		snapshot := snapshotElement(item.element, item.depth, index)
		record := snapshot.record
		nodes = append(nodes, axTreeNode{
			Index:            index,
			ParentIndex:      item.parent,
			Depth:            item.depth,
			Role:             record.role,
			Title:            record.title,
			Value:            record.value,
			Description:      record.desc,
			Identifier:       record.identifier,
			RoleDescription:  record.roleDescription,
			X:                record.x,
			Y:                record.y,
			Width:            record.w,
			Height:           record.h,
			Enabled:          record.enabled,
			Visible:          record.visible,
			Actionable:       record.actionable,
			SecondaryActions: actionNamesForElement(item.element),
		})
		if item.depth == maxDepth {
			continue
		}
		for _, child := range item.element.Children() {
			queue = append(queue, queueItem{element: child, parent: index, depth: item.depth + 1})
		}
	}
	return nodes
}

func treeTextIndexed(nodes []axTreeNode) string {
	var b strings.Builder
	for _, node := range nodes {
		b.WriteString(strings.Repeat("  ", node.Depth))
		fmt.Fprintf(&b, "[%d] %s", node.Index, node.Role)
		if node.Title != "" {
			fmt.Fprintf(&b, " title=%q", node.Title)
		}
		if node.Description != "" && node.Description != node.Title {
			fmt.Fprintf(&b, " desc=%q", node.Description)
		}
		if node.Value != "" && node.Value != node.Title && node.Value != node.Description {
			fmt.Fprintf(&b, " value=%q", node.Value)
		}
		if node.Identifier != "" {
			fmt.Fprintf(&b, " id=%q", node.Identifier)
		}
		if len(node.SecondaryActions) > 0 {
			fmt.Fprintf(&b, " actions=%q", strings.Join(node.SecondaryActions, ","))
		}
		fmt.Fprintf(&b, " bounds=(%d,%d %dx%d)\n", node.X, node.Y, node.Width, node.Height)
	}
	return b.String()
}

func resolveSnapshotRoot(app *axuiautomation.Application, window string, appRoot bool) (*axuiautomation.Element, string, error) {
	if appRoot {
		return app.Root(), fmt.Sprintf("app %q", app.BundleID()), nil
	}
	win, desc, err := resolveWindow(app, window)
	if err == nil {
		return win, desc, nil
	}
	if window != "" {
		return nil, "", err
	}
	return app.Root(), fmt.Sprintf("app %q", app.BundleID()), nil
}

func windowSnapshot(root *axuiautomation.Element, png []byte) axWindowSnapshot {
	if root == nil {
		return axWindowSnapshot{}
	}
	record := snapshotElement(root, 0, 0).record
	info := axWindowSnapshot{
		Title:  record.title,
		X:      record.x,
		Y:      record.y,
		Width:  record.w,
		Height: record.h,
	}
	if len(png) > 0 {
		if w, h, err := pngDimensions(png); err == nil {
			info.ScreenshotWidth = w
			info.ScreenshotHeight = h
		}
	}
	return info
}

func buildAXTreePayload(appName string, root *axuiautomation.Element, scope string, depth int, includeScreenshot, includeOCR bool) (axTreePayload, error) {
	var png []byte
	if includeScreenshot || includeOCR {
		var err error
		png, err = root.Screenshot()
		if err != nil {
			return axTreePayload{}, fmt.Errorf("screenshot %s: %w", scope, err)
		}
	}
	payload := axTreePayload{
		App:    appName,
		Scope:  scope,
		Window: windowSnapshot(root, png),
		Tree:   collectAXTreeNodes(root, depth),
	}
	if includeOCR {
		w, h, err := pngDimensions(png)
		if err != nil {
			return axTreePayload{}, err
		}
		results, err := recognizeText(png, w, h)
		if err != nil {
			return axTreePayload{}, err
		}
		payload.OCR = expandOCRResults(results, root)
	}
	if includeScreenshot && len(png) > 0 {
		payload.ScreenshotPNGBase64 = base64.StdEncoding.EncodeToString(png)
	}
	return payload, nil
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
	App        string `json:"app"`
	Window     string `json:"window,omitempty"`
	Depth      int    `json:"depth,omitempty"`
	JSON       bool   `json:"json,omitempty"`
	OCR        bool   `json:"ocr,omitempty"`
	Screenshot bool   `json:"screenshot,omitempty"`
	AppRoot    bool   `json:"app_root,omitempty"`
}

func registerAXTree(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "ax_tree",
		Description: "Print an indexed AX element tree. Defaults to the first app window; set window to a title substring, app_root=true for the full app tree, json=true for structured output, ocr=true for OCR blocks, and screenshot=true to include a base64 PNG.",
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
		root, scope, err := resolveSnapshotRoot(app, args.Window, args.AppRoot)
		if err != nil {
			return nil, nil, err
		}
		payload, err := buildAXTreePayload(args.App, root, scope, depth, args.Screenshot, args.OCR)
		if err != nil {
			return nil, nil, err
		}
		if args.JSON {
			data, err := json.MarshalIndent(payload, "", "  ")
			if err != nil {
				return nil, nil, fmt.Errorf("marshal: %w", err)
			}
			return textResult(string(data)), nil, nil
		}
		return textResult(treeTextIndexed(payload.Tree)), nil, nil
	})
}

// ── ax_snapshot ───────────────────────────────────────────────────────────────

type axSnapshotInput struct {
	App               string `json:"app"`
	Window            string `json:"window,omitempty"`
	Depth             int    `json:"depth,omitempty"`
	IncludeScreenshot bool   `json:"include_screenshot,omitempty"`
	IncludeOCR        bool   `json:"include_ocr,omitempty"`
	AppRoot           bool   `json:"app_root,omitempty"`
}

func registerAXSnapshot(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "ax_snapshot",
		Description: "Return a structured AX snapshot with app/window metadata, indexed tree, secondary actions, and optional OCR blocks or base64 screenshot. Defaults to the first app window.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axSnapshotInput) (*mcp.CallToolResult, any, error) {
		app, err := spinAndOpen(args.App)
		if err != nil {
			return nil, nil, err
		}
		defer app.Close()
		depth := args.Depth
		if depth <= 0 {
			depth = 6
		}
		root, scope, err := resolveSnapshotRoot(app, args.Window, args.AppRoot)
		if err != nil {
			return nil, nil, err
		}
		payload, err := buildAXTreePayload(args.App, root, scope, depth, args.IncludeScreenshot, args.IncludeOCR)
		if err != nil {
			return nil, nil, err
		}
		data, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return nil, nil, fmt.Errorf("marshal: %w", err)
		}
		return textResult(string(data)), nil, nil
	})
}

// ── ax_find ───────────────────────────────────────────────────────────────────

type axFindInput struct {
	App      string `json:"app"`
	Window   string `json:"window,omitempty"`
	Role     string `json:"role,omitempty"`
	Title    string `json:"title,omitempty"`
	Contains string `json:"contains,omitempty"`
	Exact    bool   `json:"exact,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

func registerAXFind(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "ax_find",
		Description: "Find AX elements in an app by role, exact text, or substring across title, description, value, and identifier. " +
			"Set exact=true with contains to require an exact match instead of substring. " +
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
			Exact:    args.Exact,
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
  screenshot [--out PATH] [--padding N]
                                   capture current scope and save a PNG artifact
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
	Exact    bool   `json:"exact,omitempty"`
	XOffset  *int   `json:"x_offset,omitempty"`
	YOffset  *int   `json:"y_offset,omitempty"`
}

func registerAXClick(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "ax_click",
		Description: "Click an element in an app found by normalized text lookup across title, description, value, and identifier. " +
			"Set window to scope the search to a specific window title substring. " +
			"Set exact=true to require an exact text match (prevents 'Settings' from matching 'Services Settings'). " +
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
			Exact:    args.Exact,
			Limit:    500,
		})
		if len(result.matches) == 0 {
			if ui.IsScreenRecordingTrusted() && args.Contains != "" && args.XOffset == nil && args.YOffset == nil {
				capture, err := captureOCRScope(args.App, args.Window, "", "")
				if err == nil {
					defer capture.Close()
					selection, err := selectOCRMatch(capture.result, args.Contains, nil)
					if err == nil {
						summary, resolutionNote, err := performOCRClick(capture, selection.match)
						if err == nil {
							var buf bytes.Buffer
							buf.WriteString(summary)
							buf.WriteString("\nAX search found no matching element; used OCR fallback")
							fmt.Fprintf(&buf, "\n%s", selection.resolved)
							if resolutionNote != "" {
								fmt.Fprintf(&buf, "\n%s", resolutionNote)
							}
							return textResult(buf.String()), nil, nil
						}
					}
				}
			}
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
		if strings.TrimSpace(args.App) == "" {
			return nil, nil, fmt.Errorf("ax_type: app is required")
		}
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
			endTypingCursor := beginTypingCursor(el)
			defer endTypingCursor()
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
		if err := focusElement(el); err != nil {
			return nil, nil, fmt.Errorf("focus %s: %w", formatMatch(result.matches[0]), err)
		}
		endTypingCursor := beginTypingCursor(el)
		defer endTypingCursor()
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
		Description: "List windows for an application by name or bundle ID. Returns window titles, bounds, and display index (0=main).",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axListWindowsInput) (*mcp.CallToolResult, any, error) {
		app, err := spinAndOpen(args.App)
		if err != nil {
			return nil, nil, err
		}
		defer app.Close()

		type winInfo struct {
			Title     string `json:"title"`
			X         int    `json:"x"`
			Y         int    `json:"y"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			Display   int    `json:"display"`
			OffScreen bool   `json:"off_screen,omitempty"`
			OffSpace  bool   `json:"off_space,omitempty"`
		}

		displays := activeDisplayBounds()
		var result []winInfo

		// Try AX window list first.
		wins := app.WindowList()
		for _, w := range wins {
			x, y := w.Position()
			width, height := w.Size()
			result = append(result, winInfo{
				Title:   w.Title(),
				X:       x,
				Y:       y,
				Width:   width,
				Height:  height,
				Display: displayIndexForPoint(displays, float64(x), float64(y)),
			})
		}

		// If AX returned nothing, fall back to CGWindowList (finds windows on other Spaces/displays).
		if len(result) == 0 {
			cgWindows, cgErr := listAppWindows(args.App)
			if cgErr != nil {
				return nil, nil, fmt.Errorf("no windows found for %q via AX or CGWindowList — the app may have windows on another Space or display", args.App)
			}
			for _, cw := range cgWindows {
				wi := winInfo{
					Title:     cw.Title,
					X:         int(cw.X),
					Y:         int(cw.Y),
					Width:     int(cw.Width),
					Height:    int(cw.Height),
					Display:   displayIndexForPoint(displays, cw.X, cw.Y),
					OffScreen: cw.OffScreen,
				}
				if off, err := spacedetect.IsOffSpace(cw.WindowID); err != nil {
					if !errors.Is(err, spacedetect.ErrSkyLightUnavailable) {
						slog.Debug("spacedetect: lookup failed", "windowID", cw.WindowID, "err", err)
					}
				} else if off {
					wi.OffSpace = true
				}
				result = append(result, wi)
			}
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
	Exact        bool   `json:"exact,omitempty"`
	Annotated    bool   `json:"annotated,omitempty"`
	Padding      int    `json:"padding,omitempty"`
	ArtifactPath string `json:"artifact_path,omitempty"`
	FullScreen   bool   `json:"full_screen,omitempty"`
}

func registerAXScreenshot(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "ax_screenshot",
		Description: `Capture a screenshot of an app window or specific element.

Prefer targeting a specific element with contains/role for smaller, faster, more token-efficient results. Set window to a title substring when an app has multiple windows and you want a specific one. Full app window captures are larger and should only be used when you need to see the complete window layout.

Set padding to expand the capture rect around a targeted element by N pixels on each side (useful for seeing surrounding context). Set annotated=true to burn OCR match boxes into the returned PNG. Set full_screen=true to capture the entire display (requires explicit opt-in due to large image size). Set artifact_path to save the PNG to a durable file.`,
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axScreenshotInput) (*mcp.CallToolResult, any, error) {
		var (
			png   []byte
			scope *ocrRedactionScope
		)
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
				Exact:    args.Exact,
				Limit:    500,
			})
			if len(result.matches) == 0 {
				return nil, nil, fmt.Errorf("%s", noMatchMessage(result))
			}
			el := result.matches[0].snapshot.element
			if args.Padding > 0 {
				png, err = captureElementWithPadding(el, args.Padding)
			} else {
				png, err = captureElementOrWindow(args.App, true, el)
			}
			if err != nil {
				return nil, nil, err
			}
			if args.Padding <= 0 {
				scope = &ocrRedactionScope{root: el}
			}
		} else {
			// Full window screenshot: try SCK/CGWindowList first (no AX IPC needed,
			// avoids hanging on apps with unresponsive accessibility implementations).
			var err error
			png, err = captureWindowByTitle(args.App, args.Window)
			if err != nil {
				return nil, nil, err
			}
			if args.Annotated {
				if app, err := spinAndOpen(args.App); err == nil {
					defer app.Close()
					if win, _, err := resolveWindow(app, args.Window); err == nil {
						scope = &ocrRedactionScope{root: win}
					}
				}
			}
		}

		content := []mcp.Content{}
		if args.Annotated {
			w, h, err := pngDimensions(png)
			if err != nil {
				return nil, nil, err
			}
			results, err := recognizeText(png, w, h)
			if err != nil {
				return nil, nil, err
			}
			render, err := drawAnnotatedOCR(png, w, h, results, scope, "")
			if err != nil {
				return nil, nil, err
			}
			png = render.PNG
			payload, err := formatOverlayResultsJSON(render.Results)
			if err != nil {
				return nil, nil, err
			}
			content = append(content, &mcp.TextContent{Text: overlaySummary(render) + "\n" + payload})
		}
		if args.ArtifactPath != "" {
			if err := writePNGArtifact(args.ArtifactPath, png); err != nil {
				return nil, nil, err
			}
			content = append(content,
				&mcp.TextContent{Text: fmt.Sprintf("saved screenshot to %s", filepath.Clean(args.ArtifactPath))},
			)
		}
		content = append(content, &mcp.ImageContent{Data: png, MIMEType: "image/png"})
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
		return nil, fmt.Errorf("no windows found for %q — the app may have windows on another Space or display: %w", appName, err)
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
		return nil, fmt.Errorf("no windows found for %q — the app may have windows on another Space or display: %w", appName, err)
	}
	win, ok := matchWindowInfo(windows, title)
	if !ok {
		return nil, fmt.Errorf("no window matching %q found for %q", title, appName)
	}
	if win.OffScreen {
		slog.Warn("capturing off-screen window (may be on another Space)", "app", appName, "title", win.Title, "windowID", win.WindowID)
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
			return nil, fmt.Errorf("screenshot failed: Screen Recording is still not granted — enable axmcp.app in System Settings > Privacy & Security and retry")
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
	frame := el.Frame()
	ghostcursor.FlashCaptureRect(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: frame.Origin.X, Y: frame.Origin.Y},
		Size:   corefoundation.CGSize{Width: frame.Size.Width, Height: frame.Size.Height},
	})
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
	App       string `json:"app"`
	Window    string `json:"window,omitempty"`
	Contains  string `json:"contains,omitempty"`
	Role      string `json:"role,omitempty"`
	Find      string `json:"find,omitempty"`
	JSON      bool   `json:"json,omitempty"`
	Layout    bool   `json:"layout,omitempty"`
	Annotated bool   `json:"annotated,omitempty"`
	Cols      int    `json:"cols,omitempty"`
	Rows      int    `json:"rows,omitempty"`
}

func registerAXOCR(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "ax_ocr",
		Description: "Run Apple Vision OCR on an app window or scoped AX element. " +
			"Returns recognized text with coordinates in the local space of the captured window or element, plus absolute screen coordinates derived from that target frame. " +
			"Set window to target a specific window title substring. Use contains/role to OCR a specific AX element such as a sidebar outline. " +
			"Set annotated=true to return a PNG with OCR boxes and index labels burned in. " +
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
			content := []mcp.Content{&mcp.TextContent{Text: renderOCRLayout(results, capture.imgW, capture.imgH, cols, rows)}}
			if args.Annotated && len(capture.png) > 0 {
				render, err := drawAnnotatedOCR(capture.png, capture.imgW, capture.imgH, results, capture.scope, args.Find)
				if err != nil {
					return nil, nil, err
				}
				content = append(content, &mcp.ImageContent{Data: render.PNG, MIMEType: "image/png"})
			}
			return &mcp.CallToolResult{Content: content}, nil, nil
		}
		var (
			content []mcp.Content
			render  overlayRender
		)
		if args.Annotated && len(capture.png) > 0 {
			var err error
			render, err = drawAnnotatedOCR(capture.png, capture.imgW, capture.imgH, results, capture.scope, args.Find)
			if err != nil {
				return nil, nil, err
			}
		}
		if args.JSON {
			if args.Annotated && len(capture.png) > 0 {
				out, err := formatOverlayResultsJSON(render.Results)
				if err != nil {
					return nil, nil, err
				}
				content = append(content, &mcp.TextContent{Text: out})
			} else {
				out, err := formatOCRResultsJSON(results, capture.target)
				if err != nil {
					return nil, nil, err
				}
				content = append(content, &mcp.TextContent{Text: out})
			}
		} else {
			text := formatOCRResults(results, capture.target)
			if args.Annotated && len(capture.png) > 0 {
				text = overlaySummary(render) + "\n" + text
			}
			content = append(content, &mcp.TextContent{Text: text})
		}
		if args.Annotated && len(capture.png) > 0 {
			content = append(content, &mcp.ImageContent{Data: render.PNG, MIMEType: "image/png"})
		}
		return &mcp.CallToolResult{Content: content}, nil, nil
	})
}
