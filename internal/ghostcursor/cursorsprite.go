package ghostcursor

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"sync"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objectivec"
	"github.com/tmc/apple/quartzcore"
)

const (
	cursorMaskThreshold     = 20
	cursorTargetExtent      = 13.2
	defaultPointerPath      = "/System/Library/PrivateFrameworks/AccessibilitySupport.framework/Versions/A/Frameworks/AccessibilityFoundation.framework/Versions/A/Resources/Cursors/default-pointer-1.png"
	cursorSpriteRasterScale = 12
	cursorMaskRasterScale   = 16
)

var referenceCursorMaskRows = []string{
	".....................",
	".....................",
	".....................",
	".....................",
	".....................",
	".......##............",
	"......######.........",
	".....##########......",
	".....#############...",
	"......##############.",
	"......##############.",
	"......###############",
	"......##############.",
	".......############..",
	".......###########...",
	".......#########.....",
	"........#######......",
	"........#######......",
	"........######.......",
	".........#####.......",
	"..........###........",
}

type cursorMask struct {
	width     int
	height    int
	hotX      float64
	hotY      float64
	baseScale float64
	bounds    image.Rectangle
	alpha     []uint8
}

type cursorSprite struct {
	image appkit.NSImage
}

type cursorSpriteSet struct {
	mask    cursorMask
	fogMask cursorMask
	maskImg appkit.NSImage
	aura    cursorSprite
	fill    cursorSprite
	outline cursorSprite
}

var (
	systemArrowOnce sync.Once
	systemArrowMask cursorMask
	systemArrowErr  error
)

func (c *Controller) applySpriteVisual(visual cursorVisualState) bool {
	sprites, ok := c.cursorSprites(visual.spriteActivity)
	if !ok {
		return false
	}
	tokens := visual.tokens
	fogPath := cursorPath(tokens.fogScale)

	c.applyFog(fogPath, tokens, visual.spriteActivity)
	applyCursorLayer(c.aura, sprites.aura, sprites.fogMask.frame(tokens.fogScale), fogSpriteOpacity(visual.spriteActivity, tokens))
	if c.fog.GetID() == 0 {
		c.aura.SetShadowColor(cgColor(visual.fogRed, visual.fogGreen, visual.fogBlue, 1))
		c.aura.SetShadowOpacity(float32(tokens.fogShadowAlpha))
		c.aura.SetShadowRadius(tokens.fogShadowBlur)
	} else {
		c.aura.SetShadowColor(0)
		c.aura.SetShadowOpacity(0)
		c.aura.SetShadowRadius(0)
	}
	c.aura.SetShadowOffset(corefoundation.CGSize{})
	c.aura.SetShadowPath(0)

	bodyPath := cursorPath(tokens.bodyScale)
	clearCursorLayer(c.halo)
	c.halo.SetFrame(windowFrame())
	c.halo.SetPath(bodyPath)
	c.halo.SetOpacity(1)
	c.halo.SetFillColor(cgColor(visual.bodyRed, visual.bodyGreen, visual.bodyBlue, tokens.bodyAlpha))
	c.halo.SetStrokeColor(0)
	c.halo.SetLineWidth(0)
	if c.fog.GetID() == 0 {
		c.halo.SetShadowColor(cgColor(visual.fogRed, visual.fogGreen, visual.fogBlue, 1))
		c.halo.SetShadowOpacity(float32(tokens.bodyShadowAlpha))
		c.halo.SetShadowRadius(tokens.bodyShadowBlur)
	} else {
		c.halo.SetShadowColor(0)
		c.halo.SetShadowOpacity(0)
		c.halo.SetShadowRadius(0)
	}
	c.halo.SetShadowOffset(corefoundation.CGSize{})
	c.halo.SetShadowPath(0)

	clearCursorLayer(c.dot)
	c.dot.SetFrame(windowFrame())
	c.dot.SetPath(bodyPath)
	c.dot.SetOpacity(1)
	c.dot.SetFillColor(0)
	c.dot.SetStrokeColor(cgColor(visual.outlineRed, visual.outlineGreen, visual.outlineBlue, tokens.outlineAlpha))
	c.dot.SetLineWidth(tokens.outlineWidth)
	c.dot.SetShadowOpacity(0)
	c.dot.SetShadowPath(0)
	return true
}

