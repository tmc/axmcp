package main

import (
	"fmt"
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

func TestOCRActionPointUsesOffsets(t *testing.T) {
	block := ocrResult{Text: "ShadersHeat Map", X: 1067, Y: 94, W: 134, H: 18}
	xOffset, yOffset := 28, 9
	x, y, note := ocrActionPoint(block, "Shaders", &xOffset, &yOffset)
	if x != 1095 || y != 103 {
		t.Errorf("ocrActionPoint(...) = (%d,%d), want (1095,103) relative to the bounds origin", x, y)
	}
	if note == "" {
		t.Error("ocrActionPoint(...) note is empty, want the offset reported")
	}

	if _, err := offsetsComplete(&xOffset, nil); err == nil {
		t.Error("offsetsComplete(x, nil) = nil error, want an error for a lone offset")
	}
	if ok, err := offsetsComplete(nil, nil); ok || err != nil {
		t.Errorf("offsetsComplete(nil, nil) = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestOCROptionsFromArgs(t *testing.T) {
	if opts := ocrOptionsFromArgs(axOCRInput{App: "Xcode"}); opts.Candidates != 1 || !opts.LanguageCorrection || opts.Fast {
		t.Errorf("ocrOptionsFromArgs(default) = %+v, want one spell-corrected candidate at accurate level", opts)
	}
	off := false
	opts := ocrOptionsFromArgs(axOCRInput{App: "Xcode", Candidates: 3, MinConfidence: 0.5, LanguageCorrection: &off, Fast: true})
	if opts.Candidates != 3 || opts.MinConfidence != 0.5 || opts.LanguageCorrection || !opts.Fast {
		t.Errorf("ocrOptionsFromArgs(tuned) = %+v, want the caller's knobs applied", opts)
	}
}

func TestRenderOCRLayoutKeepsColumnsAndContent(t *testing.T) {
	// Two rows of a two-column table, plus a label far below.
	results := []ocrResult{
		{Text: "Name", X: 100, Y: 100, W: 40, H: 16},
		{Text: "Cost", X: 600, Y: 101, W: 40, H: 16},
		{Text: "rmsbfloat16", X: 100, Y: 130, W: 110, H: 16},
		{Text: "5.52%", X: 600, Y: 131, W: 50, H: 16},
		{Text: "Counters", X: 100, Y: 400, W: 80, H: 16},
	}
	out := renderOCRLayout(results, 1000, 500, 80, 0)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("renderOCRLayout(...) = %q, want at least three rows", out)
	}
	for _, r := range results {
		if !strings.Contains(out, r.Text) {
			t.Errorf("renderOCRLayout(...) dropped %q:\n%s", r.Text, out)
		}
	}
	if got, want := strings.Index(lines[0], "Cost"), strings.Index(lines[1], "5.52%"); got != want {
		t.Errorf("column of %q = %d, %q = %d; want the table column aligned", "Cost", got, "5.52%", want)
	}
	if !strings.Contains(out, "\n\n") {
		t.Errorf("renderOCRLayout(...) = %q, want a blank line for the vertical gap", out)
	}
}

func TestRenderOCRLayoutSpillsOverlapInsteadOfDropping(t *testing.T) {
	// At 20 columns these two cannot share a row without overlapping.
	results := []ocrResult{
		{Text: "left column text", X: 0, Y: 50, W: 300, H: 16},
		{Text: "right column text", X: 320, Y: 50, W: 300, H: 16},
	}
	out := renderOCRLayout(results, 1000, 200, 20, 0)
	for _, r := range results {
		if !strings.Contains(out, r.Text) {
			t.Errorf("renderOCRLayout(...) dropped %q:\n%s", r.Text, out)
		}
	}
	if lines := strings.Split(strings.TrimRight(out, "\n"), "\n"); len(lines) != 2 {
		t.Errorf("renderOCRLayout(...) = %d rows, want the collision spilled onto a continuation row:\n%s", len(lines), out)
	}
}

func TestRenderOCRLayoutCapsRows(t *testing.T) {
	var results []ocrResult
	for i := range 10 {
		results = append(results, ocrResult{Text: fmt.Sprintf("row%d", i), X: 0, Y: i * 40, W: 40, H: 16})
	}
	out := renderOCRLayout(results, 400, 400, 40, 3)
	if !strings.Contains(out, "more rows") {
		t.Errorf("renderOCRLayout(...) = %q, want the truncation reported", out)
	}
	if strings.Contains(out, "row9") {
		t.Errorf("renderOCRLayout(...) = %q, want rows past the cap left out", out)
	}
}
