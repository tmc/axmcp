package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/apple/x/axuiautomation"
)

const (
	defaultActionScreenshotSettle = 300 * time.Millisecond
	defaultActionScreenshotPad    = 12
)

type axActionScreenshotInput struct {
	App         string `json:"app"`
	Action      string `json:"action"`
	Window      string `json:"window,omitempty"`
	Contains    string `json:"contains,omitempty"`
	Role        string `json:"role,omitempty"`
	ArtifactDir string `json:"artifact_dir,omitempty"`
	XOffset     *int   `json:"x_offset,omitempty"`
	YOffset     *int   `json:"y_offset,omitempty"`
	SettleMS    int    `json:"settle_ms,omitempty"`
	Padding     int    `json:"padding,omitempty"`
}

type actionTarget struct {
	snapshot       elementSnapshot
	selectionNote  string
	resolutionNote string
}

func registerAXActionScreenshot(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "ax_action_screenshot",
		Description: `Capture a window screenshot, perform a hover or click, then capture the same window again.

Returns a cropped PNG containing only the changed pixels from the post-action image. Use window to target a specific window title substring. Use contains/role to target an element within that window; if omitted, the action targets the window itself. Set artifact_dir to save before.png, after.png, and diff.png under a durable run directory.`,
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axActionScreenshotInput) (*mcp.CallToolResult, any, error) {
		action, err := parsePointerAction(args.Action)
		if err != nil {
			return nil, nil, err
		}

		app, err := spinAndOpen(args.App)
		if err != nil {
			return nil, nil, err
		}
		defer app.Close()

		win, winDesc, err := resolveWindow(app, args.Window)
		if err != nil {
			return nil, nil, err
		}

		captureWin, err := resolveCaptureWindow(args.App, args.Window != "", win.Title(), args.Window)
		if err != nil {
			return nil, nil, err
		}

		beforePNG, err := captureWindow(captureWin)
		if err != nil {
			return nil, nil, fmt.Errorf("capture before screenshot: %w", err)
		}

		target, err := resolveActionTarget(win, args.Contains, args.Role)
		if err != nil {
			return nil, nil, err
		}
		verb, err := performPointerAction(target.snapshot, action, args.XOffset, args.YOffset)
		if err != nil {
			return nil, nil, fmt.Errorf("%s %s: %w", action, formatSnapshot(target.snapshot), err)
		}

		time.Sleep(actionSettleDuration(args.SettleMS))

		afterPNG, err := captureWindow(captureWin)
		if err != nil {
			return nil, nil, fmt.Errorf("capture after screenshot: %w", err)
		}

		diffPNG, bounds, changed, err := diffChangedRegionPNG(beforePNG, afterPNG, actionPadding(args.Padding))
		if err != nil {
			return nil, nil, err
		}
		var artifactDir string
		var artifactFiles []string
		if args.ArtifactDir != "" {
			artifactDir, artifactFiles, err = writeActionScreenshotArtifacts(args.ArtifactDir, args.App, args.Window, args.Action, beforePNG, afterPNG, diffPNG)
			if err != nil {
				return nil, nil, err
			}
		}

		var buf bytes.Buffer
		fmt.Fprintf(&buf, "%s %s in %s", verb, formatSnapshot(target.snapshot), winDesc)
		if target.selectionNote != "" {
			fmt.Fprintf(&buf, "\n%s", target.selectionNote)
		}
		if target.resolutionNote != "" {
			fmt.Fprintf(&buf, "\n%s", target.resolutionNote)
		}
		if changed == 0 || len(diffPNG) == 0 {
			fmt.Fprintf(&buf, "\nno visible change detected; try increasing settle_ms if the UI animates slowly")
			if artifactDir != "" {
				fmt.Fprintf(&buf, "\nsaved artifacts in %s", artifactDir)
				fmt.Fprintf(&buf, "\n  %s", strings.Join(fileBaseNames(artifactFiles), "\n  "))
			}
			return textResult(buf.String()), nil, nil
		}
		fmt.Fprintf(&buf, "\nchanged region bounds=(%d,%d %dx%d), pixels=%d", bounds.Min.X, bounds.Min.Y, bounds.Dx(), bounds.Dy(), changed)
		if artifactDir != "" {
			fmt.Fprintf(&buf, "\nsaved artifacts in %s", artifactDir)
			fmt.Fprintf(&buf, "\n  %s", strings.Join(fileBaseNames(artifactFiles), "\n  "))
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: buf.String()},
				&mcp.ImageContent{Data: diffPNG, MIMEType: "image/png"},
			},
		}, nil, nil
	})
}

func parsePointerAction(action string) (string, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "click", "hover":
		return action, nil
	default:
		return "", fmt.Errorf("unknown action %q; use click or hover", action)
	}
}

