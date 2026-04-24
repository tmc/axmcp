package coords

import (
	"testing"

	"github.com/tmc/axmcp/internal/computeruse"
)

func TestScreenshotPointToWindowLocal(t *testing.T) {
	window := computeruse.WindowInfo{
		Width:            400,
		Height:           200,
		ScreenshotWidth:  800,
		ScreenshotHeight: 400,
	}
	point, err := ScreenshotPointToWindowLocal(window, 200, 100)
	if err != nil {
		t.Fatalf("ScreenshotPointToWindowLocal: %v", err)
	}
	if point.X != 100 || point.Y != 50 {
		t.Fatalf("point = %+v, want {100 50}", point)
	}
}

func TestScreenshotPointToWindowLocalClamps(t *testing.T) {
	window := computeruse.WindowInfo{
		Width:            400,
		Height:           200,
		ScreenshotWidth:  800,
		ScreenshotHeight: 400,
	}
	point, err := ScreenshotPointToWindowLocal(window, 800, 400)
	if err != nil {
		t.Fatalf("ScreenshotPointToWindowLocal: %v", err)
	}
	if point.X != 399 || point.Y != 199 {
		t.Fatalf("point = %+v, want {399 199}", point)
	}
}
