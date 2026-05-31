//go:build darwin

package appstate

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/computeruse"
)

func TestIsSettableRole(t *testing.T) {
	tests := []struct {
		role string
		want bool
	}{
		{role: "AXTextField", want: true},
		{role: "AXSlider", want: true},
		{role: "AXButton", want: false},
	}
	for _, tt := range tests {
		if got := isSettableRole(tt.role); got != tt.want {
			t.Fatalf("isSettableRole(%q) = %v, want %v", tt.role, got, tt.want)
		}
	}
}

func TestSnapshotStateRoundTrip(t *testing.T) {
	s := &Snapshot{
		state: computeruse.AppState{
			App: computeruse.AppInfo{Name: "Music"},
		},
	}
	if got := s.State().App.Name; got != "Music" {
		t.Fatalf("State().App.Name = %q, want Music", got)
	}
}

func TestSnapshotResolveMissing(t *testing.T) {
	s := &Snapshot{
		elements: map[int]*axuiautomation.Element{},
		nodes:    map[int]computeruse.ElementNode{},
	}
	if _, _, err := s.Resolve(1); err == nil {
		t.Fatalf("Resolve should fail for missing index")
	}
}

func TestWindowResolutionError(t *testing.T) {
	err := &WindowResolutionError{
		App:         computeruse.AppInfo{Name: "Brave Browser", BundleID: "com.brave.Browser", PID: 123},
		WindowTitle: "Smoke",
		Reason:      "no matching window found",
	}
	if got := err.Error(); got != `Brave Browser window "Smoke" unavailable: no matching window found` {
		t.Fatalf("Error() = %q", got)
	}
	var target *WindowResolutionError
	if !errors.As(err, &target) {
		t.Fatalf("errors.As failed")
	}
	if target.App.PID != 123 || target.App.BundleID != "com.brave.Browser" {
		t.Fatalf("target = %#v", target)
	}
}

func TestScaleScreenshotPNGPreservesSmallImage(t *testing.T) {
	pngData := testPNG(t, 320, 200)
	got, cfg, err := scaleScreenshotPNG(pngData, maxScreenshotLongSide)
	if err != nil {
		t.Fatalf("scaleScreenshotPNG: %v", err)
	}
	if !bytes.Equal(got, pngData) {
		t.Fatalf("scaleScreenshotPNG changed image below max long side")
	}
	if cfg.Width != 320 || cfg.Height != 200 {
		t.Fatalf("config = %dx%d, want 320x200", cfg.Width, cfg.Height)
	}
}

func TestScaleScreenshotPNGScalesLongSide(t *testing.T) {
	tests := []struct {
		name       string
		width      int
		height     int
		wantWidth  int
		wantHeight int
	}{
		{name: "wide", width: 3136, height: 1960, wantWidth: 1568, wantHeight: 980},
		{name: "tall", width: 1200, height: 2400, wantWidth: 784, wantHeight: 1568},
	}
	for _, tt := range tests {
		pngData := testPNG(t, tt.width, tt.height)
		got, cfg, err := scaleScreenshotPNG(pngData, maxScreenshotLongSide)
		if err != nil {
			t.Fatalf("%s: scaleScreenshotPNG: %v", tt.name, err)
		}
		if cfg.Width != tt.wantWidth || cfg.Height != tt.wantHeight {
			t.Fatalf("%s: config = %dx%d, want %dx%d", tt.name, cfg.Width, cfg.Height, tt.wantWidth, tt.wantHeight)
		}
		decoded, err := png.DecodeConfig(bytes.NewReader(got))
		if err != nil {
			t.Fatalf("%s: DecodeConfig: %v", tt.name, err)
		}
		if decoded.Width != tt.wantWidth || decoded.Height != tt.wantHeight {
			t.Fatalf("%s: encoded image = %dx%d, want %dx%d", tt.name, decoded.Width, decoded.Height, tt.wantWidth, tt.wantHeight)
		}
	}
}

func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 0xff, A: 0xff})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}
