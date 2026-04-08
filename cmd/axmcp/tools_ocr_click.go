package main

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/ui"
)

type ocrCapture struct {
	app    *axuiautomation.Application
	target *axuiautomation.Element
	desc   string
	imgW   int
	imgH   int
	result []ocrResult
}

func (c *ocrCapture) Close() {
	if c != nil && c.app != nil {
		c.app.Close()
		c.app = nil
	}
}

func captureOCRScope(appName, window, contains, role string) (*ocrCapture, error) {
	app, err := spinAndOpen(appName)
	if err != nil {
		return nil, err
	}
	capture := &ocrCapture{app: app}

	if contains != "" || role != "" {
		root, _, err := resolveSearchRoot(app, window)
		if err != nil {
			capture.Close()
			return nil, err
		}
		result := findElements(root, searchOptions{
			Role:     role,
			Contains: contains,
			Limit:    500,
		})
		if len(result.matches) == 0 {
			capture.Close()
			msg := noMatchMessage(result)
			if hint := ocrNoMatchHint(appName, window, primaryQuery(result.options)); hint != "" {
				msg += hint
			}
			return nil, fmt.Errorf("%s", msg)
		}
		target := result.matches[0].snapshot.element
		if target == nil {
			capture.Close()
			return nil, fmt.Errorf("ocr target disappeared: %s", formatMatch(result.matches[0]))
		}
		results, err := ocrElement(target)
		if err != nil {
			capture.Close()
			return nil, err
		}
		w, h := localSize(target)
		capture.target = target
		capture.desc = formatMatch(result.matches[0])
		capture.imgW = w
		capture.imgH = h
		capture.result = results
		return capture, nil
	}

	win, desc, err := resolveWindow(app, window)
	if err != nil {
		// AX window resolution failed. Fall back to CGWindowList-based OCR,
		// which works even when apps have unresponsive accessibility.
		results, w, h, ocrErr := ocrWindow(appName, window)
		if ocrErr != nil {
			capture.Close()
			return nil, fmt.Errorf("%v (AX fallback: %v)", ocrErr, err)
		}
		capture.target = app.Root()
		capture.desc = fmt.Sprintf("window %q (via CGWindowList)", appName)
		capture.imgW = w
		capture.imgH = h
		capture.result = results
		return capture, nil
	}
	results, w, h, err := ocrElementWithSize(win)
	if err != nil {
		title := win.Title()
		if title == "" {
			title = window
		}
		results, w, h, err = ocrWindow(appName, title)
		if err != nil {
			capture.Close()
			return nil, err
		}
	}
	capture.target = win
	capture.desc = desc
	capture.imgW = w
	capture.imgH = h
	capture.result = results
	return capture, nil
}

func ocrElementWithSize(el *axuiautomation.Element) ([]ocrResult, int, int, error) {
	if el == nil {
		return nil, 0, 0, fmt.Errorf("target disappeared")
	}
	w, h := localSize(el)
	if w <= 0 || h <= 0 {
		return nil, 0, 0, fmt.Errorf("element has zero-size frame")
	}
	results, err := ocrElement(el)
	if err != nil {
		return nil, 0, 0, err
	}
	return results, w, h, nil
}

type ocrAXCandidate struct {
	snapshot  elementSnapshot
	matchKind textMatchKind
	contains  bool
	distance2 int
	area      int
}

func pointInRecord(record elementRecord, x, y int) bool {
	if record.w <= 0 || record.h <= 0 {
		return false
	}
	return x >= record.x && x < record.x+record.w && y >= record.y && y < record.y+record.h
}

func pointToRecordDistance2(record elementRecord, x, y int) int {
	if record.w <= 0 || record.h <= 0 {
		return 1 << 30
	}
	dx := 0
	switch {
	case x < record.x:
		dx = record.x - x
	case x >= record.x+record.w:
		dx = x - (record.x + record.w - 1)
	}
	dy := 0
	switch {
	case y < record.y:
		dy = record.y - y
	case y >= record.y+record.h:
		dy = y - (record.y + record.h - 1)
	}
	return dx*dx + dy*dy
}