func applyCursorLayer(layer quartzcore.CAShapeLayer, sprite cursorSprite, frame corefoundation.CGRect, opacity float64) {
	layer.SetFrame(frame)
	layer.SetPath(0)
	layer.SetFillColor(0)
	layer.SetStrokeColor(0)
	layer.SetLineWidth(0)
	layer.SetContents(sprite.image)
	layer.SetContentsGravity(quartzcore.KCAGravityResizeAspect)
	layer.SetMagnificationFilter(quartzcore.KCAFilterLinear)
	layer.SetMinificationFilter(quartzcore.KCAFilterLinear)
	layer.SetOpacity(float32(clamp01(opacity)))
}

func clearCursorLayer(layer quartzcore.CAShapeLayer) {
	layer.SetContents(objectivec.Object{})
	layer.SetOpacity(1)
	layer.SetShadowOpacity(0)
	layer.SetShadowPath(0)
}

func fogSpriteOpacity(activity ActivityState, tokens cursorTokens) float64 {
	base := 0.0
	switch activity {
	case ActivityIdle:
		base = 0.07
	case ActivityThinking:
		base = 0.08
	case ActivityMoving, ActivityDragging:
		base = 0.18
	case ActivityPressed:
		base = 0.22
	case ActivityPaused:
		base = 0.04
	default:
		base = 0.08
	}
	return clamp01(base + tokens.fogAlpha*2.8)
}

func (c *Controller) cursorSprites(activity ActivityState) (cursorSpriteSet, bool) {
	if sprites, ok := c.cache[activity]; ok {
		return sprites, true
	}
	sprites, err := buildCursorSprites(c.palette, activity)
	if err != nil {
		return cursorSpriteSet{}, false
	}
	c.cache[activity] = sprites
	return sprites, true
}

func buildCursorSprites(p palette, activity ActivityState) (cursorSpriteSet, error) {
	mask, err := systemArrowCursorMask()
	if err != nil {
		return cursorSpriteSet{}, err
	}
	bodyRed, bodyGreen, bodyBlue := cursorBodyComponents(p, activity)
	outlineRed, outlineGreen, outlineBlue := cursorOutlineComponents(p, activity)
	fogRed, fogGreen, fogBlue := cursorFogComponents(p, activity)

	fillMask := mask.erode(2)
	if fillMask.bounds.Empty() {
		fillMask = mask.erode(1)
	}
	if fillMask.bounds.Empty() {
		fillMask = mask
	}
	outlineMask := mask.outline()
	fogMask := mask.softFogMask(7, 5.5)

	fillSprite, err := newCursorSprite(fillMask, bodyRed, bodyGreen, bodyBlue, 1)
	if err != nil {
		return cursorSpriteSet{}, err
	}
	outlineSprite, err := newCursorSprite(outlineMask, outlineRed, outlineGreen, outlineBlue, 1)
	if err != nil {
		return cursorSpriteSet{}, err
	}
	auraSprite, err := newCursorSprite(fogMask, fogRed, fogGreen, fogBlue, 1)
	if err != nil {
		return cursorSpriteSet{}, err
	}
	maskImage, err := pngImageFromMask(fogMask, 1, 1, 1, 1, cursorMaskRasterScale)
	if err != nil {
		return cursorSpriteSet{}, err
	}

	return cursorSpriteSet{
		mask:    mask,
		fogMask: fogMask,
		maskImg: maskImage,
		aura:    auraSprite,
		fill:    fillSprite,
		outline: outlineSprite,
	}, nil
}

