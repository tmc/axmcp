package main

import (
	"encoding/json"
	"github.com/tmc/axmcp/internal/computeruse"
	"math"
	"testing"
)

func TestNativeCoordinateMapping(t *testing.T) {
	info := computeruse.ScreenshotInfo{ImageID: "image", SourceKind: "window", TargetWindow: 7, Width: 420, Height: 330, GlobalRect: computeruse.CaptureRect{X: -100.25, Y: 20.5, Width: 210, Height: 82.5}, ScaleX: 2, ScaleY: 4}
	for _, tt := range []struct {
		name  string
		point nativePoint
		want  nativePoint
		bad   bool
	}{
		{"fractional", nativePoint{94.5, 160.5}, nativePoint{-53, 60.625}, false},
		{"origin", nativePoint{}, nativePoint{-100.25, 20.5}, false},
		{"right boundary", nativePoint{420, 0}, nativePoint{}, true},
		{"bottom boundary", nativePoint{0, 330}, nativePoint{}, true},
		{"negative", nativePoint{-0.01, 0}, nativePoint{}, true},
		{"nan", nativePoint{math.NaN(), 0}, nativePoint{}, true},
		{"infinity", nativePoint{0, math.Inf(1)}, nativePoint{}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, got, err := mapNativePoint(&info, tt.point)
			if (err != nil) != tt.bad {
				t.Fatalf("error=%v", err)
			}
			if !tt.bad && got != tt.want {
				t.Fatalf("global=%+v,want%+v", got, tt.want)
			}
		})
	}
	invalid := info
	invalid.ScaleX = 1
	if _, _, err := mapNativePoint(&invalid, nativePoint{1, 1}); err == nil {
		t.Fatal("wrong transform accepted")
	}
	var decoded nativePoint
	if err := json.Unmarshal([]byte(`{"x":94.5,"y":160.5}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != (nativePoint{94.5, 160.5}) {
		t.Fatalf("JSON point=%+v", decoded)
	}
}
