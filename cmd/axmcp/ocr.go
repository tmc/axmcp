package main

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/vision"
	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/ghostcursor"
	"github.com/tmc/axmcp/internal/ui"
)

// ocrResult holds a single recognized text region with its pixel coordinates.
type ocrResult struct {
	Text       string  `json:"text"`
	Confidence float32 `json:"confidence"`
	X          int     `json:"x"`
	Y          int     `json:"y"`
	W          int     `json:"w"`
	H          int     `json:"h"`
}

type ocrOutputResult struct {
	Text          string  `json:"text"`
	Confidence    float32 `json:"confidence"`
	X             int     `json:"x"`
	Y             int     `json:"y"`
	W             int     `json:"w"`
	H             int     `json:"h"`
	CenterX       int     `json:"center_x"`
	CenterY       int     `json:"center_y"`
	ScreenX       int     `json:"screen_x"`
	ScreenY       int     `json:"screen_y"`
	ScreenW       int     `json:"screen_w"`
	ScreenH       int     `json:"screen_h"`
	ScreenCenterX int     `json:"screen_center_x"`
	ScreenCenterY int     `json:"screen_center_y"`
}

// Center returns the center point of the text region in pixel coordinates.
func (r ocrResult) Center() (int, int) {
	return r.X + r.W/2, r.Y + r.H/2
}

func expandOCRResults(results []ocrResult, target *axuiautomation.Element) []ocrOutputResult {
	screenX, screenY := 0, 0
	if target != nil {
		frame := target.Frame()
		screenX = int(math.Round(frame.Origin.X))
		screenY = int(math.Round(frame.Origin.Y))
	}
	return expandOCRResultsAtOrigin(results, screenX, screenY)
}

func expandOCRResultsAtOrigin(results []ocrResult, screenX, screenY int) []ocrOutputResult {
	out := make([]ocrOutputResult, 0, len(results))
	for _, r := range results {
		centerX, centerY := r.Center()
		out = append(out, ocrOutputResult{
			Text:          r.Text,
			Confidence:    r.Confidence,
			X:             r.X,
			Y:             r.Y,
			W:             r.W,
			H:             r.H,
			CenterX:       centerX,
			CenterY:       centerY,
			ScreenX:       screenX + r.X,
			ScreenY:       screenY + r.Y,
			ScreenW:       r.W,
			ScreenH:       r.H,
			ScreenCenterX: screenX + centerX,
			ScreenCenterY: screenY + centerY,
		})
	}
	return out
}

// ocrOptions tunes Vision text recognition. The zero value is not usable;
// call defaultOCROptions.
type ocrOptions struct {
	// Candidates is how many alternate readings to keep per recognized
	// region. Vision ranks them, and beyond the first they are spelling
	// variants of the same pixels ("Counters", "Conters"), sharing one
	// bounding box. More than one is useful for reading, harmful for acting.
	Candidates int

	// MinConfidence drops results Vision reports below this confidence.
	MinConfidence float32

	// LanguageCorrection spell-corrects toward dictionary words. It helps
	// prose and hurts identifiers, hex addresses, and symbol names.
	LanguageCorrection bool

	// Fast trades accuracy for speed. It loses small text, identifiers, and
	// hex addresses outright; prefer scoping the capture instead.
	Fast bool
}

// defaultOCROptions returns the options used when a caller does not tune
// recognition: the single best reading of each region, spell-corrected.
func defaultOCROptions() ocrOptions {
	return ocrOptions{Candidates: 1, LanguageCorrection: true}
}

