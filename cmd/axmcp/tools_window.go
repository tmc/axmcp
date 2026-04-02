package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/apple/x/axuiautomation"
)

func registerAXWindowTools(s *mcp.Server) {
	registerAXWindowClick(s)
	registerAXWindowHover(s)
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

func resolveWindow(app *axuiautomation.Application, titleSubstr string) (*axuiautomation.Element, string, error) {
	wins := app.WindowList()
	if len(wins) == 0 {
		return nil, "", fmt.Errorf("no windows found")
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
