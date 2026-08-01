package main

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
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

// renderOCRLayout renders OCR results as text laid out the way it appears on
// screen. Results are grouped into visual lines by vertical position, and each
// one is written at the column its left edge maps to, so columns of a table or
// panel stay aligned. Where two results would overlap at that scale, the later
// one spills onto a continuation row under the same line rather than being
// shifted sideways or dropped: position is what the layout is for.
//
// cols is the width of the character grid; zero picks a width from the median
// glyph width of the recognized text, so one column holds roughly one
// character. maxRows caps the output, reporting how much it left out; zero
// renders everything. Blank lines mark vertical gaps in the source.
func renderOCRLayout(results []ocrResult, imgW, imgH, cols, maxRows int) string {
	if imgW == 0 || imgH == 0 || len(results) == 0 {
		return ""
	}
	if cols <= 0 {
		cols = autoLayoutCols(results, imgW)
	}

	lines := groupOCRLines(results)
	medianH := medianOCRHeight(results)

	var rendered []string
	prevY := 0
	for i, line := range lines {
		if i > 0 {
			for range layoutGapRows(line.y-prevY, medianH) {
				rendered = append(rendered, "")
			}
		}
		prevY = line.y
		rendered = append(rendered, renderOCRLine(line.runs, imgW, cols)...)
	}
	rendered = markOCRGutters(rendered)

	var buf strings.Builder
	for i, line := range rendered {
		if maxRows > 0 && i >= maxRows {
			fmt.Fprintf(&buf, "... %d more rows (raise rows to see them)\n", len(rendered)-maxRows)
			break
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	return buf.String()
}

// markOCRGutters draws a vertical bar down each corridor of blank columns that
// runs the full height of the layout. Those corridors are the boundaries
// between side-by-side panes, and without them a row reads as one record when
// it is really unrelated text from several panes at the same height — a
// plausible-looking row is worse than a garbled one, because it gets believed.
func markOCRGutters(rows []string) []string {
	width := 0
	for _, row := range rows {
		width = max(width, len([]rune(row)))
	}
	if width == 0 {
		return rows
	}

	// Count how often each column carries text. A pane boundary is a column
	// almost always blank, not necessarily always: a wide title or a tooltip
	// may cross it a few times without making it any less of a boundary.
	hits := make([]int, width)
	content := 0
	for _, row := range rows {
		if strings.TrimSpace(row) == "" {
			continue
		}
		content++
		for i, r := range []rune(row) {
			if r != ' ' {
				hits[i]++
			}
		}
	}
	if content == 0 {
		return rows
	}
	occupied := make([]bool, width)
	for i, n := range hits {
		occupied[i] = n*20 > content // busier than 5% of content rows
	}

	// A corridor must be wide enough to be a pane boundary rather than the gap
	// between two columns of one table, and must have content on both sides.
	const minGutter = 4
	gutters := map[int]bool{}
	for start := 0; start < width; start++ {
		if occupied[start] {
			continue
		}
		end := start
		for end < width && !occupied[end] {
			end++
		}
		if end-start >= minGutter && start > 0 && end < width {
			gutters[(start+end)/2] = true
		}
		start = end
	}
	if len(gutters) == 0 {
		return rows
	}

	marked := make([]string, 0, len(rows))
	for _, row := range rows {
		if strings.TrimSpace(row) == "" {
			marked = append(marked, row) // blank rows mark vertical gaps
			continue
		}
		runes := []rune(row)
		for col := range gutters {
			runes = growOCRRow(runes, col+1)
			if runes[col] == ' ' {
				runes[col] = '|'
			}
			// Text crossing a boundary keeps the column: the bar is a reading
			// aid, never a reason to lose a character.
		}
		marked = append(marked, strings.TrimRight(string(runes), " "))
	}
	return marked
}

// ocrLine is one visual line of recognized text.
type ocrLine struct {
	y    int // vertical center, in image coordinates
	runs []ocrResult
}

// groupOCRLines groups results into visual lines by vertical center, within a
// tolerance derived from the median text height, and orders each line
// left-to-right.
func groupOCRLines(results []ocrResult) []ocrLine {
	sorted := append([]ocrResult(nil), results...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Y != sorted[j].Y {
			return sorted[i].Y < sorted[j].Y
		}
		return sorted[i].X < sorted[j].X
	})

	tolerance := medianOCRHeight(results) * 3 / 4
	var lines []ocrLine
	for _, r := range sorted {
		center := r.Y + r.H/2
		if n := len(lines); n > 0 && center-lines[n-1].y <= tolerance {
			lines[n-1].runs = append(lines[n-1].runs, r)
			continue
		}
		lines = append(lines, ocrLine{y: center, runs: []ocrResult{r}})
	}
	for i := range lines {
		sort.SliceStable(lines[i].runs, func(a, b int) bool {
			return lines[i].runs[a].X < lines[i].runs[b].X
		})
	}
	return lines
}

// renderOCRLine renders one visual line, returning the continuation rows that
// collisions spilled onto along with the line itself.
func renderOCRLine(runs []ocrResult, imgW, cols int) []string {
	var rows [][]rune
	for _, r := range runs {
		text := []rune(r.Text)
		start := r.X * cols / imgW
		if start < 0 {
			start = 0
		}
		placed := false
		for i, row := range rows {
			row = growOCRRow(row, start+len(text))
			if freeOCRSpan(row, start, len(text)) {
				copy(row[start:], text)
				rows[i] = row
				placed = true
				break
			}
		}
		if !placed {
			row := growOCRRow(nil, max(cols, start+len(text)))
			copy(row[start:], text)
			rows = append(rows, row)
		}
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, strings.TrimRight(string(row), " "))
	}
	return out
}