func systemArrowCursorMask() (cursorMask, error) {
	systemArrowOnce.Do(func() {
		systemArrowMask, systemArrowErr = loadArrowCursorMask()
	})
	return systemArrowMask, systemArrowErr
}

func loadArrowCursorMask() (cursorMask, error) {
	if mask, err := loadReferenceCursorMask(); err == nil {
		return mask, nil
	}
	return loadDefaultPointerMask()
}

func loadReferenceCursorMask() (cursorMask, error) {
	if len(referenceCursorMaskRows) == 0 {
		return cursorMask{}, fmt.Errorf("empty reference cursor mask")
	}
	height := len(referenceCursorMaskRows)
	width := len(referenceCursorMaskRows[0])
	mask := cursorMask{
		width:  width,
		height: height,
		alpha:  make([]uint8, width*height),
	}
	for y, row := range referenceCursorMaskRows {
		if len(row) != width {
			return cursorMask{}, fmt.Errorf("jagged reference cursor mask")
		}
		for x, ch := range row {
			if ch != '#' {
				continue
			}
			mask.alpha[y*width+x] = 0xff
		}
	}
	mask.bounds = alphaBoundsFromMask(mask)
	if mask.bounds.Empty() {
		return cursorMask{}, fmt.Errorf("reference cursor bounds")
	}
	mask.hotX, mask.hotY = detectCursorHotspotFromMask(mask)
	maxExtent := math.Max(float64(mask.bounds.Dx()), float64(mask.bounds.Dy()))
	if maxExtent == 0 {
		return cursorMask{}, fmt.Errorf("reference cursor extent")
	}
	mask.baseScale = cursorTargetExtent / maxExtent
	return mask, nil
}

func loadDefaultPointerMask() (cursorMask, error) {
	raw, err := os.ReadFile(defaultPointerPath)
	if err != nil {
		return cursorMask{}, fmt.Errorf("read default pointer asset: %w", err)
	}
	decoded, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return cursorMask{}, fmt.Errorf("decode default pointer asset: %w", err)
	}
	nrgba := toNRGBA(decoded)
	bounds := alphaBounds(nrgba, cursorMaskThreshold)
	if bounds.Empty() {
		return cursorMask{}, fmt.Errorf("default pointer bounds")
	}
	hotX, hotY := detectCursorHotspot(nrgba)
	maxExtent := math.Max(float64(bounds.Dx()), float64(bounds.Dy()))
	if maxExtent == 0 {
		return cursorMask{}, fmt.Errorf("default pointer extent")
	}
	mask := cursorMask{
		width:     nrgba.Bounds().Dx(),
		height:    nrgba.Bounds().Dy(),
		hotX:      hotX,
		hotY:      hotY,
		baseScale: cursorTargetExtent / maxExtent,
		bounds:    bounds,
		alpha:     make([]uint8, nrgba.Bounds().Dx()*nrgba.Bounds().Dy()),
	}
	for y := 0; y < mask.height; y++ {
		for x := 0; x < mask.width; x++ {
			mask.alpha[y*mask.width+x] = nrgba.NRGBAAt(x, y).A
		}
	}
	return mask, nil
}

func detectCursorHotspot(src *image.NRGBA) (float64, float64) {
	bestX, bestY := src.Bounds().Dx()-1, src.Bounds().Dy()-1
	bestScore := math.MaxFloat64
	for y := 0; y < src.Bounds().Dy(); y++ {
		for x := 0; x < src.Bounds().Dx(); x++ {
			if src.NRGBAAt(x, y).A <= cursorMaskThreshold {
				continue
			}
			score := float64(x + y)
			if score > bestScore {
				continue
			}
			if score == bestScore && (y > bestY || (y == bestY && x >= bestX)) {
				continue
			}
			bestScore = score
			bestX = x
			bestY = y
		}
	}
	return float64(bestX) + 0.5, float64(bestY) + 0.5
}

