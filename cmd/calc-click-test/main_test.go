package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/x/axuiautomation"
)

func TestParseSequence(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "empty", input: "", want: nil},
		{name: "digits", input: "7890", want: []string{"7", "8", "9", "0"}},
		{name: "spaces ignored", input: "7 8 9", want: []string{"7", "8", "9"}},
		{name: "comma list", input: "7, 8, +/-, =", want: []string{"7", "8", "+/-", "="}},
	}
	for _, tt := range tests {
		if got := parseSequence(tt.input); !reflect.DeepEqual(got, tt.want) {
			t.Fatalf("%s: parseSequence(%q) = %v, want %v", tt.name, tt.input, got, tt.want)
		}
	}
}

func TestCaptureFileName(t *testing.T) {
	tests := []struct {
		name  string
		cycle int
		step  int
		label string
		phase string
		want  string
	}{
		{name: "cycle phase", cycle: 1, phase: "phase-start", want: "cycle-001-phase-start.png"},
		{name: "digit label", cycle: 2, step: 1, label: "7", phase: "before-click", want: "cycle-002-step-01-before-click-7.png"},
		{name: "operator label", cycle: 3, step: 4, label: "+/-", phase: "idle-end", want: "cycle-003-step-04-idle-end-plus-minus.png"},
		{name: "equals label", cycle: 9, step: 2, label: "=", phase: "after-click", want: "cycle-009-step-02-after-click-equals.png"},
	}
	for _, tt := range tests {
		if got := captureFileName(tt.cycle, tt.step, tt.label, tt.phase); got != tt.want {
			t.Fatalf("%s: captureFileName(...) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestSplitWait(t *testing.T) {
	tests := []struct {
		name  string
		wait  time.Duration
		wantA time.Duration
		wantB time.Duration
	}{
		{name: "zero", wait: 0, wantA: 0, wantB: 0},
		{name: "even", wait: 400 * time.Millisecond, wantA: 200 * time.Millisecond, wantB: 200 * time.Millisecond},
		{name: "odd", wait: 401 * time.Millisecond, wantA: 200*time.Millisecond + 500*time.Microsecond, wantB: 200*time.Millisecond + 500*time.Microsecond},
	}
	for _, tt := range tests {
		gotA, gotB := splitWait(tt.wait)
		if gotA != tt.wantA || gotB != tt.wantB {
			t.Fatalf("%s: splitWait(%s) = (%s, %s), want (%s, %s)", tt.name, tt.wait, gotA, gotB, tt.wantA, tt.wantB)
		}
	}
}

func TestScreenCaptureRectArg(t *testing.T) {
	frame := axuiautomation.Rect{
		Origin: axuiautomation.Point{X: 10, Y: 20},
		Size:   axuiautomation.Size{Width: 100, Height: 200},
	}
	if got, want := screenCaptureRectArg(frame, 16), "0,4,126,232"; got != want {
		t.Fatalf("screenCaptureRectArg(...) = %q, want %q", got, want)
	}
}

func TestCaptureRect(t *testing.T) {
	frame := axuiautomation.Rect{
		Origin: axuiautomation.Point{X: 10, Y: 20},
		Size:   axuiautomation.Size{Width: 100, Height: 200},
	}
	got := captureRect(frame, 16)
	want := axuiautomation.Rect{
		Origin: axuiautomation.Point{X: 0, Y: 4},
		Size:   axuiautomation.Size{Width: 126, Height: 232},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("captureRect(...) = %+v, want %+v", got, want)
	}
}

func TestIntersectRect(t *testing.T) {
	a := corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 10, Y: 20},
		Size:   corefoundation.CGSize{Width: 50, Height: 40},
	}
	b := corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 30, Y: 10},
		Size:   corefoundation.CGSize{Width: 40, Height: 30},
	}
	got := intersectRect(a, b)
	want := corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 30, Y: 20},
		Size:   corefoundation.CGSize{Width: 30, Height: 20},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("intersectRect(...) = %+v, want %+v", got, want)
	}
}

func TestChoosePreferred(t *testing.T) {
	tests := []struct {
		name      string
		available []string
		preferred []string
		want      string
	}{
		{name: "preferred match", available: []string{"mov", "mp4"}, preferred: []string{"mp4"}, want: "mp4"},
		{name: "fallback to first", available: []string{"mov", "mkv"}, preferred: []string{"mp4"}, want: "mov"},
		{name: "empty", available: nil, preferred: []string{"mp4"}, want: ""},
	}
	for _, tt := range tests {
		if got := choosePreferred(tt.available, tt.preferred...); got != tt.want {
			t.Fatalf("%s: choosePreferred(%v, %v) = %q, want %q", tt.name, tt.available, tt.preferred, got, tt.want)
		}
	}
}

func TestRecordingDimension(t *testing.T) {
	tests := []struct {
		name  string
		size  float64
		scale float64
		want  int
	}{
		{name: "retina scale", size: 126, scale: 2, want: 252},
		{name: "fractional scale", size: 101.2, scale: 1.5, want: 152},
		{name: "default scale", size: 80, scale: 0, want: 80},
		{name: "minimum size", size: 0.2, scale: 1, want: 2},
	}
	for _, tt := range tests {
		if got := recordingDimension(tt.size, tt.scale); got != tt.want {
			t.Fatalf("%s: recordingDimension(%v, %v) = %d, want %d", tt.name, tt.size, tt.scale, got, tt.want)
		}
	}
}

func TestLocalSourceRect(t *testing.T) {
	capture := corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 120, Y: 240},
		Size:   corefoundation.CGSize{Width: 262, Height: 440},
	}
	content := corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 100, Y: 200},
		Size:   corefoundation.CGSize{Width: 1512, Height: 982},
	}
	got := localSourceRect(capture, content)
	want := corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 20, Y: 40},
		Size:   corefoundation.CGSize{Width: 262, Height: 440},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("localSourceRect(...) = %+v, want %+v", got, want)
	}
}

func TestPointPixelScale(t *testing.T) {
	tests := []struct {
		name   string
		pixels float64
		points float64
		want   float64
	}{
		{name: "retina", pixels: 3024, points: 1512, want: 2},
		{name: "standard", pixels: 1920, points: 1920, want: 1},
		{name: "invalid", pixels: 0, points: 1512, want: 0},
	}
	for _, tt := range tests {
		if got := pointPixelScale(tt.pixels, tt.points); got != tt.want {
			t.Fatalf("%s: pointPixelScale(%v, %v) = %v, want %v", tt.name, tt.pixels, tt.points, got, tt.want)
		}
	}
}

func TestWaitForVideoFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.mp4")
	writeErr := make(chan error, 1)
	go func() {
		time.Sleep(50 * time.Millisecond)
		writeErr <- os.WriteFile(path, []byte("video"), 0o644)
	}()
	info, err := waitForVideoFile(path, 500*time.Millisecond)
	if err := <-writeErr; err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err != nil {
		t.Fatalf("waitForVideoFile(%s): %v", path, err)
	}
	if info.Size() != 5 {
		t.Fatalf("waitForVideoFile(%s) size = %d, want 5", path, info.Size())
	}
}
