//go:build darwin

package main

import (
	"testing"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
)

func TestHighlightCollectionBehaviorAvoidsConflictingFlags(t *testing.T) {
	got := highlightCollectionBehavior()
	if got&appkit.NSWindowCollectionBehaviorCanJoinAllSpaces != 0 &&
		got&appkit.NSWindowCollectionBehaviorMoveToActiveSpace != 0 {
		t.Fatalf("highlightCollectionBehavior = %v, includes conflicting flags", got)
	}
}

func TestUniqueOCRMatches(t *testing.T) {
	got := uniqueOCRMatches([]ocrResult{
		{Text: "Extra High", X: 10, Y: 20, W: 30, H: 10},
		{Text: "Extra High v", X: 10, Y: 20, W: 30, H: 10},
		{Text: "Other", X: 60, Y: 20, W: 20, H: 12},
	}, 8)
	if len(got) != 2 {
		t.Fatalf("uniqueOCRMatches len = %d, want 2", len(got))
	}
	if got[0].Text != "Extra High" {
		t.Fatalf("first unique match = %q, want %q", got[0].Text, "Extra High")
	}
}

func TestHighlightRectForMatch(t *testing.T) {
	got := highlightRectForMatch(ocrResult{X: 10, Y: 20, W: 30, H: 10}, 100, 80)
	want := corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 6, Y: 46},
		Size:   corefoundation.CGSize{Width: 38, Height: 18},
	}
	if got != want {
		t.Fatalf("highlightRectForMatch = %#v, want %#v", got, want)
	}
}

func TestHighlightRectForMatchClipsToBounds(t *testing.T) {
	got := highlightRectForMatch(ocrResult{X: 1, Y: 2, W: 5, H: 6}, 20, 20)
	want := corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 0, Y: 8},
		Size:   corefoundation.CGSize{Width: 10, Height: 12},
	}
	if got != want {
		t.Fatalf("highlightRectForMatch clipped = %#v, want %#v", got, want)
	}
}