func detectCursorHotspotFromMask(mask cursorMask) (float64, float64) {
	bestX, bestY := mask.width-1, mask.height-1
	bestScore := math.MaxFloat64
	for y := 0; y < mask.height; y++ {
		for x := 0; x < mask.width; x++ {
			if mask.alphaAt(x, y) <= cursorMaskThreshold {
				continue
			}
			score := float64(x + y)
			if score > bestScore {
				continue
			}
			if score == bestScore && (y > bestY || (y == bestY && x >= bestX)) {
				continue
			}
			bestScore = score
			bestX = x
			bestY = y
		}
	}
	return float64(bestX) + 0.5, float64(bestY) + 0.5
}

func newCursorSprite(mask cursorMask, red, green, blue, alpha float64) (cursorSprite, error) {
	image, err := pngImageFromMask(mask, red, green, blue, alpha, cursorSpriteRasterScale)
	if err != nil {
		return cursorSprite{}, err
	}
	return cursorSprite{
		image: image,
	}, nil
}

func pngImageFromMask(mask cursorMask, red, green, blue, alpha float64, rasterScale int) (appkit.NSImage, error) {
	if mask.width == 0 || mask.height == 0 {
		return appkit.NSImage{}, fmt.Errorf("empty cursor mask")
	}
	if rasterScale < 1 {
		rasterScale = 1
	}
	img := image.NewNRGBA(image.Rect(0, 0, mask.width*rasterScale, mask.height*rasterScale))
	baseAlpha := clamp01(alpha)
	for y := 0; y < img.Bounds().Dy(); y++ {
		sy := (float64(y)+0.5)/float64(rasterScale) - 0.5
		for x := 0; x < img.Bounds().Dx(); x++ {
			sx := (float64(x)+0.5)/float64(rasterScale) - 0.5
			a := sampleMaskAlpha(mask, sx, sy)
			if a <= 0 {
				continue
			}
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(clamp01(red)*255 + 0.5),
				G: uint8(clamp01(green)*255 + 0.5),
				B: uint8(clamp01(blue)*255 + 0.5),
				A: uint8(clamp01(a*baseAlpha)*255 + 0.5),
			})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return appkit.NSImage{}, fmt.Errorf("encode cursor image: %w", err)
	}
	nsimg := appkit.NewImageWithData(foundation.NewMutableDataWithBytesLength(buf.Bytes()))
	nsimg.SetSize(corefoundation.CGSize{Width: float64(mask.width), Height: float64(mask.height)})
	return nsimg, nil
}

func sampleMaskAlpha(mask cursorMask, sx, sy float64) float64 {
	if mask.width == 0 || mask.height == 0 {
		return 0
	}
	if sx < 0 {
		sx = 0
	} else if sx > float64(mask.width-1) {
		sx = float64(mask.width - 1)
	}
	if sy < 0 {
		sy = 0
	} else if sy > float64(mask.height-1) {
		sy = float64(mask.height - 1)
	}
	x0 := int(math.Floor(sx))
	y0 := int(math.Floor(sy))
	x1 := x0 + 1
	y1 := y0 + 1
	if x1 >= mask.width {
		x1 = mask.width - 1
	}
	if y1 >= mask.height {
		y1 = mask.height - 1
	}
	tx := sx - float64(x0)
	ty := sy - float64(y0)
	a00 := float64(mask.alphaAt(x0, y0)) / 255
	a10 := float64(mask.alphaAt(x1, y0)) / 255
	a01 := float64(mask.alphaAt(x0, y1)) / 255
	a11 := float64(mask.alphaAt(x1, y1)) / 255
	top := a00 + (a10-a00)*tx
	bottom := a01 + (a11-a01)*tx
	return top + (bottom-top)*ty
}

type contourVertex struct {
	X int
	Y int
}

type contourPoint struct {
	X float64
	Y float64
}

type contourEdge struct {
	From contourVertex
	To   contourVertex
}