// growOCRRow returns row padded with blanks to at least n columns. A row grows
// past the requested grid width rather than clipping text: cols sets the scale
// text is positioned at, not a budget to truncate it to.
func growOCRRow(row []rune, n int) []rune {
	for len(row) < n {
		row = append(row, ' ')
	}
	return row
}

// freeOCRSpan reports whether n columns starting at start are blank, keeping a
// blank column ahead of the span so neighboring text does not run together.
func freeOCRSpan(row []rune, start, n int) bool {
	if start > 0 && row[start-1] != ' ' {
		return false
	}
	for i := start; i < start+n; i++ {
		if row[i] != ' ' {
			return false
		}
	}
	return true
}

// layoutGapRows converts a vertical gap between visual lines into blank rows,
// preserving the whitespace that separates sections without letting a tall
// empty region push the rest of the layout off screen.
func layoutGapRows(gap, medianH int) int {
	if medianH <= 0 {
		return 0
	}
	rows := gap*2/(medianH*3) - 1
	return min(max(rows, 0), 2)
}

// autoLayoutCols picks a grid width where one column holds about one
// character, from the median glyph width of the recognized text.
func autoLayoutCols(results []ocrResult, imgW int) int {
	widths := make([]float64, 0, len(results))
	for _, r := range results {
		if n := len([]rune(r.Text)); n > 0 && r.W > 0 {
			widths = append(widths, float64(r.W)/float64(n))
		}
	}
	if len(widths) == 0 {
		return 120
	}
	sort.Float64s(widths)
	glyph := widths[len(widths)/2]
	if glyph <= 0 {
		return 120
	}
	return min(max(int(float64(imgW)/glyph), 80), 240)
}

// medianOCRHeight returns the median height of the recognized text, the scale
// that line grouping and vertical gaps are measured in.
func medianOCRHeight(results []ocrResult) int {
	heights := make([]int, 0, len(results))
	for _, r := range results {
		if r.H > 0 {
			heights = append(heights, r.H)
		}
	}
	if len(heights) == 0 {
		return 16
	}
	sort.Ints(heights)
	return heights[len(heights)/2]
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