// recognizeText runs Apple Vision OCR on PNG image data and returns results
// with bounding boxes converted to pixel coordinates.
//
// Recognition quality tracks capture resolution closely: adjacent labels such
// as a segmented tab bar fuse into one region when the image is downscaled,
// and — counterintuitively — also when it is upscaled, so callers should pass
// the image at its captured size and scope the capture rather than resize it.
func recognizeText(pngData []byte, imgWidth, imgHeight int, opts ocrOptions) ([]ocrResult, error) {
	if opts.Candidates < 1 {
		opts.Candidates = 1
	}
	nsData := foundation.NewDataWithBytesLength(pngData)
	handler := vision.NewImageRequestHandlerWithDataOptions(nsData, nil)

	request := vision.NewVNRecognizeTextRequest()
	level := vision.VNRequestTextRecognitionLevelAccurate
	if opts.Fast {
		level = vision.VNRequestTextRecognitionLevelFast
	}
	request.SetRecognitionLevel(level)
	request.SetUsesLanguageCorrection(opts.LanguageCorrection)

	ok, err := handler.PerformRequestsError([]vision.VNRequest{request.VNImageBasedRequest.VNRequest})
	if err != nil {
		return nil, fmt.Errorf("vision OCR: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("vision OCR: request failed")
	}

	observations := request.VNImageBasedRequest.VNRequest.Results()
	var results []ocrResult
	seen := map[string]bool{}
	for _, obs := range observations {
		textObs := vision.VNRecognizedTextObservationFromID(obs.ID)
		bb := textObs.BoundingBox()
		candidates := textObs.TopCandidates(uint(opts.Candidates))
		for _, c := range candidates {
			if float32(c.Confidence()) < opts.MinConfidence {
				continue
			}
			// Vision bounding boxes are normalized (0-1), origin at bottom-left.
			// Convert to pixel coordinates with origin at top-left.
			px := int(math.Round(bb.Origin.X * float64(imgWidth)))
			py := int(math.Round((1 - bb.Origin.Y - bb.Size.Height) * float64(imgHeight)))
			pw := int(math.Round(bb.Size.Width * float64(imgWidth)))
			ph := int(math.Round(bb.Size.Height * float64(imgHeight)))
			key := fmt.Sprintf("%s|%d|%d|%d|%d", c.String(), px, py, pw, ph)
			if seen[key] {
				continue
			}
			seen[key] = true
			results = append(results, ocrResult{
				Text:       c.String(),
				Confidence: float32(c.Confidence()),
				X:          px,
				Y:          py,
				W:          pw,
				H:          ph,
			})
		}
	}
	return results, nil
}

// ocrElementCapture captures a screenshot of the element and runs OCR on it.
func ocrElementCapture(el *axuiautomation.Element, opts ocrOptions) ([]ocrResult, []byte, error) {
	frame := el.Frame()
	w := int(frame.Size.Width)
	h := int(frame.Size.Height)
	if w == 0 || h == 0 {
		return nil, nil, fmt.Errorf("element has zero-size frame")
	}

	png, err := el.Screenshot()
	if err != nil {
		return nil, nil, fmt.Errorf("screenshot: %w", err)
	}
	ghostcursor.FlashCaptureRect(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: frame.Origin.X, Y: frame.Origin.Y},
		Size:   corefoundation.CGSize{Width: frame.Size.Width, Height: frame.Size.Height},
	})
	noteCLIVisualFeedback()
	results, err := recognizeText(png, w, h, opts)
	if err != nil {
		return nil, nil, err
	}
	return results, png, nil
}

// ocrElement captures a screenshot of the element and runs OCR on it.
func ocrElement(el *axuiautomation.Element, opts ocrOptions) ([]ocrResult, error) {
	results, _, err := ocrElementCapture(el, opts)
	return results, err
}

// ocrWindowResult holds a window OCR capture together with the window it came
// from, so callers can report and coordinate against the window actually
// captured rather than the one they asked for.
type ocrWindowResult struct {
	results []ocrResult
	png     []byte
	w, h    int
	win     windowInfo
}

// ocrWindowCapture captures a window screenshot and runs OCR using coordinates
// in the window's local coordinate space rather than raw screenshot pixels.
// An empty windowTitle selects the app's first listed window; the caller can
// see which one that was in the returned windowInfo.
func ocrWindowCapture(appName, windowTitle string, opts ocrOptions) (ocrWindowResult, error) {
	if !ui.IsScreenRecordingTrusted() {
		if !ui.WaitForScreenRecording(30 * time.Second) {
			return ocrWindowResult{}, fmt.Errorf("screen recording permission required for window OCR")
		}
	}
	windows, err := listAppWindows(appName)
	if err != nil || len(windows) == 0 {
		return ocrWindowResult{}, fmt.Errorf("no windows for %q — the app may have windows on another Space or display: %w", appName, err)
	}
	win := windows[0]
	if windowTitle != "" {
		var ok bool
		win, ok = matchWindowInfo(windows, windowTitle)
		if !ok {
			return ocrWindowResult{}, fmt.Errorf("no window matching %q found for %q", windowTitle, appName)
		}
	}
	png, err := captureWindow(win)
	if err != nil {
		return ocrWindowResult{}, fmt.Errorf("capture: %w", err)
	}
	coordW := int(math.Round(win.Width))
	coordH := int(math.Round(win.Height))
	if coordW <= 0 || coordH <= 0 {
		coordW, coordH, err = pngDimensions(png)
		if err != nil {
			return ocrWindowResult{}, fmt.Errorf("read image dimensions: %w", err)
		}
	}
	results, err := recognizeText(png, coordW, coordH, opts)
	return ocrWindowResult{results: results, png: png, w: coordW, h: coordH, win: win}, err
}

