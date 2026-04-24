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
