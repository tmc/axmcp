//go:build darwin

package main

import "testing"

func TestSelectOCRMatchPrefersNormalizedExactMatch(t *testing.T) {
	results := []ocrResult{
		{Text: "Push it", X: 10, Y: 10, W: 40, H: 10},
		{Text: "Push", X: 5, Y: 5, W: 30, H: 10},
		{Text: "Push now", X: 20, Y: 20, W: 60, H: 10},
	}

	selection, err := selectOCRMatch(results, "  push  ", nil)
	if err != nil {
		t.Fatalf("selectOCRMatch: %v", err)
	}
	if selection.match.Text != "Push" {
		t.Fatalf("match = %q, want exact normalized match", selection.match.Text)
	}
	if selection.index != 1 {
		t.Fatalf("index = %d, want 1", selection.index)
	}
}

func TestSelectOCRMatchUsesExplicitIndex(t *testing.T) {
	results := []ocrResult{
		{Text: "Play", X: 10, Y: 10, W: 40, H: 10},
		{Text: "Play", X: 20, Y: 20, W: 40, H: 10},
		{Text: "Play", X: 30, Y: 30, W: 40, H: 10},
	}
	idx := 3
	selection, err := selectOCRMatch(results, "play", &idx)
	if err != nil {
		t.Fatalf("selectOCRMatch: %v", err)
	}
	if selection.match.X != 30 {
		t.Fatalf("x = %d, want third hit", selection.match.X)
	}
	if selection.resolved == "" {
		t.Fatal("expected resolution note")
	}
}

func TestResolveOCRActionableTargetPrefersContainingExactMatch(t *testing.T) {
	root := elementSnapshot{record: elementRecord{x: 100, y: 200, w: 400, h: 300}}
	candidates := []elementSnapshot{
		{
			record: elementRecord{
				role:       "AXButton",
				title:      "Cancel",
				enabled:    true,
				visible:    true,
				actionable: true,
				depth:      2,
				index:      1,
				x:          210,
				y:          300,
				w:          150,
				h:          40,
			},
		},
		{
			record: elementRecord{
				role:       "AXButton",
				title:      "Add window screenshot diff",
				enabled:    true,
				visible:    true,
				actionable: true,
				depth:      3,
				index:      2,
				x:          220,
				y:          310,
				w:          220,
				h:          30,
			},
		},
	}

	got, note, ok := resolveOCRActionableTarget(root, candidates, 125, 126, "Add window screenshot diff")
	if !ok {
		t.Fatal("resolveOCRActionableTarget returned no target")
	}
	if got.record.title != "Add window screenshot diff" {
		t.Fatalf("title = %q, want exact match target", got.record.title)
	}
	if note == "" {
		t.Fatal("expected selection note")
	}
}

func TestResolveOCRActionableTargetFallsBackToNearest(t *testing.T) {
	root := elementSnapshot{record: elementRecord{x: 100, y: 200, w: 400, h: 300}}
	candidates := []elementSnapshot{
		{
			record: elementRecord{
				role:       "AXButton",
				title:      "Nearby",
				enabled:    true,
				visible:    true,
				actionable: true,
				depth:      2,
				index:      1,
				x:          210,
				y:          300,
				w:          120,
				h:          40,
			},
		},
		{
			record: elementRecord{
				role:       "AXButton",
				title:      "Farther",
				enabled:    true,
				visible:    true,
				actionable: true,
				depth:      2,
				index:      2,
				x:          360,
				y:          300,
				w:          120,
				h:          40,
			},
		},
	}

	got, note, ok := resolveOCRActionableTarget(root, candidates, 235, 125, "unmatched text")
	if !ok {
		t.Fatal("resolveOCRActionableTarget returned no target")
	}
	if got.record.title != "Nearby" {
		t.Fatalf("title = %q, want nearest target", got.record.title)
	}
	if note == "" {
		t.Fatal("expected selection note")
	}
}

func TestSelectOCRMatchRejectsOutOfRangeIndex(t *testing.T) {
	results := []ocrResult{{Text: "One"}, {Text: "One"}}
	idx := 3
	if _, err := selectOCRMatch(results, "one", &idx); err == nil {
		t.Fatal("selectOCRMatch succeeded for out-of-range index")
	}
}