func vectorPathForMask(mask cursorMask) (coregraphics.CGPathRef, error) {
	edges := maskContourEdges(mask)
	if len(edges) == 0 {
		return 0, fmt.Errorf("empty cursor contour")
	}
	loops := contourLoops(edges)
	if len(loops) == 0 {
		return 0, fmt.Errorf("cursor contour has no loop")
	}
	loop := largestContourLoop(loops)
	points := simplifyContour(loop)
	if len(points) < 3 {
		return 0, fmt.Errorf("cursor contour too small")
	}
	points = smoothContour(points, 2)
	tip := contourTip(points)
	for i := range points {
		points[i].X -= tip.X
		points[i].Y -= tip.Y
	}
	path := coregraphics.CGPathCreateMutable()
	start := midpoint(points[len(points)-1], points[0])
	coregraphics.CGPathMoveToPoint(path, nil, start.X, start.Y)
	for i, point := range points {
		next := points[(i+1)%len(points)]
		mid := midpoint(point, next)
		coregraphics.CGPathAddQuadCurveToPoint(path, nil, point.X, point.Y, mid.X, mid.Y)
	}
	coregraphics.CGPathCloseSubpath(path)
	return coregraphics.CGPathRef(path), nil
}

func midpoint(a, b contourPoint) contourPoint {
	return contourPoint{
		X: (a.X + b.X) * 0.5,
		Y: (a.Y + b.Y) * 0.5,
	}
}

func maskContourEdges(mask cursorMask) []contourEdge {
	edges := make([]contourEdge, 0)
	for y := 0; y < mask.height; y++ {
		for x := 0; x < mask.width; x++ {
			if mask.alphaAt(x, y) <= cursorMaskThreshold {
				continue
			}
			if mask.alphaAt(x, y-1) <= cursorMaskThreshold {
				edges = append(edges, contourEdge{
					From: contourVertex{X: x, Y: y},
					To:   contourVertex{X: x + 1, Y: y},
				})
			}
			if mask.alphaAt(x+1, y) <= cursorMaskThreshold {
				edges = append(edges, contourEdge{
					From: contourVertex{X: x + 1, Y: y},
					To:   contourVertex{X: x + 1, Y: y + 1},
				})
			}
			if mask.alphaAt(x, y+1) <= cursorMaskThreshold {
				edges = append(edges, contourEdge{
					From: contourVertex{X: x + 1, Y: y + 1},
					To:   contourVertex{X: x, Y: y + 1},
				})
			}
			if mask.alphaAt(x-1, y) <= cursorMaskThreshold {
				edges = append(edges, contourEdge{
					From: contourVertex{X: x, Y: y + 1},
					To:   contourVertex{X: x, Y: y},
				})
			}
		}
	}
	return edges
}

func contourLoops(edges []contourEdge) [][]contourVertex {
	next := make(map[contourVertex]contourVertex, len(edges))
	for _, edge := range edges {
		next[edge.From] = edge.To
	}
	visited := make(map[contourVertex]bool, len(edges))
	loops := make([][]contourVertex, 0, 1)
	for start := range next {
		if visited[start] {
			continue
		}
		loop := make([]contourVertex, 0, len(edges))
		cur := start
		for {
			if visited[cur] {
				break
			}
			visited[cur] = true
			loop = append(loop, cur)
			nxt, ok := next[cur]
			if !ok || nxt == start {
				break
			}
			cur = nxt
		}
		if len(loop) >= 3 {
			loops = append(loops, loop)
		}
	}
	return loops
}

func largestContourLoop(loops [][]contourVertex) []contourVertex {
	best := loops[0]
	bestArea := math.Abs(contourLoopArea(best))
	for _, loop := range loops[1:] {
		if area := math.Abs(contourLoopArea(loop)); area > bestArea {
			best = loop
			bestArea = area
		}
	}
	return best
}

func contourLoopArea(loop []contourVertex) float64 {
	if len(loop) < 3 {
		return 0
	}
	area := 0.0
	for i := range loop {
		j := (i + 1) % len(loop)
		area += float64(loop[i].X*loop[j].Y - loop[j].X*loop[i].Y)
	}
	return area / 2
}

