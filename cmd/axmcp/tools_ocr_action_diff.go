//go:build darwin

package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type axOCRActionDiffInput struct {
	App            string `json:"app"`
	Action         string `json:"action"`
	Window         string `json:"window,omitempty"`
	Contains       string `json:"contains,omitempty"`
	Role           string `json:"role,omitempty"`
	Find           string `json:"find,omitempty"`
	Match          *int   `json:"match,omitempty"`
	ActionContains string `json:"action_contains,omitempty"`
	ActionRole     string `json:"action_role,omitempty"`
	XOffset        *int   `json:"x_offset,omitempty"`
	YOffset        *int   `json:"y_offset,omitempty"`
	SettleMS       int    `json:"settle_ms,omitempty"`
}

func registerAXOCRActionDiff(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "ax_ocr_action_diff",
		Description: `Capture OCR text before an interaction, perform an action, then OCR the same scope again and return a git --word-diff style inline diff.

Actions:
  click      click an AX target
  hover      hover an AX target
  ocr_click  click the OCR match named by find inside the OCR scope
  ocr_hover  hover the OCR match named by find inside the OCR scope

Use window to target a specific window title substring. Use contains/role to scope OCR to a specific AX element such as a sidebar outline. For click/hover, action_contains/action_role can target a descendant inside that OCR scope. Optional match selects the 1-based OCR hit number after filtering; otherwise exact visible text is preferred.`,
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axOCRActionDiffInput) (*mcp.CallToolResult, any, error) {
		action := strings.ToLower(strings.TrimSpace(args.Action))
		if action == "" {
			action = "click"
		}
		if action != "click" && action != "hover" && action != "ocr_click" && action != "ocr_hover" {
			return nil, nil, fmt.Errorf("unknown action %q; use click, hover, ocr_click, or ocr_hover", args.Action)
		}

		beforeCapture, err := captureOCRScope(args.App, args.Window, args.Contains, args.Role)
		if err != nil {
			return nil, nil, err
		}
		defer beforeCapture.Close()

		actionSummary, selectionNote, resolutionNote, err := performOCRDiffAction(beforeCapture, args, action)
		if err != nil {
			return nil, nil, err
		}

		time.Sleep(actionSettleDuration(args.SettleMS))

		afterCapture, err := captureOCRScope(args.App, args.Window, args.Contains, args.Role)
		if err != nil {
			return nil, nil, err
		}
		defer afterCapture.Close()

		beforeText := snapshotTextFromOCRResults(beforeCapture.result)
		afterText := snapshotTextFromOCRResults(afterCapture.result)
		diff := renderOCRWordDiff(beforeText, afterText)

		var buf bytes.Buffer
		buf.WriteString(actionSummary)
		if selectionNote != "" {
			fmt.Fprintf(&buf, "\n%s", selectionNote)
		}
		if resolutionNote != "" {
			fmt.Fprintf(&buf, "\n%s", resolutionNote)
		}
		if diff == "" {
			fmt.Fprintf(&buf, "\nno OCR text change")
		} else {
			fmt.Fprintf(&buf, "\n%s", diff)
		}
		if beforeText != "" {
			fmt.Fprintf(&buf, "\n\nbefore:\n%s", beforeText)
		}
		if afterText != "" {
			fmt.Fprintf(&buf, "\n\nafter:\n%s", afterText)
		}
		return textResult(buf.String()), nil, nil
	})
}

func performOCRDiffAction(capture *ocrCapture, args axOCRActionDiffInput, action string) (summary, selectionNote, resolutionNote string, err error) {
	if capture == nil || capture.target == nil {
		return "", "", "", fmt.Errorf("OCR scope target disappeared")
	}
	switch action {
	case "ocr_click", "ocr_hover":
		find := strings.TrimSpace(args.Find)
		if find == "" {
			return "", "", "", fmt.Errorf("find is required for %s", action)
		}
		selection, err := selectOCRMatch(capture.result, find, args.Match)
		if err != nil {
			return "", "", "", fmt.Errorf("%s in %s", err, capture.desc)
		}
		if action == "ocr_click" {
			summary, resolutionNote, err := performOCRClick(capture, selection.match)
			if err != nil {
				return "", "", "", err
			}
			return summary, selection.resolved, resolutionNote, nil
		}
		summary, resolutionNote, err := performOCRHover(capture, selection.match)
		if err != nil {
			return "", "", "", err
		}
		return summary, selection.resolved, resolutionNote, nil
	case "click", "hover":
		target, err := resolveActionTarget(capture.target, args.ActionContains, args.ActionRole)
		if err != nil {
			return "", "", "", err
		}
		verb, err := performPointerAction(target.snapshot, action, args.XOffset, args.YOffset)
		if err != nil {
			return "", "", "", fmt.Errorf("%s %s: %w", action, formatSnapshot(target.snapshot), err)
		}
		return fmt.Sprintf("%s %s in OCR scope %s", verb, formatSnapshot(target.snapshot), capture.desc), target.selectionNote, target.resolutionNote, nil
	default:
		return "", "", "", fmt.Errorf("unknown action %q; use click, hover, ocr_click, or ocr_hover", action)
	}
}
