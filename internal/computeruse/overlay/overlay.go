package overlay

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"sort"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

type MatchRole int

const (
	RoleWinner MatchRole = iota
	RoleRunnerUp
	RoleFiltered
	RoleRedacted
)

type Match struct {
	Index      int
	Rect       image.Rectangle
	Text       string
	Confidence float64
	Role       MatchRole
}

type Options struct {
	MaxBoxes      int
	WinnerColor   color.RGBA
	RunnerColor   color.RGBA
	FilteredColor color.RGBA
	RedactedColor color.RGBA
	LabelFace     font.Face
}

type labelRect struct {
	x0 int
	y0 int
	x1 int
	y1 int
}

func DrawOCRMatches(src []byte, matches []Match, opts Options) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, fmt.Errorf("decode png: %w", err)
	}
	rgba := image.NewRGBA(img.Bounds())
	draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Src)

	opts = normalizeOptions(opts)
	matches = clipMatches(matches, rgba.Bounds(), opts.MaxBoxes)
	if len(matches) == 0 {
		return src, nil
	}

	labels := make([]labelRect, 0, len(matches))
	for _, match := range matches {
		rect := match.Rect.Intersect(rgba.Bounds())
		if rect.Empty() {
			continue
		}
		switch match.Role {
		case RoleRedacted:
			fillRect(rgba, rect, opts.RedactedColor)
			drawOutline(rgba, rect, color.RGBA{R: 130, G: 136, B: 144, A: 255}, 2)
			labels = appendLabel(rgba, labels, rect, "[redacted]", color.RGBA{R: 64, G: 68, B: 74, A: 230}, opts.LabelFace)
		case RoleFiltered:
			drawGlowOutline(rgba, rect, opts.FilteredColor, 2)
			drawOutline(rgba, rect, opts.FilteredColor, 1)
		case RoleWinner, RoleRunnerUp:
			stroke := opts.RunnerColor
			width := 2
			fill := color.RGBA{R: stroke.R, G: stroke.G, B: stroke.B, A: 26}
			if match.Role == RoleWinner {
				stroke = opts.WinnerColor
				width = 3
				fill = color.RGBA{R: stroke.R, G: stroke.G, B: stroke.B, A: 38}
			}
			fillRect(rgba, rect, fill)
			drawGlowOutline(rgba, rect, stroke, width+2)
			drawOutline(rgba, rect, stroke, width)
			if match.Index > 0 {
				labels = appendLabel(rgba, labels, rect, fmt.Sprintf("[%d]", match.Index), stroke, opts.LabelFace)
			}
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, rgba); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}
	return buf.Bytes(), nil
}

func normalizeOptions(opts Options) Options {
	if opts.MaxBoxes <= 0 {
		opts.MaxBoxes = 20
	}
	if opts.WinnerColor == (color.RGBA{}) {
		opts.WinnerColor = color.RGBA{R: 255, G: 122, B: 36, A: 255}
	}
	if opts.RunnerColor == (color.RGBA{}) {
		opts.RunnerColor = color.RGBA{R: 52, G: 211, B: 255, A: 255}
	}
	if opts.FilteredColor == (color.RGBA{}) {
		opts.FilteredColor = color.RGBA{R: 255, G: 77, B: 109, A: 208}
	}
	if opts.RedactedColor == (color.RGBA{}) {
		opts.RedactedColor = color.RGBA{R: 107, G: 114, B: 128, A: 228}
	}
	if opts.LabelFace == nil {
		opts.LabelFace = basicfont.Face7x13
	}
	return opts
}

