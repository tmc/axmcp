package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseFrameName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  frameReport
		ok    bool
	}{
		{
			name:  "phase frame",
			input: "cycle-001-phase-start.png",
			want:  frameReport{Cycle: 1, Phase: "phase-start"},
			ok:    true,
		},
		{
			name:  "step frame",
			input: "cycle-002-step-03-idle-end-plus-minus.png",
			want:  frameReport{Cycle: 2, Step: 3, Phase: "idle-end", Label: "+/-"},
			ok:    true,
		},
		{
			name:  "bad name",
			input: "contact-sheet.png",
			ok:    false,
		},
	}
	for _, tt := range tests {
		got, ok := parseFrameName(tt.input)
		if ok != tt.ok {
			t.Fatalf("%s: parseFrameName(%q) ok=%v, want %v", tt.name, tt.input, ok, tt.ok)
		}
		if ok && !reflect.DeepEqual(got, tt.want) {
			t.Fatalf("%s: parseFrameName(%q) = %+v, want %+v", tt.name, tt.input, got, tt.want)
		}
	}
}

func TestCompareFrames(t *testing.T) {
	frames := []frameReport{
		{Cycle: 1, Step: 2, Phase: "idle-end", Label: "7"},
		{Cycle: 1, Phase: "phase-start"},
		{Cycle: 1, Step: 1, Phase: "before-click", Label: "3"},
		{Cycle: 1, Step: 1, Phase: "idle-mid", Label: "3"},
		{Cycle: 1, Phase: "phase-end"},
	}
	want := []frameReport{
		{Cycle: 1, Phase: "phase-start"},
		{Cycle: 1, Step: 1, Phase: "before-click", Label: "3"},
		{Cycle: 1, Step: 1, Phase: "idle-mid", Label: "3"},
		{Cycle: 1, Step: 2, Phase: "idle-end", Label: "7"},
		{Cycle: 1, Phase: "phase-end"},
	}
	for i := 0; i < len(frames); i++ {
		for j := i + 1; j < len(frames); j++ {
			if compareFrames(frames[i], frames[j]) > 0 {
				frames[i], frames[j] = frames[j], frames[i]
			}
		}
	}
	if !reflect.DeepEqual(frames, want) {
		t.Fatalf("compareFrames ordering = %+v, want %+v", frames, want)
	}
}

func TestGroupFrames(t *testing.T) {
	frames := []frameReport{
		{File: "cycle-001-phase-start.png", Cycle: 1, Phase: "phase-start"},
		{File: "cycle-001-step-01-before-click-3.png", Cycle: 1, Step: 1, Phase: "before-click", Label: "3"},
		{File: "cycle-001-step-01-idle-end-3.png", Cycle: 1, Step: 1, Phase: "idle-end", Label: "3"},
		{File: "cycle-001-phase-end.png", Cycle: 1, Phase: "phase-end"},
	}
	grouped := groupFrames(frames)
	got := grouped[1]
	if len(got.PhaseFrames) != 2 {
		t.Fatalf("phase frame count = %d, want 2", len(got.PhaseFrames))
	}
	if len(got.Steps) != 1 {
		t.Fatalf("step count = %d, want 1", len(got.Steps))
	}
	if got.Steps[0].Label != "3" {
		t.Fatalf("step label = %q, want 3", got.Steps[0].Label)
	}
}

func TestFallbackBucketValue(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "01-0-65", want: "0.65"},
		{input: "02-1-45", want: "1.45"},
		{input: "01-1400ms", want: "1400ms"},
	}
	for _, tt := range tests {
		if got := fallbackBucketValue(tt.input); got != tt.want {
			t.Fatalf("fallbackBucketValue(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestMarkdownPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "review.json", want: "./review.json"},
		{input: "cursor-scale/01-0-65/contact-sheet.png", want: "./cursor-scale/01-0-65/contact-sheet.png"},
		{input: "./already-relative.png", want: "./already-relative.png"},
		{input: "/tmp/absolute.png", want: "/tmp/absolute.png"},
		{input: "https://example.com/a.png", want: "https://example.com/a.png"},
	}
	for _, tt := range tests {
		if got := markdownPath(tt.input); got != tt.want {
			t.Fatalf("markdownPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestRenderMarkdownUsesMarkdownImages(t *testing.T) {
	rep := report{
		Title:       "Test",
		Root:        "/tmp/root",
		GeneratedAt: "now",
		Dimensions: []dimensionReport{{
			Name: "cursor-scale",
			Buckets: []bucketReport{{
				Name:         "01-1-45",
				Value:        "1.45",
				Dir:          "cursor-scale/01-1-45",
				ContactSheet: "cursor-scale/01-1-45/contact-sheet.png",
				Frames: []frameReport{
					{File: "cursor-scale/01-1-45/cycle-001-phase-start.png", Cycle: 1, Phase: "phase-start"},
					{File: "cursor-scale/01-1-45/cycle-001-phase-end.png", Cycle: 1, Phase: "phase-end"},
					{File: "cursor-scale/01-1-45/cycle-001-step-01-before-click-3.png", Cycle: 1, Step: 1, Phase: "before-click", Label: "3"},
					{File: "cursor-scale/01-1-45/cycle-001-step-01-idle-end-3.png", Cycle: 1, Step: 1, Phase: "idle-end", Label: "3"},
				},
			}},
		}},
	}
	md := renderMarkdown(rep)
	if strings.Contains(md, "<img") {
		t.Fatalf("renderMarkdown() unexpectedly contains raw img tags:\n%s", md)
	}
	if !strings.Contains(md, "![cycle 1 phase-start](./cursor-scale/01-1-45/cycle-001-phase-start.png)") {
		t.Fatalf("renderMarkdown() missing markdown image for phase frame:\n%s", md)
	}
	if !strings.Contains(md, "![cycle 1 step 1 before-click 3](./cursor-scale/01-1-45/cycle-001-step-01-before-click-3.png)") {
		t.Fatalf("renderMarkdown() missing markdown image for step frame:\n%s", md)
	}
}

func TestRenderMarkdownEmbedsVideo(t *testing.T) {
	rep := report{
		Title:       "Test",
		Root:        "/tmp/root",
		GeneratedAt: "now",
		Dimensions: []dimensionReport{{
			Name: "cursor-scale",
			Buckets: []bucketReport{{
				Name:         "01-1-45",
				Value:        "1.45",
				Dir:          "cursor-scale/01-1-45",
				ContactSheet: "cursor-scale/01-1-45/contact-sheet.png",
				Video:        "cursor-scale/01-1-45/run.mp4",
			}},
		}},
	}
	md := renderMarkdown(rep)
	if !strings.Contains(md, "![run.mp4](./cursor-scale/01-1-45/run.mp4)") {
		t.Fatalf("renderMarkdown() missing markdown video embed:\n%s", md)
	}
	if strings.Contains(md, "<video") {
		t.Fatalf("renderMarkdown() should not emit raw video HTML:\n%s", md)
	}
}
