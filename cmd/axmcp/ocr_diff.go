//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func normalizeOCRSnapshot(snapshot, format string) (string, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	switch format {
	case "", "auto":
		trimmed := strings.TrimSpace(snapshot)
		if strings.HasPrefix(trimmed, "[") {
			results, err := parseOCRSnapshotJSON(snapshot)
			if err == nil {
				return snapshotTextFromOCRResults(results), nil
			}
		}
		return strings.TrimSpace(snapshot), nil
	case "json":
		results, err := parseOCRSnapshotJSON(snapshot)
		if err != nil {
			return "", err
		}
		return snapshotTextFromOCRResults(results), nil
	case "text":
		return strings.TrimSpace(snapshot), nil
	default:
		return "", fmt.Errorf("unknown OCR snapshot format %q; use auto, json, or text", format)
	}
}

func parseOCRSnapshotJSON(snapshot string) ([]ocrResult, error) {
	var results []ocrResult
	if err := json.Unmarshal([]byte(snapshot), &results); err != nil {
		return nil, fmt.Errorf("parse OCR snapshot JSON: %w", err)
	}
	return results, nil
}

func snapshotTextFromOCRResults(results []ocrResult) string {
	results = primaryOCRResults(results)
	if len(results) == 0 {
		return ""
	}
	sort.SliceStable(results, func(i, j int) bool {
		switch {
		case results[i].Y != results[j].Y:
			return results[i].Y < results[j].Y
		case results[i].X != results[j].X:
			return results[i].X < results[j].X
		default:
			return results[i].Text < results[j].Text
		}
	})

	type line struct {
		y     int
		h     int
		items []ocrResult
	}
	lines := []line{{y: results[0].Y, h: results[0].H, items: []ocrResult{results[0]}}}
	for _, r := range results[1:] {
		cur := &lines[len(lines)-1]
		threshold := max(6, max(cur.h, r.H)/2)
		if absInt(r.Y-cur.y) <= threshold {
			cur.items = append(cur.items, r)
			cur.y = (cur.y*(len(cur.items)-1) + r.Y) / len(cur.items)
			cur.h = max(cur.h, r.H)
			continue
		}
		lines = append(lines, line{y: r.Y, h: r.H, items: []ocrResult{r}})
	}

	var out []string
	for _, line := range lines {
		sort.SliceStable(line.items, func(i, j int) bool {
			if line.items[i].X != line.items[j].X {
				return line.items[i].X < line.items[j].X
			}
			return line.items[i].Text < line.items[j].Text
		})
		var parts []string
		for _, item := range line.items {
			text := strings.TrimSpace(item.Text)
			if text == "" {
				continue
			}
			parts = append(parts, text)
		}
		if len(parts) == 0 {
			continue
		}
		out = append(out, strings.Join(parts, " "))
	}
	return strings.Join(out, "\n")
}

func primaryOCRResults(results []ocrResult) []ocrResult {
	if len(results) == 0 {
		return nil
	}
	type key struct {
		x int
		y int
		w int
		h int
	}
	byBox := make(map[key]ocrResult, len(results))
	order := make([]key, 0, len(results))
	for _, r := range results {
		k := key{x: r.X, y: r.Y, w: r.W, h: r.H}
		cur, ok := byBox[k]
		if !ok {
			byBox[k] = r
			order = append(order, k)
			continue
		}
		if r.Confidence > cur.Confidence {
			byBox[k] = r
			continue
		}
		if r.Confidence == cur.Confidence && len(strings.TrimSpace(r.Text)) > len(strings.TrimSpace(cur.Text)) {
			byBox[k] = r
		}
	}
	out := make([]ocrResult, 0, len(order))
	for _, k := range order {
		out = append(out, byBox[k])
	}
	return out
}

func renderOCRWordDiff(before, after string) string {
	if before == after {
		return before
	}
	a := wordDiffTokens(before)
	b := wordDiffTokens(after)
	if len(a) == 0 && len(b) == 0 {
		return ""
	}

	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}

	var buf strings.Builder
	var removed []string
	var added []string
	flush := func() {
		if len(removed) > 0 {
			fmt.Fprintf(&buf, "[-%s-]", strings.Join(removed, ""))
			removed = removed[:0]
		}
		if len(added) > 0 {
			fmt.Fprintf(&buf, "{+%s+}", strings.Join(added, ""))
			added = added[:0]
		}
	}

	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			flush()
			buf.WriteString(a[i])
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			removed = append(removed, a[i])
			i++
		default:
			added = append(added, b[j])
			j++
		}
	}
	removed = append(removed, a[i:]...)
	added = append(added, b[j:]...)
	flush()
	return buf.String()
}

func wordDiffTokens(s string) []string {
	if s == "" {
		return nil
	}
	var tokens []string
	var cur []rune
	class := -1
	flush := func() {
		if len(cur) == 0 {
			return
		}
		tokens = append(tokens, string(cur))
		cur = cur[:0]
	}
	for _, r := range s {
		next := tokenClass(r)
		if class != -1 && next != class {
			flush()
		}
		cur = append(cur, r)
		class = next
	}
	flush()
	return tokens
}

func tokenClass(r rune) int {
	switch {
	case unicode.IsSpace(r):
		return 0
	case unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_':
		return 1
	default:
		return 2
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

type axOCRDiffInput struct {
	Before string `json:"before"`
	After  string `json:"after"`
	Format string `json:"format,omitempty"`
}

func registerAXOCRDiff(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "ax_ocr_diff",
		Description: "Compare two OCR snapshots and return a git --word-diff style inline diff. " +
			"Pass before/after as raw text, or pass the JSON output from ax_ocr(json=true). " +
			"Use format='json' to force OCR JSON normalization, format='text' to diff raw text, or omit it for auto-detection.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args axOCRDiffInput) (*mcp.CallToolResult, any, error) {
		before, err := normalizeOCRSnapshot(args.Before, args.Format)
		if err != nil {
			return nil, nil, err
		}
		after, err := normalizeOCRSnapshot(args.After, args.Format)
		if err != nil {
			return nil, nil, err
		}
		if before == after {
			if before == "" {
				return textResult("no OCR text in either snapshot"), nil, nil
			}
			return textResult("no OCR text change\n\n" + before), nil, nil
		}

		diff := renderOCRWordDiff(before, after)
		var sections []string
		sections = append(sections, diff)
		if before != "" {
			sections = append(sections, "before:\n"+before)
		}
		if after != "" {
			sections = append(sections, "after:\n"+after)
		}
		return textResult(strings.Join(sections, "\n\n")), nil, nil
	})
}
