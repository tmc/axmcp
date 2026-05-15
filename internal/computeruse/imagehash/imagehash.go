// Package imagehash computes small perceptual hashes for screenshots.
package imagehash

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
)

// DHash8 returns a 64-bit horizontal difference hash for a PNG image.
func DHash8(pngData []byte) (uint64, error) {
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return 0, fmt.Errorf("decode png: %w", err)
	}
	b := img.Bounds()
	if b.Empty() {
		return 0, fmt.Errorf("empty image")
	}
	var hash uint64
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if grayAt(img, b, x, y) > grayAt(img, b, x+1, y) {
				hash |= 1 << uint(y*8+x)
			}
		}
	}
	return hash, nil
}

func grayAt(img image.Image, b image.Rectangle, x, y int) uint32 {
	px := b.Min.X
	if b.Dx() > 1 {
		px += x * (b.Dx() - 1) / 8
	}
	py := b.Min.Y
	if b.Dy() > 1 {
		py += y * (b.Dy() - 1) / 7
	}
	r, g, bl, _ := img.At(px, py).RGBA()
	return 299*r + 587*g + 114*bl
}
