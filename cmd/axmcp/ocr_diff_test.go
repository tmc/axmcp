//go:build darwin

package main

import (
	"strings"
	"testing"
)

func TestSnapshotTextFromOCRResults(t *testing.T) {
	results := []ocrResult{
		{Text: "Planner", Confidence: 0.91, X: 8, Y: 10, W: 50, H: 12},
		{Text: "Planer", Confidence: 0.40, X: 8, Y: 10, W: 50, H: 12},
		{Text: "Documents", Confidence: 0.88, X: 8, Y: 28, W: 70, H: 12},
		{Text: "Runtime", Confidence: 0.92, X: 8, Y: 52, W: 48, H: 12},
		{Text: "Gaps", Confidence: 0.92, X: 64, Y: 53, W: 30, H: 12},
	}

	got := snapshotTextFromOCRResults(results)
	want := "Planner\nDocuments\nRuntime Gaps"
	if got != want {
		t.Fatalf("snapshotTextFromOCRResults = %q, want %q", got, want)
	}
}

func TestNormalizeOCRSnapshotJSON(t *testing.T) {
	snapshot := `[
  {"text":"Planner","confidence":0.91,"x":8,"y":10,"w":50,"h":12},
  {"text":"Documents","confidence":0.88,"x":8,"y":28,"w":70,"h":12}
]`

	got, err := normalizeOCRSnapshot(snapshot, "json")
	if err != nil {
		t.Fatalf("normalizeOCRSnapshot: %v", err)
	}
	if got != "Planner\nDocuments" {
		t.Fatalf("normalizeOCRSnapshot = %q", got)
	}
}

func TestRenderOCRWordDiff(t *testing.T) {
	before := "Planner\nDocuments\nRuntime Gaps"
	after := "Planner\nInbox\nRuntime Gaps"

	got := renderOCRWordDiff(before, after)
	if !strings.Contains(got, "Planner\n") {
		t.Fatalf("renderOCRWordDiff missing unchanged prefix: %q", got)
	}
	if !strings.Contains(got, "[-Documents-]{+Inbox+}") {
		t.Fatalf("renderOCRWordDiff missing replacement: %q", got)
	}
	if !strings.Contains(got, "\nRuntime Gaps") {
		t.Fatalf("renderOCRWordDiff missing unchanged suffix: %q", got)
	}
}