func clipMatches(matches []Match, bounds image.Rectangle, limit int) []Match {
	out := make([]Match, 0, min(len(matches), limit))
	for _, match := range matches {
		match.Rect = match.Rect.Intersect(bounds)
		if match.Rect.Empty() {
			continue
		}
		out = append(out, match)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if pri := rolePriority(out[i].Role) - rolePriority(out[j].Role); pri != 0 {
			return pri < 0
		}
		if out[i].Index != out[j].Index {
			if out[i].Index == 0 || out[j].Index == 0 {
				return out[i].Index > out[j].Index
			}
			return out[i].Index < out[j].Index
		}
		if out[i].Confidence != out[j].Confidence {
			return out[i].Confidence > out[j].Confidence
		}
		if out[i].Rect.Min.Y != out[j].Rect.Min.Y {
			return out[i].Rect.Min.Y < out[j].Rect.Min.Y
		}
		return out[i].Rect.Min.X < out[j].Rect.Min.X
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func rolePriority(role MatchRole) int {
	switch role {
	case RoleWinner:
		return 0
	case RoleRunnerUp:
		return 1
	case RoleRedacted:
		return 2
	default:
		return 3
	}
}

func fillRect(img *image.RGBA, rect image.Rectangle, c color.RGBA) {
	draw.Draw(img, rect, image.NewUniform(c), image.Point{}, draw.Over)
}

func drawOutline(img *image.RGBA, rect image.Rectangle, c color.RGBA, width int) {
	if rect.Empty() || width <= 0 {
		return
	}
	for i := 0; i < width; i++ {
		r := rect.Inset(i).Intersect(img.Bounds())
		if r.Empty() {
			return
		}
		for x := r.Min.X; x < r.Max.X; x++ {
			img.Set(x, r.Min.Y, c)
			img.Set(x, r.Max.Y-1, c)
		}
		for y := r.Min.Y; y < r.Max.Y; y++ {
			img.Set(r.Min.X, y, c)
			img.Set(r.Max.X-1, y, c)
		}
	}
}

func drawGlowOutline(img *image.RGBA, rect image.Rectangle, c color.RGBA, radius int) {
	if rect.Empty() || radius <= 0 {
		return
	}
	for i := radius; i >= 1; i-- {
		alpha := uint8(int(c.A) * i / (radius * 3))
		drawOutline(img, rect.Inset(-i), color.RGBA{R: c.R, G: c.G, B: c.B, A: alpha}, 1)
	}
}

func appendLabel(img *image.RGBA, existing []labelRect, rect image.Rectangle, text string, accent color.RGBA, face font.Face) []labelRect {
	if text == "" {
		return existing
	}
	d := &font.Drawer{Face: face}
	textW := d.MeasureString(text).Ceil()
	textH := face.Metrics().Height.Ceil()
	paddingX := 4
	paddingY := 2
	labelW := textW + 2*paddingX
	labelH := textH + 2*paddingY
	candidates := []labelRect{
		{x0: rect.Min.X, y0: max(rect.Min.Y-labelH-2, 0), x1: rect.Min.X + labelW, y1: max(rect.Min.Y-labelH-2, 0) + labelH},
		{x0: max(rect.Max.X-labelW, 0), y0: max(rect.Min.Y-labelH-2, 0), x1: max(rect.Max.X-labelW, 0) + labelW, y1: max(rect.Min.Y-labelH-2, 0) + labelH},
		{x0: rect.Min.X, y0: min(rect.Max.Y+2, img.Bounds().Max.Y-labelH), x1: rect.Min.X + labelW, y1: min(rect.Max.Y+2, img.Bounds().Max.Y-labelH) + labelH},
		{x0: max(rect.Max.X-labelW, 0), y0: min(rect.Max.Y+2, img.Bounds().Max.Y-labelH), x1: max(rect.Max.X-labelW, 0) + labelW, y1: min(rect.Max.Y+2, img.Bounds().Max.Y-labelH) + labelH},
	}
	var chosen labelRect
	chosenOK := false
	for _, candidate := range candidates {
		candidate = clampLabel(candidate, img.Bounds())
		if !intersectsAny(candidate, existing) {
			chosen = candidate
			chosenOK = true
			break
		}
	}
	if !chosenOK {
		chosen = clampLabel(candidates[0], img.Bounds())
	}
	fillRect(img, image.Rect(chosen.x0, chosen.y0, chosen.x1, chosen.y1), color.RGBA{R: 17, G: 24, B: 39, A: 228})
	drawOutline(img, image.Rect(chosen.x0, chosen.y0, chosen.x1, chosen.y1), accent, 1)
	d = &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(color.RGBA{R: 255, G: 255, B: 255, A: 255}),
		Face: face,
		Dot: fixed.Point26_6{
			X: fixed.I(chosen.x0 + paddingX),
			Y: fixed.I(chosen.y0 + paddingY + face.Metrics().Ascent.Ceil()),
		},
	}
	d.DrawString(text)
	return append(existing, chosen)
}

func clampLabel(r labelRect, bounds image.Rectangle) labelRect {
	w := r.x1 - r.x0
	h := r.y1 - r.y0
	x0 := max(bounds.Min.X, min(r.x0, bounds.Max.X-w))
	y0 := max(bounds.Min.Y, min(r.y0, bounds.Max.Y-h))
	return labelRect{x0: x0, y0: y0, x1: x0 + w, y1: y0 + h}
}

func intersectsAny(r labelRect, existing []labelRect) bool {
	for _, other := range existing {
		if r.x0 < other.x1 && r.x1 > other.x0 && r.y0 < other.y1 && r.y1 > other.y0 {
			return true
		}
	}
	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
