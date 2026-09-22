package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/ghostcursor"
)

func registerAXWindowTools(s *mcp.Server) {
	registerAXWindowClick(s)
	registerAXWindowHover(s)
	registerAXWindowDrag(s)
	registerAXWindowDragPath(s)
	registerAXWindowMove(s)
	registerAXWindowRaise(s)
	registerAXWindowAction(s)
}

// ── ax_window_click ───────────────────────────────────────────────────────────

type axWindowClickInput struct {
	App    string `json:"app"`
	Window string `json:"window,omitempty"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
}

func registerAXWindowClick(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "ax_window_click",
		Description: `Click a point in an application window using local coordinates from the window's top-left corner. ` +
			`Useful with ax_ocr results, which report coordinates in the target's local space.`,
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axWindowClickInput) (*mcp.CallToolResult, any, error) {
		app, err := spinAndOpen(args.App)
		if err != nil {
			return nil, nil, err
		}
		defer app.Close()

		win, desc, err := resolveWindow(app, args.Window)
		if err != nil {
			return nil, nil, err
		}
		if err := clickLocalPoint(win, args.X, args.Y); err != nil {
			return nil, nil, fmt.Errorf("click %s at local %d,%d: %w", desc, args.X, args.Y, err)
		}
		return textResult(windowPointResult("clicked", desc, args.X, args.Y)), nil, nil
	})
}

// ── ax_window_drag ────────────────────────────────────────────────────────────

type axWindowDragInput struct {
	App    string `json:"app"`
	Window string `json:"window,omitempty"`
	StartX int    `json:"start_x"`
	StartY int    `json:"start_y"`
	EndX   int    `json:"end_x"`
	EndY   int    `json:"end_y"`
	Button string `json:"button,omitempty"`
}

func registerAXWindowDrag(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "ax_window_drag",
		Description: `Drag between two points in an application window using local coordinates from the window's top-left corner. ` +
			`Useful with OCR or screenshot coordinates when you need drag-and-drop or scrubber interactions. ` +
			`Button can be "left" or "right" and defaults to "left".`,
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axWindowDragInput) (*mcp.CallToolResult, any, error) {
		app, err := spinAndOpen(args.App)
		if err != nil {
			return nil, nil, err
		}
		defer app.Close()

		win, desc, err := resolveWindow(app, args.Window)
		if err != nil {
			return nil, nil, err
		}
		button, err := parseMouseButton(args.Button)
		if err != nil {
			return nil, nil, err
		}
		if err := dragLocalPoint(win, args.StartX, args.StartY, args.EndX, args.EndY, button); err != nil {
			return nil, nil, fmt.Errorf("drag %s from local %d,%d to %d,%d: %w", desc, args.StartX, args.StartY, args.EndX, args.EndY, err)
		}
		return textResult(fmt.Sprintf("dragged %s from local %d,%d to %d,%d", desc, args.StartX, args.StartY, args.EndX, args.EndY)), nil, nil
	})
}

// ── ax_window_drag_path ───────────────────────────────────────────────────────

type axWindowDragPathPoint struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type axWindowDragPathInput struct {
	App        string                  `json:"app"`
	Window     string                  `json:"window,omitempty"`
	Points     []axWindowDragPathPoint `json:"points"`
	Button     string                  `json:"button,omitempty"`
	Easing     string                  `json:"easing,omitempty"`
	DurationMS int                     `json:"duration_ms,omitempty"`
}