// ocrWindow captures a window screenshot and runs OCR using coordinates in the
// window's local coordinate space rather than raw screenshot pixels.
func ocrWindow(appName, windowTitle string, opts ocrOptions) ([]ocrResult, int, int, error) {
	capture, err := ocrWindowCapture(appName, windowTitle, opts)
	return capture.results, capture.w, capture.h, err
}

// pngDimensions reads width and height from PNG header (IHDR chunk).
func pngDimensions(data []byte) (int, int, error) {
	// PNG: 8-byte signature + IHDR chunk: 4 len + 4 type + 4 width + 4 height
	if len(data) < 24 {
		return 0, 0, fmt.Errorf("data too short for PNG header")
	}
	w := int(data[16])<<24 | int(data[17])<<16 | int(data[18])<<8 | int(data[19])
	h := int(data[20])<<24 | int(data[21])<<16 | int(data[22])<<8 | int(data[23])
	return w, h, nil
}

// formatOCRResults formats results as human-readable text lines.
func formatOCRResults(results []ocrOutputResult) string {
	var buf strings.Builder
	for _, r := range results {
		fmt.Fprintf(&buf, "[%.2f] %q center=(%d,%d) bounds=(%d,%d %dx%d) screen_center=(%d,%d) screen_bounds=(%d,%d %dx%d)\n",
			r.Confidence, r.Text, r.CenterX, r.CenterY, r.X, r.Y, r.W, r.H,
			r.ScreenCenterX, r.ScreenCenterY, r.ScreenX, r.ScreenY, r.ScreenW, r.ScreenH)
	}
	return buf.String()
}

// formatOCRResultsJSON formats results as indented JSON.
func formatOCRResultsJSON(results []ocrOutputResult) (string, error) {
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// renderOCRLayout places OCR text on a character grid that preserves the
// spatial layout of the original image. Each text result is positioned
// proportionally within a cols x rows grid using its left edge for
// horizontal placement. Overlapping text is placed on the nearest free
// row within 3 rows of its target; text that cannot be placed is dropped
// rather than displaced far from its source. Consecutive empty rows are
// collapsed to a single blank line.
func renderOCRLayout(results []ocrResult, imgW, imgH, cols, rows int) string {
	if imgW == 0 || imgH == 0 || len(results) == 0 {
		return ""
	}

	grid := make([][]byte, rows)
	for i := range grid {
		grid[i] = make([]byte, cols)
		for j := range grid[i] {
			grid[i][j] = ' '
		}
	}

	// Sort results top-to-bottom, left-to-right for deterministic placement.
	sorted := make([]ocrResult, len(results))
	copy(sorted, results)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0; j-- {
			if sorted[j].Y < sorted[j-1].Y || (sorted[j].Y == sorted[j-1].Y && sorted[j].X < sorted[j-1].X) {
				sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
			} else {
				break
			}
		}
	}

	for _, r := range sorted {
		// Map left edge of bounding box to grid column.
		startCol := r.X * cols / imgW
		row := r.Y * rows / imgH
		if row >= rows {
			row = rows - 1
		}

		text := r.Text
		if startCol < 0 {
			startCol = 0
		}
		if startCol+len(text) > cols {
			text = text[:max(0, cols-startCol)]
		}
		if len(text) == 0 {
			continue
		}

		// Try target row, then up to 3 rows away.
		const maxDrift = 3
		placed := false
		for delta := range maxDrift + 1 {
			for _, tryRow := range []int{row + delta, row - delta} {
				if tryRow < 0 || tryRow >= rows {
					continue
				}
				free := true
				for k := range len(text) {
					if grid[tryRow][startCol+k] != ' ' {
						free = false
						break
					}
				}
				if free {
					copy(grid[tryRow][startCol:], text)
					placed = true
					break
				}
			}
			if placed {
				break
			}
		}
	}

	// Render grid, collapsing consecutive empty rows.
	var buf strings.Builder
	prevEmpty := false
	for _, line := range grid {
		s := strings.TrimRight(string(line), " ")
		if s == "" {
			if !prevEmpty {
				buf.WriteByte('\n')
			}
			prevEmpty = true
			continue
		}
		prevEmpty = false
		buf.WriteString(s)
		buf.WriteByte('\n')
	}
	return buf.String()
}

