package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestDiffChangedRegionPNG(t *testing.T) {
	before := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	fillRect(before, before.Bounds(), color.NRGBA{R: 255, G: 255, B: 255, A: 255})

	after := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	fillRect(after, after.Bounds(), color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	fillRect(after, image.Rect(2, 3, 5, 6), color.NRGBA{R: 255, A: 255})

	beforePNG := mustPNG(t, before)
	afterPNG := mustPNG(t, after)

	diffPNG, bounds, changed, err := diffChangedRegionPNG(beforePNG, afterPNG, 1)
	if err != nil {
		t.Fatalf("diffChangedRegionPNG: %v", err)
	}
	if changed != 9 {
		t.Fatalf("changed pixels = %d, want 9", changed)
	}
	wantBounds := image.Rect(1, 2, 6, 7)
	if bounds != wantBounds {
		t.Fatalf("bounds = %v, want %v", bounds, wantBounds)
	}

	got := decodePNG(t, diffPNG)
	if got.Bounds() != image.Rect(0, 0, wantBounds.Dx(), wantBounds.Dy()) {
		t.Fatalf("diff image bounds = %v, want %v", got.Bounds(), image.Rect(0, 0, wantBounds.Dx(), wantBounds.Dy()))
	}
	if px := color.NRGBAModel.Convert(got.At(0, 0)).(color.NRGBA); px.A != 0 {
		t.Fatalf("unchanged padded pixel alpha = %d, want 0", px.A)
	}
	if px := color.NRGBAModel.Convert(got.At(1, 1)).(color.NRGBA); px != (color.NRGBA{R: 255, A: 255}) {
		t.Fatalf("changed pixel = %#v, want red", px)
	}
}

func TestDiffChangedRegionPNGNoChange(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	fillRect(img, img.Bounds(), color.NRGBA{R: 10, G: 20, B: 30, A: 255})

	diffPNG, bounds, changed, err := diffChangedRegionPNG(mustPNG(t, img), mustPNG(t, img), 4)
	if err != nil {
		t.Fatalf("diffChangedRegionPNG: %v", err)
	}
	if len(diffPNG) != 0 {
		t.Fatalf("diff PNG length = %d, want 0", len(diffPNG))
	}
	if changed != 0 {
		t.Fatalf("changed = %d, want 0", changed)
	}
	if !bounds.Empty() {
		t.Fatalf("bounds = %v, want empty", bounds)
	}
}

func TestMatchWindowInfo(t *testing.T) {
	windows := []windowInfo{
		{Title: "Preferences"},
		{Title: "Preferences Advanced"},
		{Title: "Console"},
	}

	win, ok := matchWindowInfo(windows, "preferences")
	if !ok {
		t.Fatal("matchWindowInfo exact match = false, want true")
	}
	if win.Title != "Preferences" {
		t.Fatalf("matchWindowInfo exact title = %q, want Preferences", win.Title)
	}

	win, ok = matchWindowInfo(windows, "advanced")
	if !ok {
		t.Fatal("matchWindowInfo substring match = false, want true")
	}
	if win.Title != "Preferences Advanced" {
		t.Fatalf("matchWindowInfo substring title = %q, want Preferences Advanced", win.Title)
	}
}

func TestWindowOwnerMatchesIdentifier(t *testing.T) {
	win := windowInfo{
		OwnerName: "A2UI Renderer",
		OwnerPID:  19238,
	}

	if !windowOwnerMatchesIdentifier(win, "19238") {
		t.Fatal("windowOwnerMatchesIdentifier(pid) = false, want true")
	}
	if !windowOwnerMatchesIdentifier(win, "renderer") {
		t.Fatal("windowOwnerMatchesIdentifier(name substring) = false, want true")
	}
	if windowOwnerMatchesIdentifier(win, "19239") {
		t.Fatal("windowOwnerMatchesIdentifier(wrong pid) = true, want false")
	}
}

func fillRect(img *image.NRGBA, rect image.Rectangle, c color.NRGBA) {
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
}

func mustPNG(t *testing.T, img image.Image) []byte {
	t.Helper()

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

func decodePNG(t *testing.T, data []byte) image.Image {
	t.Helper()

	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	return img
}
