package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
)

func diffChangedRegionPNG(beforePNG, afterPNG []byte, padding int) ([]byte, image.Rectangle, int, error) {
	before, err := png.Decode(bytes.NewReader(beforePNG))
	if err != nil {
		return nil, image.Rectangle{}, 0, fmt.Errorf("decode before screenshot: %w", err)
	}
	after, err := png.Decode(bytes.NewReader(afterPNG))
	if err != nil {
		return nil, image.Rectangle{}, 0, fmt.Errorf("decode after screenshot: %w", err)
	}

	img, bounds, changed := diffChangedRegion(before, after, padding)
	if changed == 0 || img == nil {
		return nil, image.Rectangle{}, 0, nil
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, image.Rectangle{}, 0, fmt.Errorf("encode changed region: %w", err)
	}
	return buf.Bytes(), bounds, changed, nil
}

func diffChangedRegion(before, after image.Image, padding int) (*image.NRGBA, image.Rectangle, int) {
	union := before.Bounds().Union(after.Bounds())
	if union.Empty() {
		return nil, image.Rectangle{}, 0
	}

	minX, minY := union.Max.X, union.Max.Y
	maxX, maxY := union.Min.X, union.Min.Y
	changed := 0

	for y := union.Min.Y; y < union.Max.Y; y++ {
		for x := union.Min.X; x < union.Max.X; x++ {
			if samePixel(before, after, x, y) {
				continue
			}
			changed++
			if x < minX {
				minX = x
			}
			if y < minY {
				minY = y
			}
			if x >= maxX {
				maxX = x + 1
			}
			if y >= maxY {
				maxY = y + 1
			}
		}
	}
	if changed == 0 {
		return nil, image.Rectangle{}, 0
	}

	if padding < 0 {
		padding = 0
	}
	bounds := image.Rect(minX-padding, minY-padding, maxX+padding, maxY+padding).Intersect(after.Bounds())
	if bounds.Empty() {
		return nil, image.Rectangle{}, 0
	}

	out := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if samePixel(before, after, x, y) {
				continue
			}
			out.SetNRGBA(x-bounds.Min.X, y-bounds.Min.Y, pixelAt(after, x, y))
		}
	}
	return out, bounds, changed
}

func samePixel(before, after image.Image, x, y int) bool {
	return pixelAt(before, x, y) == pixelAt(after, x, y)
}

func pixelAt(img image.Image, x, y int) color.NRGBA {
	if !image.Pt(x, y).In(img.Bounds()) {
		return color.NRGBA{}
	}
	return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
}
