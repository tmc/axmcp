package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/apple/x/axuiautomation"
)

func registerAXInteractionTools(s *mcp.Server) {
	registerAXScroll(s)
	registerAXActionScreenshot(s)
	registerAXOCRActionDiff(s)
	registerAXOCRClick(s)
	registerAXOCRHover(s)
	registerAXDoubleClick(s)
	registerAXSetValue(s)
	registerAXKeyStroke(s)
	registerAXPerformAction(s)
}

// ── ax_scroll ─────────────────────────────────────────────────────────────────

type axScrollInput struct {
	App       string `json:"app"`
	Contains  string `json:"contains,omitempty"`
	Role      string `json:"role,omitempty"`
	Direction string `json:"direction"`
	Amount    int    `json:"amount,omitempty"`
}

func registerAXScroll(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "ax_scroll",
		Description: `Scroll an element or window in an app. Direction: "up", "down", "left", "right". ` +
			`Amount is in lines (default 3). If contains/role are omitted, scrolls the first window.`,
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axScrollInput) (*mcp.CallToolResult, any, error) {
		app, err := spinAndOpen(args.App)
		if err != nil {
			return nil, nil, err
		}
		defer app.Close()

		dir, err := parseDirection(args.Direction)
		if err != nil {
			return nil, nil, err
		}
		amount := args.Amount
		if amount <= 0 {
			amount = 3
		}

		el, desc, err := resolveTarget(app, args.Contains, args.Role)
		if err != nil {
			return nil, nil, err
		}

		if _, err := performDefaultScroll(snapshotElement(el, 0, 0), dir, amount); err != nil {
			return nil, nil, fmt.Errorf("scroll %s: %w", desc, err)
		}
		return textResult(fmt.Sprintf("scrolled %s %s by %d lines", desc, args.Direction, amount)), nil, nil
	})
}

// ── ax_double_click ───────────────────────────────────────────────────────────

type axDoubleClickInput struct {
	App      string `json:"app"`
	Contains string `json:"contains"`
	Role     string `json:"role,omitempty"`
}

func registerAXDoubleClick(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "ax_double_click",
		Description: "Double-click an element in an app found by normalized text lookup.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axDoubleClickInput) (*mcp.CallToolResult, any, error) {
		app, err := spinAndOpen(args.App)
		if err != nil {
			return nil, nil, err
		}
		defer app.Close()

		result := findElements(app.Root(), searchOptions{
			Role:     args.Role,
			Contains: args.Contains,
			Limit:    100,
		})
		if len(result.matches) == 0 {
			return nil, nil, fmt.Errorf("%s", noMatchMessage(result))
		}

		match := result.matches[0]
		resolution := resolveClickTarget(match, 50)
		target := resolution.target.element
		if target == nil {
			return nil, nil, fmt.Errorf("double-click target disappeared: %s", formatMatch(match))
		}

		doubleClickSummary, err := performDefaultDoubleClick(resolution.target)
		if err != nil {
			return nil, nil, fmt.Errorf("double-click %s: %w", formatSnapshot(resolution.target), err)
		}
		var buf bytes.Buffer
		buf.WriteString(doubleClickSummary)
		if note := selectionReason(result); note != "" {
			fmt.Fprintf(&buf, "\n%s", note)
		}
		return textResult(buf.String()), nil, nil
	})
}

// ── ax_set_value ──────────────────────────────────────────────────────────────

type axSetValueInput struct {
	App      string `json:"app"`
	Contains string `json:"contains,omitempty"`
	Role     string `json:"role,omitempty"`
	Value    string `json:"value"`
}

func registerAXSetValue(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "ax_set_value",
		Description: "Set the AXValue of an element (text fields, sliders, etc) found by normalized text lookup. " +
			"When contains is omitted but role is specified, matches by role alone (useful for empty text fields).",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axSetValueInput) (*mcp.CallToolResult, any, error) {
		if args.Contains == "" && args.Role == "" {
			return nil, nil, fmt.Errorf("at least one of contains or role is required")
		}
		app, err := spinAndOpen(args.App)
		if err != nil {
			return nil, nil, err
		}
		defer app.Close()

		result := findElements(app.Root(), searchOptions{
			Role:     args.Role,
			Contains: args.Contains,
			Limit:    100,
		})
		if len(result.matches) == 0 {
			return nil, nil, fmt.Errorf("%s", noMatchMessage(result))
		}
		el := result.matches[0].snapshot.element
		if el == nil {
			return nil, nil, fmt.Errorf("set_value target disappeared: %s", formatMatch(result.matches[0]))
		}
		if err := el.SetValue(args.Value); err != nil {
			return nil, nil, fmt.Errorf("set_value %s: %w", formatMatch(result.matches[0]), err)
		}
		var buf bytes.Buffer
		fmt.Fprintf(&buf, "set value on %s", formatMatch(result.matches[0]))
		if note := selectionReason(result); note != "" {
			fmt.Fprintf(&buf, "\n%s", note)
		}
		return textResult(buf.String()), nil, nil
	})
}

// ── ax_keystroke ──────────────────────────────────────────────────────────────

type axKeyStrokeInput struct {
	Key     string `json:"key"`
	Shift   bool   `json:"shift,omitempty"`
	Control bool   `json:"control,omitempty"`
	Option  bool   `json:"option,omitempty"`
	Command bool   `json:"command,omitempty"`
}

