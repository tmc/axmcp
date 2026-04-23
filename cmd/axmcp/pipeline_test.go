package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParsePipelineScreenshotArgs(t *testing.T) {
	got, err := parsePipelineScreenshotArgs([]string{"--out", "/tmp/out.png", "--padding", "12"})
	if err != nil {
		t.Fatalf("parsePipelineScreenshotArgs: %v", err)
	}
	if got.outPath != "/tmp/out.png" {
		t.Fatalf("outPath = %q, want %q", got.outPath, "/tmp/out.png")
	}
	if got.padding != 12 {
		t.Fatalf("padding = %d, want 12", got.padding)
	}
}

func TestParsePipelineScreenshotArgsRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing out", args: []string{"--out"}},
		{name: "missing padding", args: []string{"--padding"}},
		{name: "bad padding", args: []string{"--padding", "nope"}},
		{name: "negative padding", args: []string{"--padding", "-1"}},
		{name: "unknown flag", args: []string{"--what"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parsePipelineScreenshotArgs(tt.args); err == nil {
				t.Fatalf("parsePipelineScreenshotArgs(%v) = nil, want error", tt.args)
			}
		})
	}
}

func TestPipelineScreenshotPathUsesProvidedPath(t *testing.T) {
	got, err := pipelineScreenshotPath("/tmp/example.png")
	if err != nil {
		t.Fatalf("pipelineScreenshotPath(explicit): %v", err)
	}
	if got != "/tmp/example.png" {
		t.Fatalf("path = %q, want %q", got, "/tmp/example.png")
	}
}

func TestPipelineScreenshotPathCreatesTempPNG(t *testing.T) {
	got, err := pipelineScreenshotPath("")
	if err != nil {
		t.Fatalf("pipelineScreenshotPath(temp): %v", err)
	}
	defer os.Remove(got)
	if filepath.Ext(got) != ".png" {
		t.Fatalf("path = %q, want .png suffix", got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("Stat(%q): %v", got, err)
	}
}