// findOCRText searches OCR results for text containing the query string.
// Returns results sorted by relevance: normalized exact matches first, then
// shorter text, then by position (top-to-bottom, left-to-right).
func findOCRText(results []ocrResult, query string) []ocrResult {
	queryNorm := normalizeMatchString(query)
	var matches []ocrResult
	for _, r := range results {
		if strings.Contains(normalizeMatchString(r.Text), queryNorm) {
			matches = append(matches, r)
		}
	}
	// Sort: exact match first, then shorter text, then top-to-bottom.
	for i := 1; i < len(matches); i++ {
		for j := i; j > 0; j-- {
			a, b := matches[j], matches[j-1]
			aExact := normalizeMatchString(a.Text) == queryNorm
			bExact := normalizeMatchString(b.Text) == queryNorm
			swap := false
			switch {
			case aExact && !bExact:
				swap = true
			case !aExact && bExact:
				// keep
			case len(a.Text) < len(b.Text):
				swap = true
			case len(a.Text) == len(b.Text) && (a.Y < b.Y || (a.Y == b.Y && a.X < b.X)):
				swap = true
			}
			if swap {
				matches[j], matches[j-1] = matches[j-1], matches[j]
			}
		}
	}
	return dedupeOCRRegions(matches)
}

// dedupeOCRRegions drops matches that cover a region already claimed by an
// earlier, better-ranked match. Vision often reports the same block with
// slightly different spellings across calls ("Cost Graph Counters",
// "Cost Graph Conters"), which would otherwise inflate the match count and
// make a 1-based match index select a spelling rather than a region.
func dedupeOCRRegions(matches []ocrResult) []ocrResult {
	var kept []ocrResult
	for _, m := range matches {
		duplicate := false
		for _, k := range kept {
			if overlapsOCRRegion(k, m) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			kept = append(kept, m)
		}
	}
	return kept
}

// overlapsOCRRegion reports whether a and b cover mostly the same pixels,
// meaning more than half of the smaller region lies inside the larger.
func overlapsOCRRegion(a, b ocrResult) bool {
	areaA, areaB := a.W*a.H, b.W*b.H
	if areaA <= 0 || areaB <= 0 {
		return false
	}
	w := min(a.X+a.W, b.X+b.W) - max(a.X, b.X)
	h := min(a.Y+a.H, b.Y+b.H) - max(a.Y, b.Y)
	if w <= 0 || h <= 0 {
		return false
	}
	return 2*w*h > min(areaA, areaB)
}

// ocrMatchPoint returns the point to act on for query within result r, in the
// coordinate space of r. When query covers only part of r.Text — Vision fuses
// adjacent labels such as a segmented tab bar into a single block — the point
// is placed over the matched substring rather than the block center, so that
// clicking "Shaders" in a "ShadersHeat Map" block does not land on "Heat Map".
// note describes any adjustment, and is empty for a whole-block match.
func ocrMatchPoint(r ocrResult, query string) (x, y int, note string) {
	cx, cy := r.Center()
	text := []rune(strings.ToLower(displayString(r.Text)))
	want := []rune(strings.ToLower(strings.TrimSpace(query)))
	if len(want) == 0 || len(text) == 0 || len(want) >= len(text) {
		return cx, cy, ""
	}
	start := runeIndex(text, want)
	if start < 0 {
		return cx, cy, fmt.Sprintf("%q is part of the larger OCR block %q; clicking the block center", query, r.Text)
	}
	mid := start + len(want)/2
	x = r.X + r.W*mid/len(text)
	note = fmt.Sprintf("%q covers only part of the OCR block %q; targeting the matched span instead of the block center", query, r.Text)
	return x, cy, note
}

// runeIndex returns the index of the first occurrence of want in text, or -1.
func runeIndex(text, want []rune) int {
	for i := 0; i+len(want) <= len(text); i++ {
		if string(text[i:i+len(want)]) == string(want) {
			return i
		}
	}
	return -1
}
