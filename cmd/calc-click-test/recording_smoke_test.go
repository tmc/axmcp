//go:build darwin

package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/apple/screencapturekit"
	"github.com/tmc/apple/x/axuiautomation"
)

func TestRecorderSmoke(t *testing.T) {
	if os.Getenv("CALC_CLICK_TEST_RECORDING_SMOKE") == "" {
		t.Skip("set CALC_CLICK_TEST_RECORDING_SMOKE=1 to run ScreenCaptureKit smoke test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	content, err := screencapturekit.GetSCShareableContentClass().GetShareableContent(ctx)
	if err != nil {
		t.Fatalf("get shareable content: %v", err)
	}
	displays := content.Displays()
	if len(displays) == 0 {
		t.Fatal("no displays available for ScreenCaptureKit smoke test")
	}
	frame := displays[0].Frame()
	filter := screencapturekit.NewContentFilterWithDisplayExcludingWindows(displays[0], nil)
	t.Logf("display frame=%+v", frame)
	t.Logf("filter content rect=%+v scale=%.2f", filter.ContentRect(), float64(filter.PointPixelScale()))
	width := math.Min(320, math.Max(80, frame.Size.Width-80))
	height := math.Min(240, math.Max(80, frame.Size.Height-80))
	capture := axuiautomation.Rect{
		Origin: axuiautomation.Point{X: frame.Origin.X + 40, Y: frame.Origin.Y + 40},
		Size:   axuiautomation.Size{Width: width, Height: height},
	}

	cfg := config{
		video:         true,
		videoFPS:      10,
		screenshotDir: t.TempDir(),
	}
	target, err := resolveRecordingTarget(capture)
	if err != nil {
		t.Fatalf("resolveRecordingTarget: %v", err)
	}
	recorder, err := newRecorder(cfg, capture)
	if err != nil {
		t.Fatalf("newRecorder: %v", err)
	}
	if recorder == nil {
		t.Fatal("newRecorder returned nil recorder")
	}
	if err := recorder.Start(); err != nil {
		t.Fatalf("recorder.Start: %v", err)
	}
	time.Sleep(1200 * time.Millisecond)
	if err := recorder.Stop(); err != nil {
		t.Fatalf("recorder.Stop: %v", err)
	}

	path := filepath.Join(cfg.screenshotDir, "run.mp4")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Size() == 0 {
		t.Fatalf("recorded empty video %s", path)
	}
	dims, err := videoDimensions(path)
	if err != nil {
		t.Fatalf("videoDimensions(%s): %v", path, err)
	}
	want := fmt.Sprintf("%dx%d", target.widthPx, target.heightPx)
	if dims != want {
		t.Fatalf("videoDimensions(%s) = %s, want %s", path, dims, want)
	}
}

func videoDimensions(path string) (string, error) {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return "", fmt.Errorf("look up ffprobe: %w", err)
	}
	cmd := exec.Command(ffprobe,
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height",
		"-of", "csv=p=0:s=x",
		path,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ffprobe %s: %v: %s", path, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