// knownKeys maps friendly key names to macOS virtual key codes.
var knownKeys = map[string]uint16{
	"return":    0x24,
	"enter":     0x24,
	"tab":       0x30,
	"escape":    0x35,
	"esc":       0x35,
	"delete":    0x33,
	"backspace": 0x33,
	"up":        0x7E,
	"down":      0x7D,
	"left":      0x7B,
	"right":     0x7C,
	"space":     0x31,
	"home":      0x73,
	"end":       0x77,
	"pageup":    0x74,
	"pagedown":  0x79,
	"f1":        0x7A, "f2": 0x78, "f3": 0x63, "f4": 0x76,
	"f5": 0x60, "f6": 0x61, "f7": 0x62, "f8": 0x64,
	"f9": 0x65, "f10": 0x6D, "f11": 0x67, "f12": 0x6F,
	// Letters
	"a": 0x00, "b": 0x0B, "c": 0x08, "d": 0x02, "e": 0x0E,
	"f": 0x03, "g": 0x05, "h": 0x04, "i": 0x22, "j": 0x26,
	"k": 0x28, "l": 0x25, "m": 0x2E, "n": 0x2D, "o": 0x1F,
	"p": 0x23, "q": 0x0C, "r": 0x0F, "s": 0x01, "t": 0x11,
	"u": 0x20, "v": 0x09, "w": 0x0D, "x": 0x07, "y": 0x10,
	"z": 0x06,
	// Numbers
	"0": 0x1D, "1": 0x12, "2": 0x13, "3": 0x14, "4": 0x15,
	"5": 0x17, "6": 0x16, "7": 0x1A, "8": 0x1C, "9": 0x19,
}

func registerAXKeyStroke(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "ax_keystroke",
		Description: `Send a keyboard shortcut or key press. Key can be a letter, number, or special key ` +
			`(return, tab, escape, delete, up, down, left, right, space, home, end, pageup, pagedown, f1-f12). ` +
			`Combine with modifier flags for shortcuts like Cmd+S (key="s", command=true).`,
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axKeyStrokeInput) (*mcp.CallToolResult, any, error) {
		keyName := strings.ToLower(args.Key)
		code, ok := knownKeys[keyName]
		if !ok {
			return nil, nil, fmt.Errorf("unknown key %q; supported: letters, numbers, return, tab, escape, delete, arrows, space, home, end, pageup, pagedown, f1-f12", args.Key)
		}
		if err := axuiautomation.SendKeyCombo(code, args.Shift, args.Control, args.Option, args.Command); err != nil {
			return nil, nil, fmt.Errorf("keystroke: %w", err)
		}
		var desc strings.Builder
		if args.Command {
			desc.WriteString("Cmd+")
		}
		if args.Control {
			desc.WriteString("Ctrl+")
		}
		if args.Option {
			desc.WriteString("Opt+")
		}
		if args.Shift {
			desc.WriteString("Shift+")
		}
		desc.WriteString(args.Key)
		return textResult(fmt.Sprintf("sent keystroke: %s", desc.String())), nil, nil
	})
}

// ── ax_perform_action ─────────────────────────────────────────────────────────

type axPerformActionInput struct {
	App      string `json:"app"`
	Contains string `json:"contains"`
	Role     string `json:"role,omitempty"`
	Action   string `json:"action"`
}

func registerAXPerformAction(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "ax_perform_action",
		Description: `Perform a named AX action on an element (e.g. AXPress, AXConfirm, AXCancel, AXShowMenu, AXIncrement, AXDecrement, AXRaise).`,
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axPerformActionInput) (*mcp.CallToolResult, any, error) {
		app, err := spinAndOpen(args.App)
		if err != nil {
			return nil, nil, err
		}
		defer app.Close()

		result := findElements(app.Root(), searchOptions{
			Role:     args.Role,
			Contains: args.Contains,
			Limit:    100,
		})
		if len(result.matches) == 0 {
			return nil, nil, fmt.Errorf("%s", noMatchMessage(result))
		}
		el := result.matches[0].snapshot.element
		if el == nil {
			return nil, nil, fmt.Errorf("action target disappeared: %s", formatMatch(result.matches[0]))
		}
		if err := el.PerformAction(args.Action); err != nil {
			return nil, nil, fmt.Errorf("perform %s on %s: %w", args.Action, formatMatch(result.matches[0]), err)
		}
		// Spin the run loop so the target app processes the action.
		axuiautomation.SpinRunLoop(200 * time.Millisecond)
		var buf bytes.Buffer
		fmt.Fprintf(&buf, "performed %s on %s", args.Action, formatMatch(result.matches[0]))
		if note := selectionReason(result); note != "" {
			fmt.Fprintf(&buf, "\n%s", note)
		}
		return textResult(buf.String()), nil, nil
	})
}

// ── helpers ───────────────────────────────────────────────────────────────────

func parseDirection(s string) (axuiautomation.ScrollDirection, error) {
	switch strings.ToLower(s) {
	case "up":
		return axuiautomation.ScrollUp, nil
	case "down":
		return axuiautomation.ScrollDown, nil
	case "left":
		return axuiautomation.ScrollLeft, nil
	case "right":
		return axuiautomation.ScrollRight, nil
	default:
		return 0, fmt.Errorf("invalid direction %q; use up, down, left, or right", s)
	}
}

func resolveTarget(app *axuiautomation.Application, contains, role string) (*axuiautomation.Element, string, error) {
	if contains != "" || role != "" {
		result := findElements(app.Root(), searchOptions{
			Role:     role,
			Contains: contains,
			Limit:    100,
		})
		if len(result.matches) == 0 {
			return nil, "", fmt.Errorf("%s", noMatchMessage(result))
		}
		el := result.matches[0].snapshot.element
		if el == nil {
			return nil, "", fmt.Errorf("target disappeared: %s", formatMatch(result.matches[0]))
		}
		return el, formatMatch(result.matches[0]), nil
	}
	wins := app.WindowList()
	if len(wins) == 0 {
		return nil, "", fmt.Errorf("no windows found")
	}
	return wins[0], "first window", nil
}
