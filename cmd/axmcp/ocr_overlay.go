package main

import (
	"encoding/json"
	"fmt"
	"image"
	"sort"
	"strings"

	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/computeruse/overlay"
)

const (
	agentOCRBoxCap     = 20
	humanOCRBoxCap     = 8
	maxOCRResultOutput = 50
	minOCRConfidence   = 0.60
)

type overlayResult struct {
	Index      int     `json:"index,omitempty"`
	Role       string  `json:"role"`
	Text       string  `json:"text,omitempty"`
	Confidence float32 `json:"confidence"`
	X          int     `json:"x"`
	Y          int     `json:"y"`
	W          int     `json:"w"`
	H          int     `json:"h"`
	CenterX    int     `json:"center_x"`
	CenterY    int     `json:"center_y"`
}

type overlayRender struct {
	PNG           []byte
	Results       []overlayResult
	OverlayCount  int
	RawCount      int
	Truncated     int
	RedactedCount int
	SelectedCount int
}

type ocrRedactionScope struct {
	root *axuiautomation.Element
}

func drawAnnotatedOCR(png []byte, _ int, _ int, results []ocrResult, scope *ocrRedactionScope, query string) (overlayRender, error) {
	specs := buildOverlaySpecs(results, scope, query)
	annotated, err := overlay.DrawOCRMatches(png, specs.matches, overlay.Options{MaxBoxes: agentOCRBoxCap})
	if err != nil {
		return overlayRender{}, err
	}
	return overlayRender{
		PNG:           annotated,
		Results:       specs.results,
		OverlayCount:  len(specs.matches),
		RawCount:      len(results),
		Truncated:     max(0, len(specs.results)-len(specs.matches)),
		RedactedCount: specs.redacted,
		SelectedCount: specs.selected,
	}, nil
}

type overlaySpec struct {
	matches  []overlay.Match
	results  []overlayResult
	redacted int
	selected int
}

func buildOverlaySpecs(results []ocrResult, scope *ocrRedactionScope, query string) overlaySpec {
	selected := make(map[string]int)
	if strings.TrimSpace(query) != "" {
		for i, match := range findOCRText(results, query) {
			selected[ocrMatchKey(match)] = i + 1
		}
	}

	root := scopeRoot(scope)
	raw := make([]overlayResult, 0, len(results))
	matches := make([]overlay.Match, 0, min(len(results), agentOCRBoxCap))
	redactedCount := 0
	selectedCount := len(selected)
	winnerAssigned := false
	for _, result := range prioritizeOCRResults(results, selected) {
		role := overlay.RoleRunnerUp
		index := 0
		text := result.Text
		if secure, ok := nearestSecureMatch(root, result); ok && secure {
			role = overlay.RoleRedacted
			text = ""
			redactedCount++
		} else if n, ok := selected[ocrMatchKey(result)]; ok {
			index = n
			if !winnerAssigned {
				role = overlay.RoleWinner
				winnerAssigned = true
			}
		} else if result.Confidence < minOCRConfidence {
			role = overlay.RoleFiltered
		} else if !winnerAssigned && len(selected) == 0 {
			role = overlay.RoleWinner
			index = 1
			winnerAssigned = true
		}
		centerX, centerY := result.Center()
		raw = append(raw, overlayResult{
			Index:      index,
			Role:       overlayRoleName(role),
			Text:       text,
			Confidence: result.Confidence,
			X:          result.X,
			Y:          result.Y,
			W:          result.W,
			H:          result.H,
			CenterX:    centerX,
			CenterY:    centerY,
		})
		matches = append(matches, overlay.Match{
			Index:      index,
			Rect:       image.Rect(result.X, result.Y, result.X+result.W, result.Y+result.H),
			Text:       text,
			Confidence: float64(result.Confidence),
			Role:       role,
		})
	}
	return overlaySpec{
		matches:  matches,
		results:  raw,
		redacted: redactedCount,
		selected: selectedCount,
	}
}

func prioritizeOCRResults(results []ocrResult, selected map[string]int) []ocrResult {
	out := append([]ocrResult(nil), results...)
	sort.SliceStable(out, func(i, j int) bool {
		ki := selected[ocrMatchKey(out[i])]
		kj := selected[ocrMatchKey(out[j])]
		switch {
		case ki > 0 && kj == 0:
			return true
		case ki == 0 && kj > 0:
			return false
		case ki > 0 && kj > 0 && ki != kj:
			return ki < kj
		case out[i].Confidence != out[j].Confidence:
			return out[i].Confidence > out[j].Confidence
		case out[i].Y != out[j].Y:
			return out[i].Y < out[j].Y
		default:
			return out[i].X < out[j].X
		}
	})
	return out
}

func formatOverlayResultsJSON(results []overlayResult) (string, error) {
	trimmed := results
	if len(trimmed) > maxOCRResultOutput {
		trimmed = trimmed[:maxOCRResultOutput]
	}
	data, err := json.MarshalIndent(trimmed, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func overlaySummary(render overlayRender) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("annotated %d OCR match", render.OverlayCount))
	if render.OverlayCount != 1 {
		parts[0] += "es"
	}
	if render.RedactedCount > 0 {
		parts = append(parts, fmt.Sprintf("%d redacted", render.RedactedCount))
	}
	if render.RawCount > maxOCRResultOutput {
		parts = append(parts, fmt.Sprintf("showing top %d of %d", maxOCRResultOutput, render.RawCount))
	}
	return strings.Join(parts, "; ")
}

func ocrMatchKey(match ocrResult) string {
	return fmt.Sprintf("%s|%d|%d|%d|%d", normalizeMatchString(match.Text), match.X, match.Y, match.W, match.H)
}

func overlayRoleName(role overlay.MatchRole) string {
	switch role {
	case overlay.RoleWinner:
		return "winner"
	case overlay.RoleRunnerUp:
		return "runner_up"
	case overlay.RoleRedacted:
		return "redacted"
	default:
		return "filtered"
	}
}

func scopeRoot(scope *ocrRedactionScope) *axuiautomation.Element {
	if scope == nil {
		return nil
	}
	return scope.root
}

func nearestSecureMatch(root *axuiautomation.Element, match ocrResult) (bool, bool) {
	if root == nil {
		return false, false
	}
	rootSnap := snapshotElement(root, 0, 0)
	localX, localY := match.Center()
	absX := rootSnap.record.x + localX
	absY := rootSnap.record.y + localY
	bestDistance := int(^uint(0) >> 1)
	bestArea := int(^uint(0) >> 1)
	bestRole := ""
	for _, candidate := range collectSnapshots(root, 500) {
		record := candidate.record
		if !record.visible || record.w <= 0 || record.h <= 0 {
			continue
		}
		distance := pointToRecordDistance2(record, absX, absY)
		if pointInRecord(record, absX, absY) {
			distance = 0
		}
		area := candidateArea(record)
		if distance < bestDistance || (distance == bestDistance && area < bestArea) {
			bestDistance = distance
			bestArea = area
			bestRole = record.role
		}
	}
	if bestRole == "" {
		return false, false
	}
	return bestRole == "AXSecureTextField", true
}