func resolveActionTarget(root *axuiautomation.Element, contains, role string) (actionTarget, error) {
	if root == nil {
		return actionTarget{}, fmt.Errorf("no window found")
	}
	if contains == "" && role == "" {
		return actionTarget{snapshot: snapshotElement(root, 0, 0)}, nil
	}

	result := findElements(root, searchOptions{
		Role:     role,
		Contains: contains,
		Limit:    500,
	})
	if len(result.matches) == 0 {
		return actionTarget{}, fmt.Errorf("%s", noMatchMessage(result))
	}

	match := result.matches[0]
	resolution := resolveClickTarget(match, 500)
	if resolution.target.element == nil {
		return actionTarget{}, fmt.Errorf("target disappeared: %s", formatMatch(match))
	}
	return actionTarget{
		snapshot:       resolution.target,
		selectionNote:  selectionReason(result),
		resolutionNote: resolution.reason,
	}, nil
}

func performPointerAction(target elementSnapshot, action string, xOffset, yOffset *int) (string, error) {
	if target.element == nil {
		return "", fmt.Errorf("target disappeared")
	}
	switch action {
	case "click":
		if (xOffset == nil) != (yOffset == nil) {
			return "", fmt.Errorf("click offsets require both x_offset and y_offset")
		}
		if xOffset != nil {
			if err := clickLocalPoint(target.element, *xOffset, *yOffset); err != nil {
				return "", err
			}
			return fmt.Sprintf("clicked at offset %d,%d", *xOffset, *yOffset), nil
		}
		if x, y, ok := preferredClickPoint(target); ok {
			if err := clickLocalPoint(target.element, x, y); err == nil {
				return fmt.Sprintf("clicked at hit point %d,%d", x, y), nil
			}
		}
		return performDefaultClick(target)
	case "hover":
		if xOffset != nil || yOffset != nil {
			return "", fmt.Errorf("hover does not support x_offset or y_offset")
		}
		return performDefaultHover(target)
	default:
		return "", fmt.Errorf("unknown action %q; use click or hover", action)
	}
}

func actionSettleDuration(ms int) time.Duration {
	if ms <= 0 {
		return defaultActionScreenshotSettle
	}
	return time.Duration(ms) * time.Millisecond
}

func actionPadding(padding int) int {
	if padding <= 0 {
		return defaultActionScreenshotPad
	}
	return padding
}

func resolveCaptureWindow(appName string, strict bool, titles ...string) (windowInfo, error) {
	windows, err := listAppWindows(appName)
	if err != nil {
		return windowInfo{}, fmt.Errorf("no windows found for %q: %w", appName, err)
	}
	if win, ok := matchWindowInfo(windows, titles...); ok {
		return win, nil
	}
	if strict {
		for _, title := range titles {
			if title != "" {
				return windowInfo{}, fmt.Errorf("no on-screen window matching %q found for %q", title, appName)
			}
		}
	}
	return windows[0], nil
}

func writeActionScreenshotArtifacts(baseDir, appName, windowTitle, action string, beforePNG, afterPNG, diffPNG []byte) (string, []string, error) {
	return writeActionScreenshotArtifactsAt(baseDir, appName, windowTitle, action, time.Now().UTC(), beforePNG, afterPNG, diffPNG)
}

func writeActionScreenshotArtifactsAt(baseDir, appName, windowTitle, action string, now time.Time, beforePNG, afterPNG, diffPNG []byte) (string, []string, error) {
	if baseDir == "" {
		return "", nil, fmt.Errorf("artifact_dir is empty")
	}
	if len(beforePNG) == 0 {
		return "", nil, fmt.Errorf("before screenshot is empty")
	}
	if len(afterPNG) == 0 {
		return "", nil, fmt.Errorf("after screenshot is empty")
	}

	runDir, beforePath, afterPath, diffPath := artifactPaths(baseDir, appName, windowTitle, action, now)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("create artifact directory: %w", err)
	}

	written := []string{beforePath, afterPath}
	if err := os.WriteFile(beforePath, beforePNG, 0o644); err != nil {
		return "", nil, fmt.Errorf("write artifact %s: %w", beforePath, err)
	}
	if err := os.WriteFile(afterPath, afterPNG, 0o644); err != nil {
		return "", nil, fmt.Errorf("write artifact %s: %w", afterPath, err)
	}
	if len(diffPNG) > 0 {
		if err := os.WriteFile(diffPath, diffPNG, 0o644); err != nil {
			return "", nil, fmt.Errorf("write artifact %s: %w", diffPath, err)
		}
		written = append(written, diffPath)
	}
	return runDir, written, nil
}
