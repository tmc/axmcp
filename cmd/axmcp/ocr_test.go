package main

import (
	"strings"
	"testing"
)

func TestExpandOCRResultsAtOrigin(t *testing.T) {
	got := expandOCRResultsAtOrigin([]ocrResult{
		{Text: "Extra High", Confidence: 0.99, X: 12, Y: 34, W: 40, H: 10},
	}, 200, 300)
	if len(got) != 1 {
		t.Fatalf("len(expandOCRResultsAtOrigin(...)) = %d, want 1", len(got))
	}
	want := ocrOutputResult{
		Text:          "Extra High",
		Confidence:    0.99,
		X:             12,
		Y:             34,
		W:             40,
		H:             10,
		CenterX:       32,
		CenterY:       39,
		ScreenX:       212,
		ScreenY:       334,
		ScreenW:       40,
		ScreenH:       10,
		ScreenCenterX: 232,
		ScreenCenterY: 339,
	}
	if got[0] != want {
		t.Fatalf("expandOCRResultsAtOrigin(...)[0] = %#v, want %#v", got[0], want)
	}
}

func TestFormatOCRResultsIncludesScreenCoordinates(t *testing.T) {
	got := formatOCRResults([]ocrResult{
		{Text: "Extra High", Confidence: 0.99, X: 12, Y: 34, W: 40, H: 10},
	}, nil)
	for _, want := range []string{
		`center=(32,39)`,
		`bounds=(12,34 40x10)`,
		`screen_center=(32,39)`,
		`screen_bounds=(12,34 40x10)`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatOCRResults(...) missing %q in %q", want, got)
		}
	}
}