func candidateArea(record elementRecord) int {
	if record.w <= 0 || record.h <= 0 {
		return 1 << 30
	}
	return record.w * record.h
}

func resolveOCRActionableTarget(root elementSnapshot, descendants []elementSnapshot, localX, localY int, query string) (elementSnapshot, string, bool) {
	candidates := append([]elementSnapshot(nil), descendants...)
	if root.record.actionable && root.record.enabled && root.record.visible {
		candidates = append(candidates, root)
	}
	if len(candidates) == 0 {
		return elementSnapshot{}, "", false
	}

	absX := root.record.x + localX
	absY := root.record.y + localY

	ranked := make([]ocrAXCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		record := candidate.record
		distance2 := pointToRecordDistance2(record, absX, absY)
		contains := pointInRecord(record, absX, absY)
		if !contains && distance2 > 24*24 {
			continue
		}
		_, matchKind, ok := matchTextField(record, query, false)
		if !ok {
			matchKind = matchNone
		}
		ranked = append(ranked, ocrAXCandidate{
			snapshot:  candidate,
			matchKind: matchKind,
			contains:  contains,
			distance2: distance2,
			area:      candidateArea(record),
		})
	}
	if len(ranked) == 0 {
		return elementSnapshot{}, "", false
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		a := ranked[i]
		b := ranked[j]
		switch {
		case a.contains != b.contains:
			return a.contains
		case a.matchKind != b.matchKind:
			return a.matchKind > b.matchKind
		case a.snapshot.record.depth != b.snapshot.record.depth:
			return a.snapshot.record.depth > b.snapshot.record.depth
		case a.area != b.area:
			return a.area < b.area
		case a.distance2 != b.distance2:
			return a.distance2 < b.distance2
		default:
			return a.snapshot.record.index < b.snapshot.record.index
		}
	})

	best := ranked[0]
	var reason string
	switch {
	case best.contains && best.matchKind == matchExact:
		reason = fmt.Sprintf("using AX target %s containing the OCR match center with exact text match", formatSnapshot(best.snapshot))
	case best.contains && best.matchKind == matchContains:
		reason = fmt.Sprintf("using AX target %s containing the OCR match center with text overlap", formatSnapshot(best.snapshot))
	case best.contains:
		reason = fmt.Sprintf("using AX target %s containing the OCR match center", formatSnapshot(best.snapshot))
	default:
		reason = fmt.Sprintf("using nearby AX target %s (%d px from the OCR match center)", formatSnapshot(best.snapshot), roundedDistance(best.distance2))
	}
	return best.snapshot, reason, true
}

func nearestOCRActionableTarget(capture *ocrCapture, match ocrResult) (elementSnapshot, string, bool) {
	if capture == nil || capture.target == nil {
		return elementSnapshot{}, "", false
	}
	root := snapshotElement(capture.target, 0, 0)
	descendants := actionableDescendants(root, 500)
	localX, localY := match.Center()
	return resolveOCRActionableTarget(root, descendants, localX, localY, displayString(match.Text))
}

func roundedDistance(distance2 int) int {
	if distance2 <= 0 {
		return 0
	}
	d := 0
	for d*d < distance2 {
		d++
	}
	return d
}

func performOCRClick(capture *ocrCapture, match ocrResult) (summary, resolutionNote string, err error) {
	if capture == nil || capture.target == nil {
		return "", "", fmt.Errorf("OCR scope target disappeared")
	}
	x, y := match.Center()
	if target, note, ok := nearestOCRActionableTarget(capture, match); ok && target.element != nil {
		clickSummary, err := performDefaultClick(target)
		if err == nil {
			if strings.Contains(clickSummary, "via AXPress") {
				return fmt.Sprintf("clicked OCR match %q in %s via AXPress on %s", match.Text, capture.desc, formatSnapshot(target)), note, nil
			}
			return fmt.Sprintf("clicked OCR match %q in %s via local click on %s", match.Text, capture.desc, formatSnapshot(target)), note, nil
		}
		resolutionNote = fmt.Sprintf("%s\nclick target failed: %v; falling back to OCR point", note, err)
	}
	if err := clickLocalPoint(capture.target, x, y); err != nil {
		return "", resolutionNote, fmt.Errorf("click OCR match %q in %s: %w", match.Text, capture.desc, err)
	}
	summary = fmt.Sprintf("clicked OCR match %q in %s at %d,%d via local click", match.Text, capture.desc, x, y)
	if resolutionNote == "" {
		resolutionNote = "no actionable AX target found at the OCR match; using local click"
	}
	return summary, resolutionNote, nil
}