func simplifyContour(loop []contourVertex) []contourPoint {
	if len(loop) == 0 {
		return nil
	}
	points := make([]contourPoint, 0, len(loop))
	for i, cur := range loop {
		prev := loop[(i-1+len(loop))%len(loop)]
		next := loop[(i+1)%len(loop)]
		if (prev.X == cur.X && cur.X == next.X) || (prev.Y == cur.Y && cur.Y == next.Y) {
			continue
		}
		points = append(points, contourPoint{X: float64(cur.X), Y: float64(cur.Y)})
	}
	if len(points) == 0 {
		for _, point := range loop {
			points = append(points, contourPoint{X: float64(point.X), Y: float64(point.Y)})
		}
	}
	return points
}

func smoothContour(points []contourPoint, iterations int) []contourPoint {
	if iterations <= 0 || len(points) < 3 {
		return points
	}
	out := points
	for ; iterations > 0; iterations-- {
		next := make([]contourPoint, 0, len(out)*2)
		for i, cur := range out {
			nxt := out[(i+1)%len(out)]
			next = append(next,
				contourPoint{
					X: cur.X*0.75 + nxt.X*0.25,
					Y: cur.Y*0.75 + nxt.Y*0.25,
				},
				contourPoint{
					X: cur.X*0.25 + nxt.X*0.75,
					Y: cur.Y*0.25 + nxt.Y*0.75,
				},
			)
		}
		out = next
	}
	return out
}

func contourTip(points []contourPoint) contourPoint {
	best := points[0]
	bestScore := best.X + best.Y*1.25
	for _, point := range points[1:] {
		score := point.X + point.Y*1.25
		if score < bestScore || (score == bestScore && point.X < best.X) {
			best = point
			bestScore = score
		}
	}
	return best
}

func (m cursorMask) frame(scale float64) corefoundation.CGRect {
	if scale <= 0 {
		scale = 1
	}
	displayScale := m.baseScale * scale
	width := float64(m.width) * displayScale
	height := float64(m.height) * displayScale
	tip := cursorTipPoint()
	return corefoundation.CGRect{
		Origin: corefoundation.CGPoint{
			X: tip.X - m.hotX*displayScale,
			Y: tip.Y - (float64(m.height)-m.hotY)*displayScale,
		},
		Size: corefoundation.CGSize{
			Width:  width,
			Height: height,
		},
	}
}

func (m cursorMask) alphaAt(x, y int) uint8 {
	if x < 0 || y < 0 || x >= m.width || y >= m.height {
		return 0
	}
	return m.alpha[y*m.width+x]
}

func (m cursorMask) erode(radius int) cursorMask {
	if radius <= 0 {
		return m
	}
	out := cursorMask{
		width:     m.width,
		height:    m.height,
		hotX:      m.hotX,
		hotY:      m.hotY,
		baseScale: m.baseScale,
		alpha:     make([]uint8, len(m.alpha)),
	}
	for y := 0; y < m.height; y++ {
		for x := 0; x < m.width; x++ {
			a := m.alphaAt(x, y)
			if a == 0 {
				continue
			}
			if !m.isSolidNeighborhood(x, y, radius) {
				continue
			}
			out.alpha[y*m.width+x] = a
		}
	}
	out.bounds = alphaBoundsFromMask(out)
	return out
}

func (m cursorMask) outline() cursorMask {
	out := cursorMask{
		width:     m.width,
		height:    m.height,
		hotX:      m.hotX,
		hotY:      m.hotY,
		baseScale: m.baseScale,
		alpha:     make([]uint8, len(m.alpha)),
	}
	for y := 0; y < m.height; y++ {
		for x := 0; x < m.width; x++ {
			a := m.alphaAt(x, y)
			if a == 0 {
				continue
			}
			if m.isSolidNeighborhood(x, y, 1) {
				continue
			}
			out.alpha[y*m.width+x] = a
		}
	}
	out.bounds = alphaBoundsFromMask(out)
	if out.bounds.Empty() {
		return m
	}
	return out
}