func registerAXWindowDragPath(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "ax_window_drag_path",
		Description: `Drag through a series of points in an application window as one continuous stroke, ` +
			`using local coordinates from the window's top-left corner. ` +
			`The button goes down at the first point, stays down through every later point, and comes up at the last, ` +
			`so curved strokes on a canvas render as a single stroke instead of several disconnected drags. ` +
			`Button can be "left" or "right" and defaults to "left". ` +
			`Easing can be "linear" for straight segments or "curved" for a bezier arc between points, and defaults to "linear". ` +
			`Duration_ms is the time budget per segment and defaults to 250.`,
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axWindowDragPathInput) (*mcp.CallToolResult, any, error) {
		if len(args.Points) < 2 {
			return nil, nil, fmt.Errorf("drag path needs at least 2 points, got %d", len(args.Points))
		}
		button, err := parseMouseButton(args.Button)
		if err != nil {
			return nil, nil, err
		}
		curve, err := parseDragEasing(args.Easing)
		if err != nil {
			return nil, nil, err
		}
		app, err := spinAndOpen(args.App)
		if err != nil {
			return nil, nil, err
		}
		defer app.Close()

		win, desc, err := resolveWindow(app, args.Window)
		if err != nil {
			return nil, nil, err
		}
		points := make([]dragPoint, len(args.Points))
		for i, p := range args.Points {
			points[i] = dragPoint{X: p.X, Y: p.Y}
		}
		if err := dragLocalPath(win, points, button, curve, time.Duration(args.DurationMS)*time.Millisecond); err != nil {
			return nil, nil, fmt.Errorf("drag %s through %s: %w", desc, formatDragPath(points), err)
		}
		return textResult(fmt.Sprintf("dragged %s through %d local points %s", desc, len(points), formatDragPath(points))), nil, nil
	})
}

// parseDragEasing maps the easing option to a cursor curve style. The empty
// value means linear, so a caller that asks for a straight segment gets one.
func parseDragEasing(easing string) (ghostcursor.CurveStyle, error) {
	switch strings.ToLower(strings.TrimSpace(easing)) {
	case "", "linear":
		return ghostcursor.CurveLinear, nil
	case "curved", "bezier":
		return ghostcursor.CurveBezier, nil
	case "ease", "ease-in-out":
		return ghostcursor.CurveEaseInOut, nil
	default:
		return 0, fmt.Errorf("unsupported easing %q: want linear, curved, or ease", easing)
	}
}

func formatDragPath(points []dragPoint) string {
	var b strings.Builder
	for i, p := range points {
		if i > 0 {
			b.WriteString(" -> ")
		}
		fmt.Fprintf(&b, "%d,%d", p.X, p.Y)
	}
	return b.String()
}

// ── ax_window_hover ───────────────────────────────────────────────────────────

type axWindowHoverInput struct {
	App    string `json:"app"`
	Window string `json:"window,omitempty"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
}

func registerAXWindowHover(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "ax_window_hover",
		Description: `Move the pointer to a point in an application window using local coordinates from the window's top-left corner. ` +
			`Useful with ax_ocr results, which report coordinates in the target's local space.`,
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axWindowHoverInput) (*mcp.CallToolResult, any, error) {
		app, err := spinAndOpen(args.App)
		if err != nil {
			return nil, nil, err
		}
		defer app.Close()

		win, desc, err := resolveWindow(app, args.Window)
		if err != nil {
			return nil, nil, err
		}
		if err := hoverLocalPoint(win, args.X, args.Y); err != nil {
			return nil, nil, fmt.Errorf("hover %s at local %d,%d: %w", desc, args.X, args.Y, err)
		}
		return textResult(windowPointResult("hovered", desc, args.X, args.Y)), nil, nil
	})
}

// ── ax_window_move ────────────────────────────────────────────────────────────