func performOCRHover(capture *ocrCapture, match ocrResult) (summary, resolutionNote string, err error) {
	if capture == nil || capture.target == nil {
		return "", "", fmt.Errorf("OCR scope target disappeared")
	}
	x, y := match.Center()
	if err := hoverLocalPoint(capture.target, x, y); err != nil {
		return "", "", fmt.Errorf("hover OCR match %q in %s: %w", match.Text, capture.desc, err)
	}
	summary = fmt.Sprintf("hovered OCR match %q in %s at %d,%d via local hover", match.Text, capture.desc, x, y)
	return summary, "hover uses the OCR match center", nil
}

type selectedOCRMatch struct {
	match    ocrResult
	index    int
	total    int
	resolved string
}

func selectOCRMatch(results []ocrResult, query string, matchIndex *int) (selectedOCRMatch, error) {
	matches := findOCRText(results, query)
	if len(matches) == 0 {
		return selectedOCRMatch{}, fmt.Errorf("no visible text matching %q found", query)
	}

	if matchIndex != nil {
		if *matchIndex < 1 || *matchIndex > len(matches) {
			return selectedOCRMatch{}, fmt.Errorf("match %d out of range; found %d visible matches for %q", *matchIndex, len(matches), query)
		}
		i := *matchIndex - 1
		return selectedOCRMatch{
			match:    matches[i],
			index:    *matchIndex,
			total:    len(matches),
			resolved: fmt.Sprintf("selected OCR match %d of %d", *matchIndex, len(matches)),
		}, nil
	}

	queryNorm := normalizeMatchString(query)
	for i, match := range matches {
		if normalizeMatchString(match.Text) == queryNorm {
			return selectedOCRMatch{
				match:    match,
				index:    i + 1,
				total:    len(matches),
				resolved: fmt.Sprintf("selected exact OCR match %d of %d", i+1, len(matches)),
			}, nil
		}
	}

	return selectedOCRMatch{
		match:    matches[0],
		index:    1,
		total:    len(matches),
		resolved: fmt.Sprintf("selected OCR match 1 of %d", len(matches)),
	}, nil
}

func ocrToolCall(appName, window, query string) string {
	var args []string
	args = append(args, fmt.Sprintf("app=%q", appName))
	if window != "" {
		args = append(args, fmt.Sprintf("window=%q", window))
	}
	if query != "" {
		args = append(args, fmt.Sprintf("find=%q", query))
	}
	return "ax_ocr(" + strings.Join(args, ", ") + ")"
}

func ocrClickToolCall(appName, window, query string) string {
	var args []string
	args = append(args, fmt.Sprintf("app=%q", appName))
	if window != "" {
		args = append(args, fmt.Sprintf("window=%q", window))
	}
	args = append(args, fmt.Sprintf("find=%q", query))
	return "ax_ocr_click(" + strings.Join(args, ", ") + ")"
}

func ocrNoMatchHint(appName, window, query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}
	var buf strings.Builder
	buf.WriteString("\nVisible text may exist only in OCR.")
	if !ui.IsScreenRecordingTrusted() {
		fmt.Fprintf(&buf, " Try %s or %s after granting Screen Recording.", ocrToolCall(appName, window, query), ocrClickToolCall(appName, window, query))
		return buf.String()
	}

	results, _, _, err := ocrWindow(appName, window)
	if err != nil {
		fmt.Fprintf(&buf, " Try %s or %s.", ocrToolCall(appName, window, query), ocrClickToolCall(appName, window, query))
		return buf.String()
	}
	matches := findOCRText(results, query)
	if len(matches) == 0 {
		fmt.Fprintf(&buf, " Try %s to inspect visible text.", ocrToolCall(appName, window, query))
		return buf.String()
	}

	buf.WriteString("\nVisible OCR matches:\n")
	for _, match := range matches[:min(len(matches), 3)] {
		cx, cy := match.Center()
		fmt.Fprintf(&buf, "  - %q center=(%d,%d) bounds=(%d,%d %dx%d)\n", match.Text, cx, cy, match.X, match.Y, match.W, match.H)
	}
	fmt.Fprintf(&buf, "Use %s to click the best visible match.", ocrClickToolCall(appName, window, query))
	return strings.TrimRight(buf.String(), "\n")
}

