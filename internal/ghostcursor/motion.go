//go:build darwin

package ghostcursor

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
)

type screenPoint struct {
	X int
	Y int
}

type curvePoint struct {
	X float64
	Y float64
}

// SamplePath returns the screen-space keyframes for moving between start and
// end using the requested curve. The returned slice always contains the first
// and last point when the positions differ.
func SamplePath(start, end Position, opts MoveOptions) ([]Position, error) {
	x0, y0, err := resolveScreenPoint(start)
	if err != nil {
		return nil, err
	}
	x1, y1, err := resolveScreenPoint(end)
	if err != nil {
		return nil, err
	}
	if x0 == x1 && y0 == y1 {
		return []Position{ScreenPosition(x0, y0)}, nil
	}
	if opts.Duration <= 0 {
		return []Position{ScreenPosition(x0, y0), ScreenPosition(x1, y1)}, nil
	}
	points := sampleScreenPath(x0, y0, x1, y1, moveSteps(distance(x0, y0, x1, y1), opts.Duration), normalizeMoveOptions(opts))
	path := make([]Position, len(points))
	for i, p := range points {
		path[i] = ScreenPosition(p.X, p.Y)
	}
	return path, nil
}

func resolveScreenPoint(pos Position) (int, int, error) {
	if pos.Space != CoordinateSpaceScreen {
		return 0, 0, fmt.Errorf("unsupported coordinate space %d", pos.Space)
	}
	return int(math.Round(pos.X)), int(math.Round(pos.Y)), nil
}

func distance(x0, y0, x1, y1 int) float64 {
	return math.Hypot(float64(x1-x0), float64(y1-y0))
}

func normalizeMoveOptions(opts MoveOptions) MoveOptions {
	switch opts.CurveStyle {
	case CurveBezier, CurveEaseInOut, CurveLinear:
	default:
		opts.CurveStyle = CurveBezier
	}
	if opts.Strength == 0 {
		opts.Strength = 0.08
	}
	if opts.Jitter == 0 {
		opts.Jitter = 0.015
	}
	if opts.Jitter < 0 {
		opts.Jitter = 0
	}
	return opts
}

func sampleScreenPath(x0, y0, x1, y1, steps int, opts MoveOptions) []screenPoint {
	if steps < 1 {
		steps = 1
	}
	start := curvePoint{X: float64(x0), Y: float64(y0)}
	end := curvePoint{X: float64(x1), Y: float64(y1)}
	control1, control2 := computeBezierControls(x0, y0, x1, y1, opts.Strength, opts.Jitter)
	points := make([]screenPoint, 0, steps+1)
	appendPoint := func(x, y int) {
		if len(points) > 0 && points[len(points)-1].X == x && points[len(points)-1].Y == y {
			return
		}
		points = append(points, screenPoint{X: x, Y: y})
	}
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		progress := curveProgress(opts.CurveStyle, t)
		p := pointAlongCurve(start, control1, control2, end, progress, opts.CurveStyle)
		appendPoint(int(math.Round(p.X)), int(math.Round(p.Y)))
	}
	appendPoint(x1, y1)
	return points
}

func pointAlongCurve(start, control1, control2, end curvePoint, progress float64, style CurveStyle) curvePoint {
	switch style {
	case CurveLinear, CurveEaseInOut:
		return lerpCurvePoint(start, end, progress)
	default:
		return cubicBezierPoint(start, control1, control2, end, progress)
	}
}

func lerpCurvePoint(a, b curvePoint, t float64) curvePoint {
	return curvePoint{
		X: a.X + (b.X-a.X)*t,
		Y: a.Y + (b.Y-a.Y)*t,
	}
}

func cubicBezierPoint(p0, p1, p2, p3 curvePoint, t float64) curvePoint {
	u := 1 - t
	tt := t * t
	uu := u * u
	uuu := uu * u
	ttt := tt * t
	return curvePoint{
		X: uuu*p0.X + 3*uu*t*p1.X + 3*u*tt*p2.X + ttt*p3.X,
		Y: uuu*p0.Y + 3*uu*t*p1.Y + 3*u*tt*p2.Y + ttt*p3.Y,
	}
}

