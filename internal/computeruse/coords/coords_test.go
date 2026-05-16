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

func TestWindowLocalToScreenshot(t *testing.T) {
	window := computeruse.WindowInfo{
		Width:            400,
		Height:           200,
		ScreenshotWidth:  800,
		ScreenshotHeight: 400,
	}
	point, err := WindowLocalToScreenshot(window, Point{X: 100, Y: 50})
	if err != nil {
		t.Fatalf("WindowLocalToScreenshot: %v", err)
	}
	if point.X != 200 || point.Y != 100 {
		t.Fatalf("point = %+v, want {200 100}", point)
	}
}

func TestWindowLocalToScreenshotClamps(t *testing.T) {
	window := computeruse.WindowInfo{
		Width:            400,
		Height:           200,
		ScreenshotWidth:  800,
		ScreenshotHeight: 400,
	}
	point, err := WindowLocalToScreenshot(window, Point{X: 400, Y: 200})
	if err != nil {
		t.Fatalf("WindowLocalToScreenshot: %v", err)
	}
	if point.X != 799 || point.Y != 399 {
		t.Fatalf("point = %+v, want {799 399}", point)
	}
}

func TestWindowScreenRoundTripPreservesNegativeDisplayOrigin(t *testing.T) {
	window := computeruse.WindowInfo{
		X:      -1280,
		Y:      120,
		Width:  640,
		Height: 480,
	}
	local := Point{X: 320, Y: 240}
	screen, err := WindowLocalToScreen(window, local)
	if err != nil {
		t.Fatalf("WindowLocalToScreen: %v", err)
	}
	if screen.X != -960 || screen.Y != 360 {
		t.Fatalf("screen = %+v, want {-960 360}", screen)
	}
	got, err := ScreenToWindowLocal(window, screen)
	if err != nil {
		t.Fatalf("ScreenToWindowLocal: %v", err)
	}
	if got != local {
		t.Fatalf("round trip = %+v, want %+v", got, local)
	}
}

func TestScreenToWindowLocalRejectsOutsidePoint(t *testing.T) {
	window := computeruse.WindowInfo{X: 10, Y: 20, Width: 100, Height: 80}
	if _, err := ScreenToWindowLocal(window, ScreenPoint{X: 9, Y: 20}); err == nil {
		t.Fatalf("ScreenToWindowLocal outside left = nil, want error")
	}
	if _, err := ScreenToWindowLocal(window, ScreenPoint{X: 110, Y: 20}); err == nil {
		t.Fatalf("ScreenToWindowLocal outside right = nil, want error")
	}
}