type axOCRClickInput struct {
	App      string `json:"app"`
	Find     string `json:"find"`
	Match    *int   `json:"match,omitempty"`
	Window   string `json:"window,omitempty"`
	Contains string `json:"contains,omitempty"`
	Role     string `json:"role,omitempty"`
}

type axOCRHoverInput struct {
	App      string `json:"app"`
	Find     string `json:"find"`
	Match    *int   `json:"match,omitempty"`
	Window   string `json:"window,omitempty"`
	Contains string `json:"contains,omitempty"`
	Role     string `json:"role,omitempty"`
}

func registerAXOCRClick(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "ax_ocr_click",
		Description: `Find visible text with OCR inside a window or scoped AX element, then click its center.

Use window to target a specific window title substring. Use contains/role to OCR a specific AX element such as a sidebar outline, then click text inside that element using local coordinates. Optional match selects the 1-based OCR hit number after filtering; otherwise exact visible text is preferred.`,
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axOCRClickInput) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(args.Find) == "" {
			return nil, nil, fmt.Errorf("find is required")
		}

		capture, err := captureOCRScope(args.App, args.Window, args.Contains, args.Role)
		if err != nil {
			return nil, nil, err
		}
		defer capture.Close()

		selection, err := selectOCRMatch(capture.result, args.Find, args.Match)
		if err != nil {
			return nil, nil, fmt.Errorf("%s in %s", err, capture.desc)
		}
		summary, resolutionNote, err := performOCRClick(capture, selection.match)
		if err != nil {
			return nil, nil, err
		}

		var buf bytes.Buffer
		buf.WriteString(summary)
		fmt.Fprintf(&buf, "\n%s", selection.resolved)
		if resolutionNote != "" {
			fmt.Fprintf(&buf, "\n%s", resolutionNote)
		}
		x, y := selection.match.Center()
		fmt.Fprintf(&buf, "\ncenter=(%d,%d) bounds=(%d,%d %dx%d)", x, y, selection.match.X, selection.match.Y, selection.match.W, selection.match.H)
		return textResult(buf.String()), nil, nil
	})
}

func registerAXOCRHover(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "ax_ocr_hover",
		Description: `Find visible text with OCR inside a window or scoped AX element, then move the pointer to its center.

Use window to target a specific window title substring. Use contains/role to OCR a specific AX element such as a sidebar outline, then hover text inside that element using local coordinates. Optional match selects the 1-based OCR hit number after filtering; otherwise exact visible text is preferred.`,
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axOCRHoverInput) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(args.Find) == "" {
			return nil, nil, fmt.Errorf("find is required")
		}

		capture, err := captureOCRScope(args.App, args.Window, args.Contains, args.Role)
		if err != nil {
			return nil, nil, err
		}
		defer capture.Close()

		selection, err := selectOCRMatch(capture.result, args.Find, args.Match)
		if err != nil {
			return nil, nil, fmt.Errorf("%s in %s", err, capture.desc)
		}
		summary, resolutionNote, err := performOCRHover(capture, selection.match)
		if err != nil {
			return nil, nil, err
		}

		var buf bytes.Buffer
		buf.WriteString(summary)
		fmt.Fprintf(&buf, "\n%s", selection.resolved)
		if resolutionNote != "" {
			fmt.Fprintf(&buf, "\n%s", resolutionNote)
		}
		x, y := selection.match.Center()
		fmt.Fprintf(&buf, "\ncenter=(%d,%d) bounds=(%d,%d %dx%d)", x, y, selection.match.X, selection.match.Y, selection.match.W, selection.match.H)
		return textResult(buf.String()), nil, nil
	})
}
