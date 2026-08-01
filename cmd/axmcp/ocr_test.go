package main

import (
	"strings"
	"testing"
)

func TestExpandOCRResultsAtOrigin(t *testing.T) {
	got := expandOCRResultsAtOrigin([]ocrResult{
		{Text: "Extra High", Confidence: 0.99, X: 12, Y: 34, W: 40, H: 10},
	}, 200, 300)
	if len(got) != 1 {
		t.Fatalf("len(expandOCRResultsAtOrigin(...)) = %d, want 1", len(got))
	}
	want := ocrOutputResult{
		Text:          "Extra High",
		Confidence:    0.99,
		X:             12,
		Y:             34,
		W:             40,
		H:             10,
		CenterX:       32,
		CenterY:       39,
		ScreenX:       212,
		ScreenY:       334,
		ScreenW:       40,
		ScreenH:       10,
		ScreenCenterX: 232,
		ScreenCenterY: 339,
	}
	if got[0] != want {
		t.Fatalf("expandOCRResultsAtOrigin(...)[0] = %#v, want %#v", got[0], want)
	}
}

func TestFormatOCRResultsIncludesScreenCoordinates(t *testing.T) {
	got := formatOCRResults(expandOCRResults([]ocrResult{
		{Text: "Extra High", Confidence: 0.99, X: 12, Y: 34, W: 40, H: 10},
	}, nil))
	for _, want := range []string{
		`center=(32,39)`,
		`bounds=(12,34 40x10)`,
		`screen_center=(32,39)`,
		`screen_bounds=(12,34 40x10)`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatOCRResults(...) missing %q in %q", want, got)
		}
	}
}

func TestFindOCRTextDedupesOverlappingRegions(t *testing.T) {
	results := []ocrResult{
		{Text: "Cost Graph Counters", X: 1414, Y: 92, W: 151, H: 20},
		{Text: "Cost Graph Conters", X: 1415, Y: 93, W: 150, H: 19},
		{Text: "Counters", X: 40, Y: 400, W: 60, H: 18},
	}
	matches := findOCRText(results, "Counters")
	if len(matches) != 2 {
		t.Fatalf("findOCRText(...) = %d matches, want 2 distinct regions: %+v", len(matches), matches)
	}
	if matches[0].Text != "Counters" {
		t.Errorf("matches[0].Text = %q, want the exact match first", matches[0].Text)
	}
	if matches[1].X != 1414 {
		t.Errorf("matches[1].X = %d, want the tab-bar region at 1414", matches[1].X)
	}
}

func TestOCRMatchPointTargetsSubstringSpan(t *testing.T) {
	block := ocrResult{Text: "ShadersHeat Map", X: 1067, Y: 94, W: 134, H: 18}

	x, y, note := ocrMatchPoint(block, "Shaders")
	if note == "" {
		t.Error("ocrMatchPoint(...) note is empty, want the fused-block adjustment reported")
	}
	if cx, _ := block.Center(); x >= cx {
		t.Errorf("ocrMatchPoint(...) x = %d, want left of the block center %d", x, cx)
	}
	if wantY := block.Y + block.H/2; y != wantY {
		t.Errorf("ocrMatchPoint(...) y = %d, want %d", y, wantY)
	}

	x, _, note = ocrMatchPoint(block, "Heat Map")
	if cx, _ := block.Center(); x <= cx {
		t.Errorf("ocrMatchPoint(...) x = %d, want right of the block center %d", x, cx)
	}
	if note == "" {
		t.Error("ocrMatchPoint(...) note is empty for the trailing label")
	}

	exact := ocrResult{Text: "Counters", X: 10, Y: 20, W: 80, H: 16}
	x, y, note = ocrMatchPoint(exact, "counters")
	if cx, cy := exact.Center(); x != cx || y != cy {
		t.Errorf("ocrMatchPoint(...) = (%d,%d), want the center (%d,%d) for a whole-block match", x, y, cx, cy)
	}
	if note != "" {
		t.Errorf("ocrMatchPoint(...) note = %q, want empty for a whole-block match", note)
	}
}
