package main

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/tmc/apple/corefoundation"
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

func TestRenderOCRLayoutMarksPaneBoundaries(t *testing.T) {
	// Two panes with a corridor of blank pixels between them, plus one wide
	// title crossing the corridor.
	results := []ocrResult{
		{Text: "a title that spans both panes", X: 60, Y: 10, W: 700, H: 16},
		{Text: "navigator", X: 0, Y: 60, W: 180, H: 16},
		{Text: "inspector", X: 700, Y: 61, W: 180, H: 16},
		{Text: "entry", X: 0, Y: 90, W: 120, H: 16},
		{Text: "value", X: 700, Y: 91, W: 120, H: 16},
	}
	out := renderOCRLayout(results, 1000, 200, 100, 0)
	if !strings.Contains(out, "|") {
		t.Errorf("renderOCRLayout(...) = %q, want a boundary between the panes", out)
	}
	for _, r := range results {
		if !strings.Contains(out, r.Text) {
			t.Errorf("renderOCRLayout(...) lost %q to a boundary marker:\n%s", r.Text, out)
		}
	}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.Contains(line, "navigator") && !strings.Contains(line, "|") {
			t.Errorf("row %q spans both panes without a boundary marker", line)
		}
	}
}

func TestROIToCGRect(t *testing.T) {
	tests := []struct {
		name string
		roi  *ocrRegion
		imgW int
		imgH int
		want struct{ x, y, w, h float64 }
	}{
		{
			name: "nil region defaults to full image",
			roi:  nil,
			imgW: 1000,
			imgH: 500,
			want: struct{ x, y, w, h float64 }{0, 0, 1, 1},
		},
		{
			name: "zero width/height defaults to full image",
			roi:  &ocrRegion{X: 10, Y: 10, W: 0, H: 0},
			imgW: 1000,
			imgH: 500,
			want: struct{ x, y, w, h float64 }{0, 0, 1, 1},
		},
		{
			name: "sub-region normalized to vision space (bottom-left origin)",
			roi:  &ocrRegion{X: 300, Y: 100, W: 400, H: 200},
			imgW: 1000,
			imgH: 500,
			want: struct{ x, y, w, h float64 }{0.3, 0.4, 0.4, 0.4},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := roiToCGRect(tt.roi, tt.imgW, tt.imgH)
			if got.Origin.X != tt.want.x || got.Origin.Y != tt.want.y || got.Size.Width != tt.want.w || got.Size.Height != tt.want.h {
				t.Errorf("roiToCGRect(%+v, %d, %d) = origin(%v,%v) size(%v,%v), want origin(%v,%v) size(%v,%v)",
					tt.roi, tt.imgW, tt.imgH,
					got.Origin.X, got.Origin.Y, got.Size.Width, got.Size.Height,
					tt.want.x, tt.want.y, tt.want.w, tt.want.h)
			}
		})
	}
}

func TestMapOBSToPixel(t *testing.T) {
	tests := []struct {
		name                       string
		bb                         struct{ x, y, w, h float64 }
		roi                        *ocrRegion
		imgW, imgH                 int
		wantX, wantY, wantW, wantH int
	}{
		{
			name:  "roi-relative box maps to full-image pixels",
			bb:    struct{ x, y, w, h float64 }{x: 0.1852, y: 0.0, w: 0.1, h: 0.1},
			roi:   &ocrRegion{X: 300, Y: 0, W: 400, H: 1000},
			imgW:  1000,
			imgH:  1000,
			wantX: 374,
			wantY: 900,
			wantW: 40,
			wantH: 100,
		},
		{
			name:  "nil ROI uses full image coordinates",
			bb:    struct{ x, y, w, h float64 }{x: 0.1, y: 0.2, w: 0.3, h: 0.4},
			roi:   nil,
			imgW:  1000,
			imgH:  500,
			wantX: 100,
			wantY: 200, // (1 - 0.2 - 0.4) * 500 = 200
			wantW: 300,
			wantH: 200,
		},
		{
			name:  "sub-region ROI maps back to full image top-left coordinates",
			bb:    struct{ x, y, w, h float64 }{x: 0.0, y: 0.5, w: 0.5, h: 0.25},
			roi:   &ocrRegion{X: 100, Y: 200, W: 300, H: 400},
			imgW:  1000,
			imgH:  1000,
			wantX: 100,
			wantY: 300, // 200 + (1 - 0.5 - 0.25) * 400 = 300
			wantW: 150, // 0.5 * 300 = 150
			wantH: 100, // 0.25 * 400 = 100
		},
		{
			name:  "top-left corner of ROI",
			bb:    struct{ x, y, w, h float64 }{x: 0.0, y: 0.8, w: 0.2, h: 0.2},
			roi:   &ocrRegion{X: 200, Y: 100, W: 500, H: 500},
			imgW:  1000,
			imgH:  1000,
			wantX: 200,
			wantY: 100, // 100 + (1 - 0.8 - 0.2) * 500 = 100
			wantW: 100,
			wantH: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cgBB := corefoundation.CGRect{
				Origin: corefoundation.CGPoint{X: tt.bb.x, Y: tt.bb.y},
				Size:   corefoundation.CGSize{Width: tt.bb.w, Height: tt.bb.h},
			}
			px, py, pw, ph := mapOBSToPixel(cgBB, tt.roi, tt.imgW, tt.imgH)
			if px != tt.wantX || py != tt.wantY || pw != tt.wantW || ph != tt.wantH {
				t.Errorf("mapOBSToPixel(%+v, %+v, %d, %d) = (%d,%d %dx%d), want (%d,%d %dx%d)",
					tt.bb, tt.roi, tt.imgW, tt.imgH,
					px, py, pw, ph,
					tt.wantX, tt.wantY, tt.wantW, tt.wantH)
			}
		})
	}
}

