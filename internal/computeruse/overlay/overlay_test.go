package overlay

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestDrawOCRMatches(t *testing.T) {
	var buf bytes.Buffer
	src := image.NewRGBA(image.Rect(0, 0, 64, 48))
	fill := color.RGBA{R: 240, G: 240, B: 240, A: 255}
	for y := 0; y < 48; y++ {
		for x := 0; x < 64; x++ {
			src.Set(x, y, fill)
		}
	}
	if err := png.Encode(&buf, src); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	out, err := DrawOCRMatches(buf.Bytes(), []Match{
		{Index: 1, Rect: image.Rect(8, 8, 24, 20), Confidence: 0.95, Role: RoleWinner},
		{Index: 2, Rect: image.Rect(30, 10, 50, 24), Confidence: 0.82, Role: RoleRunnerUp},
		{Rect: image.Rect(12, 28, 34, 40), Confidence: 0.20, Role: RoleFiltered},
		{Rect: image.Rect(40, 30, 60, 44), Role: RoleRedacted},
	}, Options{})
	if err != nil {
		t.Fatalf("DrawOCRMatches: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	if got := color.RGBAModel.Convert(img.At(8, 8)).(color.RGBA); got == fill {
		t.Fatal("winner outline did not modify the image")
	}
	if got := color.RGBAModel.Convert(img.At(45, 35)).(color.RGBA); got == fill {
		t.Fatal("redacted box did not fill the image")
	}
}

func TestDrawOCRMatchesAppliesCap(t *testing.T) {
	var buf bytes.Buffer
	src := image.NewRGBA(image.Rect(0, 0, 32, 32))
	if err := png.Encode(&buf, src); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	var matches []Match
	for i := 0; i < 10; i++ {
		matches = append(matches, Match{
			Index: i + 1,
			Rect:  image.Rect(i, i, i+4, i+4),
			Role:  RoleRunnerUp,
		})
	}
	out, err := DrawOCRMatches(buf.Bytes(), matches, Options{MaxBoxes: 3})
	if err != nil {
		t.Fatalf("DrawOCRMatches: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	if got := color.RGBAModel.Convert(img.At(20, 20)).(color.RGBA); got != (color.RGBA{}) {
		t.Fatalf("pixel from capped-out match changed = %#v, want transparent source pixel", got)
	}
}
