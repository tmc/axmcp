package screen

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"os/exec"

	"golang.org/x/image/draw"
)

// DefaultScale is the default scaling factor for screenshots.
const DefaultScale = 0.25

// CaptureSimulator captures a screenshot of the simulator window, scaled down.
func CaptureSimulator(ctx context.Context, udid string) ([]byte, error) {
	return CaptureSimulatorScaled(ctx, udid, DefaultScale)
}

// CaptureSimulatorScaled captures a screenshot and scales it by the given factor.
func CaptureSimulatorScaled(ctx context.Context, udid string, scale float64) ([]byte, error) {
	if udid == "" {
		return nil, fmt.Errorf("udid required")
	}

	cmd := exec.CommandContext(ctx, "xcrun", "simctl", "io", udid, "screenshot", "-")
	var buf bytes.Buffer
	cmd.Stdout = &buf

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to capture screenshot: %v", err)
	}

	if scale >= 1.0 {
		return buf.Bytes(), nil
	}

	img, err := png.Decode(&buf)
	if err != nil {
		return nil, fmt.Errorf("failed to decode screenshot: %v", err)
	}

	bounds := img.Bounds()
	newWidth := int(float64(bounds.Dx()) * scale)
	newHeight := int(float64(bounds.Dy()) * scale)

	scaled := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
	draw.ApproxBiLinear.Scale(scaled, scaled.Bounds(), img, bounds, draw.Over, nil)

	var out bytes.Buffer
	if err := png.Encode(&out, scaled); err != nil {
		return nil, fmt.Errorf("failed to encode scaled screenshot: %v", err)
	}

	return out.Bytes(), nil
}