func TestParseOCRRegion(t *testing.T) {
	tests := []struct {
		input   string
		want    *ocrRegion
		wantErr bool
	}{
		{"10,20,300,200", &ocrRegion{X: 10, Y: 20, W: 300, H: 200}, false},
		{"10 20 300 200", &ocrRegion{X: 10, Y: 20, W: 300, H: 200}, false},
		{" 100 , 50 , 400 , 300 ", &ocrRegion{X: 100, Y: 50, W: 400, H: 300}, false},
		{"10,20,300", nil, true},
		{"invalid", nil, true},
		{"10,20,0,200", nil, true},
		{"10,20,100,-5", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseOCRRegion(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseOCRRegion(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && *got != *tt.want {
				t.Errorf("parseOCRRegion(%q) = %+v, want %+v", tt.input, *got, *tt.want)
			}
		})
	}
}

func TestValidateOCRRegion(t *testing.T) {
	tests := []struct {
		name    string
		roi     *ocrRegion
		imgW    int
		imgH    int
		wantErr string
	}{
		{
			name:    "valid region inside capture",
			roi:     &ocrRegion{X: 100, Y: 200, W: 300, H: 400},
			imgW:    1000,
			imgH:    1000,
			wantErr: "",
		},
		{
			name:    "nil region is valid",
			roi:     nil,
			imgW:    1000,
			imgH:    1000,
			wantErr: "",
		},
		{
			name:    "out-of-bounds region beyond width and height",
			roi:     &ocrRegion{X: 99999, Y: 99999, W: 500, H: 500},
			imgW:    3840,
			imgH:    2130,
			wantErr: "region 99999,99999 500x500 lies outside the 3840x2130 capture",
		},
		{
			name:    "negative origin coordinates",
			roi:     &ocrRegion{X: -10, Y: 0, W: 100, H: 100},
			imgW:    1000,
			imgH:    1000,
			wantErr: "region -10,0 100x100 lies outside the 1000x1000 capture",
		},
		{
			name:    "region extending beyond image right edge",
			roi:     &ocrRegion{X: 900, Y: 0, W: 200, H: 100},
			imgW:    1000,
			imgH:    1000,
			wantErr: "region 900,0 200x100 lies outside the 1000x1000 capture",
		},
		{
			name:    "empty region",
			roi:     &ocrRegion{X: 10, Y: 10, W: 0, H: 50},
			imgW:    1000,
			imgH:    1000,
			wantErr: "region 10,10 0x50 is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateOCRRegion(tt.roi, tt.imgW, tt.imgH)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("validateOCRRegion(%+v, %d, %d) unexpected error: %v", tt.roi, tt.imgW, tt.imgH, err)
				}
			} else {
				if err == nil || err.Error() != tt.wantErr {
					t.Errorf("validateOCRRegion(%+v, %d, %d) = %v, want error %q", tt.roi, tt.imgW, tt.imgH, err, tt.wantErr)
				}
			}
		})
	}
}

// TestRecognizeTextRepeated runs Vision several times on the same image so that
// an over-release of the request objects crashes here rather than in a server.
func TestRecognizeTextRepeated(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 200, 100))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := recognizeText(buf.Bytes(), 200, 100, defaultOCROptions()); err != nil {
			t.Fatalf("recognizeText: %v", err)
		}
	}
}