type axWindowMoveInput struct {
	App    string  `json:"app"`
	Window string  `json:"window,omitempty"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
}

func registerAXWindowMove(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "ax_window_move",
		Description: `Move an application window to a new position (x, y in screen coordinates). ` +
			`Optionally specify a window title substring to target a specific window.`,
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axWindowMoveInput) (*mcp.CallToolResult, any, error) {
		app, err := spinAndOpen(args.App)
		if err != nil {
			return nil, nil, err
		}
		defer app.Close()

		win, desc, err := resolveWindow(app, args.Window)
		if err != nil {
			return nil, nil, err
		}

		if err := win.SetPosition(args.X, args.Y); err != nil {
			return nil, nil, fmt.Errorf("move %s: %w", desc, err)
		}
		return textResult(fmt.Sprintf("moved %s to (%.0f, %.0f)", desc, args.X, args.Y)), nil, nil
	})
}

// ── ax_window_raise ───────────────────────────────────────────────────────────

type axWindowRaiseInput struct {
	App    string `json:"app"`
	Window string `json:"window,omitempty"`
}

func registerAXWindowRaise(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "ax_window_raise",
		Description: "Raise an application window to the front. Optionally specify a window title substring.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axWindowRaiseInput) (*mcp.CallToolResult, any, error) {
		app, err := spinAndOpen(args.App)
		if err != nil {
			return nil, nil, err
		}
		defer app.Close()

		win, desc, err := resolveWindow(app, args.Window)
		if err != nil {
			return nil, nil, err
		}

		if err := win.Raise(); err != nil {
			return nil, nil, fmt.Errorf("raise %s: %w", desc, err)
		}
		return textResult(fmt.Sprintf("raised %s", desc)), nil, nil
	})
}

// ── ax_window_action ──────────────────────────────────────────────────────────

type axWindowActionInput struct {
	App    string `json:"app"`
	Window string `json:"window,omitempty"`
	Action string `json:"action"`
}

func registerAXWindowAction(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "ax_window_action",
		Description: `Perform a window-level action. Supported actions: ` +
			`"close" (click close button), "minimize" (click minimize button), ` +
			`"zoom" (click zoom/maximize button). ` +
			`Optionally specify a window title substring.`,
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axWindowActionInput) (*mcp.CallToolResult, any, error) {
		app, err := spinAndOpen(args.App)
		if err != nil {
			return nil, nil, err
		}
		defer app.Close()

		win, desc, err := resolveWindow(app, args.Window)
		if err != nil {
			return nil, nil, err
		}

		buttonRole, err := windowActionToButton(args.Action)
		if err != nil {
			return nil, nil, err
		}

		btn := findButtonBySubrole(win, buttonRole)
		if btn == nil {
			return nil, nil, fmt.Errorf("%s button not found on %s", args.Action, desc)
		}
		if _, err := performDefaultClick(snapshotElement(btn, 0, 0)); err != nil {
			return nil, nil, fmt.Errorf("%s %s: %w", args.Action, desc, err)
		}
		return textResult(fmt.Sprintf("%s %s", args.Action, desc)), nil, nil
	})
}

// ── helpers ───────────────────────────────────────────────────────────────────

// noAXWindowsMessage explains an empty AX window list. ax_list_windows falls
// back to CGWindowList, so it can report windows these AX-only tools cannot
// reach; saying nothing here reads as a contradiction between the two tools.
const noAXWindowsMessage = "the app exposes no accessibility windows " +
	"(its AX server may be unresponsive, or the windows are on another Space or display). " +
	"ax_list_windows also reads CGWindowList and may still list them; " +
	"ax_ocr / ax_ocr_click work from that same CGWindowList capture."

func resolveWindow(app *axuiautomation.Application, titleSubstr string) (*axuiautomation.Element, string, error) {
	wins := app.WindowList()
	if len(wins) == 0 {
		return nil, "", fmt.Errorf("%s", noAXWindowsMessage)
	}
	if titleSubstr == "" {
		title := wins[0].Title()
		if title == "" {
			title = "untitled"
		}
		return wins[0], fmt.Sprintf("window %q", title), nil
	}
	lower := strings.ToLower(titleSubstr)
	for _, w := range wins {
		if strings.Contains(strings.ToLower(w.Title()), lower) {
			return w, fmt.Sprintf("window %q", w.Title()), nil
		}
	}
	return nil, "", fmt.Errorf("no window matching %q found", titleSubstr)
}

func findButtonBySubrole(win *axuiautomation.Element, subrole string) *axuiautomation.Element {
	for _, btn := range win.Descendants().ByRole("AXButton").WithLimit(20).AllElements() {
		if btn.Subrole() == subrole {
			return btn
		}
	}
	return nil
}

func windowActionToButton(action string) (string, error) {
	switch strings.ToLower(action) {
	case "close":
		return "AXCloseButton", nil
	case "minimize":
		return "AXMinimizeButton", nil
	case "zoom", "maximize":
		return "AXZoomButton", nil
	default:
		return "", fmt.Errorf("unknown window action %q; use close, minimize, or zoom", action)
	}
}

func windowPointResult(verb, desc string, x, y int) string {
	return fmt.Sprintf("%s %s at local %d,%d", verb, desc, x, y)
}