func (m cursorMask) softFogMask(padding int, radius float64) cursorMask {
	if padding < 0 {
		padding = 0
	}
	if radius <= 0 {
		return m
	}
	out := cursorMask{
		width:     m.width + 2*padding,
		height:    m.height + 2*padding,
		hotX:      m.hotX + float64(padding),
		hotY:      m.hotY + float64(padding),
		baseScale: m.baseScale,
		alpha:     make([]uint8, (m.width+2*padding)*(m.height+2*padding)),
	}
	intRadius := int(math.Ceil(radius))
	for y := 0; y < m.height; y++ {
		for x := 0; x < m.width; x++ {
			srcAlpha := m.alphaAt(x, y)
			if srcAlpha <= cursorMaskThreshold {
				continue
			}
			for dy := -intRadius; dy <= intRadius; dy++ {
				for dx := -intRadius; dx <= intRadius; dx++ {
					dist := math.Hypot(float64(dx), float64(dy))
					if dist > radius {
						continue
					}
					falloff := math.Pow(1-dist/radius, 1.7)
					if dx == 0 && dy == 0 {
						falloff = 1
					}
					if falloff <= 0 {
						continue
					}
					tx := x + padding + dx
					ty := y + padding + dy
					if tx < 0 || ty < 0 || tx >= out.width || ty >= out.height {
						continue
					}
					alpha := uint8(clamp01(float64(srcAlpha)/255.0*falloff)*255 + 0.5)
					idx := ty*out.width + tx
					if alpha > out.alpha[idx] {
						out.alpha[idx] = alpha
					}
				}
			}
		}
	}
	out.bounds = alphaBoundsFromMask(out)
	return out
}

func (m cursorMask) isSolidNeighborhood(x, y, radius int) bool {
	for dy := -radius; dy <= radius; dy++ {
		for dx := -radius; dx <= radius; dx++ {
			if m.alphaAt(x+dx, y+dy) <= cursorMaskThreshold {
				return false
			}
		}
	}
	return true
}

func alphaBounds(src *image.NRGBA, threshold uint8) image.Rectangle {
	minX, minY := src.Bounds().Dx(), src.Bounds().Dy()
	maxX, maxY := 0, 0
	for y := 0; y < src.Bounds().Dy(); y++ {
		for x := 0; x < src.Bounds().Dx(); x++ {
			if src.NRGBAAt(x, y).A <= threshold {
				continue
			}
			if x < minX {
				minX = x
			}
			if y < minY {
				minY = y
			}
			if x+1 > maxX {
				maxX = x + 1
			}
			if y+1 > maxY {
				maxY = y + 1
			}
		}
	}
	if maxX <= minX || maxY <= minY {
		return image.Rectangle{}
	}
	return image.Rect(minX, minY, maxX, maxY)
}

func alphaBoundsFromMask(mask cursorMask) image.Rectangle {
	minX, minY := mask.width, mask.height
	maxX, maxY := 0, 0
	for y := 0; y < mask.height; y++ {
		for x := 0; x < mask.width; x++ {
			if mask.alphaAt(x, y) <= cursorMaskThreshold {
				continue
			}
			if x < minX {
				minX = x
			}
			if y < minY {
				minY = y
			}
			if x+1 > maxX {
				maxX = x + 1
			}
			if y+1 > maxY {
				maxY = y + 1
			}
		}
	}
	if maxX <= minX || maxY <= minY {
		return image.Rectangle{}
	}
	return image.Rect(minX, minY, maxX, maxY)
}

func toNRGBA(src image.Image) *image.NRGBA {
	bounds := src.Bounds()
	if nrgba, ok := src.(*image.NRGBA); ok && nrgba.Rect == bounds {
		return nrgba
	}
	dst := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			dst.Set(x, y, src.At(x, y))
		}
	}
	return dst
}
