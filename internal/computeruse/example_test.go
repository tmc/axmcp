package computeruse_test

import (
	"fmt"
	"github.com/tmc/axmcp/internal/computeruse"
)

func ExampleScreenshotInfo() {
	image := computeruse.ScreenshotInfo{
		Width: 820, Height: 544,
		GlobalRect: computeruse.CaptureRect{X: 150, Y: 100, Width: 410, Height: 272},
		ScaleX:     2, ScaleY: 2,
	}
	// A raw image point maps to global macOS logical points using its capture.
	x, y := 188.0, 320.0
	fmt.Println(image.GlobalRect.X+x/image.ScaleX, image.GlobalRect.Y+y/image.ScaleY)
	// Output: 244 260
}

func ExampleCaptureRect() {
	r := computeruse.CaptureRect{X: -100.5, Y: 20.25, Width: 410, Height: 272}
	fmt.Println(r.X, r.Y, r.Width, r.Height)
	// Output: -100.5 20.25 410 272
}

func ExampleDisplayInfo() {
	d := computeruse.DisplayInfo{ID: 1, Bounds: computeruse.CaptureRect{Width: 1280, Height: 800}, PixelWidth: 2560, PixelHeight: 1600}
	fmt.Println(d.Bounds.Width, d.PixelWidth)
	// Output: 1280 2560
}
