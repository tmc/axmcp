package computeruse

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"

	xdraw "golang.org/x/image/draw"
)

const MaxScreenshotLongSide = 1568

// NormalizeScreenshotPNG returns PNG data capped to maxLongSide and its
// returned pixel dimensions.
func NormalizeScreenshotPNG(pngData []byte, maxLongSide int) ([]byte, image.Config, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(pngData))
	if err != nil {
		return nil, image.Config{}, fmt.Errorf("decode screenshot: %w", err)
	}
	if maxLongSide <= 0 || cfg.Width <= maxLongSide && cfg.Height <= maxLongSide {
		return pngData, cfg, nil
	}
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return nil, image.Config{}, fmt.Errorf("decode screenshot: %w", err)
	}
	width, height := ScaledSize(cfg.Width, cfg.Height, maxLongSide)
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), xdraw.Over, nil)
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return nil, image.Config{}, fmt.Errorf("encode screenshot: %w", err)
	}
	return out.Bytes(), image.Config{ColorModel: dst.ColorModel(), Width: width, Height: height}, nil
}

// ScaledSize returns dimensions whose longest side is maxLongSide.
// If the input dimensions or maxLongSide are not positive, it returns the
// input dimensions unchanged.
func ScaledSize(width, height, maxLongSide int) (int, int) {
	if width <= 0 || height <= 0 || maxLongSide <= 0 {
		return width, height
	}
	if width >= height {
		return maxLongSide, max(1, int(math.Round(float64(height)*float64(maxLongSide)/float64(width))))
	}
	return max(1, int(math.Round(float64(width)*float64(maxLongSide)/float64(height)))), maxLongSide
}
