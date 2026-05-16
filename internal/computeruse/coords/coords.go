// Package coords converts between computer-use coordinate spaces.
package coords

import (
	"fmt"
	"math"

	"github.com/tmc/axmcp/internal/computeruse"
)

// Point is a point in window-local coordinates.
type Point struct {
	X int
	Y int
}

// ScreenPoint is a global CoreGraphics/SkyLight point in points.
type ScreenPoint struct {
	X int
	Y int
}

// ScreenshotPoint is a point in screenshot pixel coordinates.
type ScreenshotPoint struct {
	X int
	Y int
}

// ScreenshotPointToWindowLocal converts screenshot pixel coordinates to
// window-local point coordinates. It accounts for Retina scale by using the
// captured screenshot dimensions instead of assuming pixels and points match.
func ScreenshotPointToWindowLocal(window computeruse.WindowInfo, x, y int) (Point, error) {
	if x < 0 || y < 0 {
		return Point{}, fmt.Errorf("coordinates must be non-negative")
	}
	if window.Width <= 0 || window.Height <= 0 {
		return Point{}, fmt.Errorf("window has empty bounds")
	}
	if window.ScreenshotWidth <= 0 || window.ScreenshotHeight <= 0 {
		return Point{}, fmt.Errorf("window is missing screenshot dimensions")
	}
	localX := int(math.Round(float64(x) * float64(window.Width) / float64(window.ScreenshotWidth)))
	localY := int(math.Round(float64(y) * float64(window.Height) / float64(window.ScreenshotHeight)))
	if localX >= window.Width {
		localX = window.Width - 1
	}
	if localY >= window.Height {
		localY = window.Height - 1
	}
	return Point{X: localX, Y: localY}, nil
}

// WindowLocalToScreenshot converts a window-local point to screenshot pixel
// coordinates.
func WindowLocalToScreenshot(window computeruse.WindowInfo, point Point) (ScreenshotPoint, error) {
	if point.X < 0 || point.Y < 0 {
		return ScreenshotPoint{}, fmt.Errorf("coordinates must be non-negative")
	}
	if window.Width <= 0 || window.Height <= 0 {
		return ScreenshotPoint{}, fmt.Errorf("window has empty bounds")
	}
	if window.ScreenshotWidth <= 0 || window.ScreenshotHeight <= 0 {
		return ScreenshotPoint{}, fmt.Errorf("window is missing screenshot dimensions")
	}
	x := int(math.Round(float64(point.X) * float64(window.ScreenshotWidth) / float64(window.Width)))
	y := int(math.Round(float64(point.Y) * float64(window.ScreenshotHeight) / float64(window.Height)))
	if x >= window.ScreenshotWidth {
		x = window.ScreenshotWidth - 1
	}
	if y >= window.ScreenshotHeight {
		y = window.ScreenshotHeight - 1
	}
	return ScreenshotPoint{X: x, Y: y}, nil
}

// WindowLocalToScreen converts a window-local point to global screen points.
// WindowInfo.X/Y are already in the same top-left-origin global space used by
// AX frames and CGEvents, so negative display origins are preserved.
func WindowLocalToScreen(window computeruse.WindowInfo, point Point) (ScreenPoint, error) {
	if point.X < 0 || point.Y < 0 {
		return ScreenPoint{}, fmt.Errorf("coordinates must be non-negative")
	}
	if window.Width <= 0 || window.Height <= 0 {
		return ScreenPoint{}, fmt.Errorf("window has empty bounds")
	}
	return ScreenPoint{X: window.X + point.X, Y: window.Y + point.Y}, nil
}

// ScreenToWindowLocal converts global screen points to window-local points.
func ScreenToWindowLocal(window computeruse.WindowInfo, point ScreenPoint) (Point, error) {
	if window.Width <= 0 || window.Height <= 0 {
		return Point{}, fmt.Errorf("window has empty bounds")
	}
	local := Point{X: point.X - window.X, Y: point.Y - window.Y}
	if local.X < 0 || local.Y < 0 || local.X >= window.Width || local.Y >= window.Height {
		return Point{}, fmt.Errorf("screen point outside window")
	}
	return local, nil
}