func computeBezierControls(x0, y0, x1, y1 int, strength, jitter float64) (curvePoint, curvePoint) {
	dx := float64(x1 - x0)
	dy := float64(y1 - y0)
	dist := math.Hypot(dx, dy)
	if dist == 0 {
		return curvePoint{X: float64(x0), Y: float64(y0)}, curvePoint{X: float64(x1), Y: float64(y1)}
	}
	nx, ny := -dy/dist, dx/dist
	sign, jitter1, jitter2 := deterministicCurveOffsets(x0, y0, x1, y1, jitter)
	swoop := clampFloat(dist*maxFloat(0.03, strength), 6, 24)
	off1 := (swoop + dist*jitter1) * sign
	off2 := (swoop*0.62 + dist*jitter2) * sign
	return curvePoint{
			X: float64(x0) + dx*0.30 + nx*off1,
			Y: float64(y0) + dy*0.30 + ny*off1,
		}, curvePoint{
			X: float64(x0) + dx*0.74 + nx*off2,
			Y: float64(y0) + dy*0.74 + ny*off2,
		}
}

func deterministicCurveOffsets(x0, y0, x1, y1 int, jitter float64) (sign, jitter1, jitter2 float64) {
	seed := curveSeed(x0, y0, x1, y1)
	sign = stableArcDirection(float64(x1-x0), float64(y1-y0))
	jitter1 = jitter * centeredUnit(seed>>9)
	jitter2 = jitter * centeredUnit(seed>>27)
	return sign, jitter1, jitter2
}

func stableArcDirection(dx, dy float64) float64 {
	if math.Abs(dx) >= math.Abs(dy) {
		if dx >= 0 {
			return -1
		}
		return 1
	}
	if dy >= 0 {
		return 1
	}
	return -1
}

func curveSeed(x0, y0, x1, y1 int) uint64 {
	const offset = 1469598103934665603
	const prime = 1099511628211
	h := uint64(offset)
	for _, v := range [...]int64{int64(x0), int64(y0), int64(x1), int64(y1)} {
		h ^= uint64(v) ^ uint64(v>>32)
		h *= prime
	}
	return h
}

func centeredUnit(v uint64) float64 {
	return float64(v&0xffff)/65535.0 - 0.5
}

func moveSteps(distance float64, duration time.Duration) int {
	if duration <= 0 {
		return 1
	}
	steps := int(math.Ceil(duration.Seconds() * 60))
	if distanceSteps := int(math.Ceil(distance / 16)); distanceSteps > steps {
		steps = distanceSteps
	}
	if steps < 1 {
		return 1
	}
	if steps > 240 {
		return 240
	}
	return steps
}

func circleFrame(size float64) corefoundation.CGRect {
	inset := (windowSize - size) / 2
	return corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: inset, Y: inset},
		Size:   corefoundation.CGSize{Width: size, Height: size},
	}
}

func windowFrame() corefoundation.CGRect {
	return corefoundation.CGRect{
		Size: corefoundation.CGSize{
			Width:  windowSize,
			Height: windowSize,
		},
	}
}

func roundedRectFrame(width, height float64) corefoundation.CGRect {
	return corefoundation.CGRect{
		Origin: corefoundation.CGPoint{
			X: (windowSize - width) / 2,
			Y: (windowSize - height) / 2,
		},
		Size: corefoundation.CGSize{
			Width:  width,
			Height: height,
		},
	}
}

func cursorTipPoint() corefoundation.CGPoint {
	return corefoundation.CGPoint{
		X: windowSize / 2,
		Y: windowSize / 2,
	}
}

var (
	arrowPathOnce sync.Once
	arrowPath     coregraphics.CGPathRef
)

func cursorPath(scale float64) coregraphics.CGPathRef {
	if scale <= 0 {
		scale = 1
	}
	tip := cursorTipPoint()
	transform := corefoundation.CGAffineTransform{
		A:  scale,
		D:  -scale,
		Tx: tip.X,
		Ty: tip.Y,
	}
	return coregraphics.CGPathCreateCopyByTransformingPath(baseArrowPath(), &transform)
}

func cursorFogPath(scale float64) coregraphics.CGPathRef {
	body := cursorPath(scale)
	strokeWidth := math.Max(2.2, 4.0*scale)
	stroked := coregraphics.CGPathCreateCopyByStrokingPath(
		body,
		nil,
		strokeWidth,
		coregraphics.KCGLineCapRound,
		coregraphics.KCGLineJoinRound,
		10,
	)
	path := coregraphics.CGPathCreateMutable()
	coregraphics.CGPathAddPath(path, nil, body)
	if stroked != 0 {
		coregraphics.CGPathAddPath(path, nil, stroked)
	}
	return coregraphics.CGPathRef(path)
}

func baseArrowPath() coregraphics.CGPathRef {
	arrowPathOnce.Do(func() {
		mask, err := systemArrowCursorMask()
		if err == nil {
			if path, err := vectorPathForMask(mask); err == nil {
				arrowPath = path
				return
			}
		}
		path := coregraphics.CGPathCreateMutable()
		points := []corefoundation.CGPoint{
			{X: -0.8, Y: 6.2},
			{X: -0.6, Y: 2.6},
			{X: 0.0, Y: 0.0},
			{X: 4.2, Y: 1.1},
			{X: 8.7, Y: 3.0},
			{X: 11.5, Y: 5.0},
			{X: 11.4, Y: 6.9},
			{X: 9.4, Y: 8.0},
			{X: 6.8, Y: 12.0},
			{X: 5.7, Y: 14.4},
			{X: 4.0, Y: 14.2},
			{X: 3.0, Y: 10.8},
			{X: 1.0, Y: 8.1},
			{X: -0.2, Y: 7.0},
		}
		coregraphics.CGPathMoveToPoint(path, nil, points[0].X, points[0].Y)
		for i := 1; i < len(points)-1; i++ {
			midX := (points[i].X + points[i+1].X) / 2
			midY := (points[i].Y + points[i+1].Y) / 2
			coregraphics.CGPathAddQuadCurveToPoint(path, nil, points[i].X, points[i].Y, midX, midY)
		}
		last := points[len(points)-1]
		coregraphics.CGPathAddQuadCurveToPoint(path, nil, last.X, last.Y, points[0].X, points[0].Y)
		coregraphics.CGPathCloseSubpath(path)
		arrowPath = coregraphics.CGPathRef(path)
	})
	return arrowPath
}

func expandRect(rect corefoundation.CGRect, padding float64) corefoundation.CGRect {
	if padding <= 0 {
		return rect
	}
	return corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: rect.Origin.X - padding, Y: rect.Origin.Y - padding},
		Size: corefoundation.CGSize{
			Width:  rect.Size.Width + 2*padding,
			Height: rect.Size.Height + 2*padding,
		},
	}
}

func insetRect(rect corefoundation.CGRect, padding float64) corefoundation.CGRect {
	if padding <= 0 {
		return rect
	}
	width := rect.Size.Width - 2*padding
	if width < 0 {
		width = 0
	}
	height := rect.Size.Height - 2*padding
	if height < 0 {
		height = 0
	}
	return corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: rect.Origin.X + padding, Y: rect.Origin.Y + padding},
		Size:   corefoundation.CGSize{Width: width, Height: height},
	}
}

func roundedRectPath(rect corefoundation.CGRect, radius float64) coregraphics.CGPathRef {
	path := coregraphics.CGPathCreateMutable()
	coregraphics.CGPathAddRoundedRect(path, nil, rect, radius, radius)
	return coregraphics.CGPathRef(path)
}
