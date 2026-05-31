//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestActionScreenshotArtifactRunName(t *testing.T) {
	now := time.Date(2026, time.March, 29, 15, 24, 5, 0, time.UTC)
	got := actionScreenshotArtifactRunName("Surface Workbench", "Selection Driven Workspace", "Click", now)
	want := "20260329T152405Z-click-surface-workbench-selection-driven-workspace"
	if got != want {
		t.Fatalf("actionScreenshotArtifactRunName = %q, want %q", got, want)
	}
}

func TestWriteActionScreenshotArtifactsAt(t *testing.T) {
	baseDir := t.TempDir()
	now := time.Date(2026, time.March, 29, 15, 24, 5, 0, time.UTC)
	runDir, written, err := writeActionScreenshotArtifactsAt(baseDir, "Surface Workbench", "Selection Driven Workspace", "Hover", now, []byte("before"), []byte("after"), nil)
	if err != nil {
		t.Fatalf("writeActionScreenshotArtifactsAt: %v", err)
	}

	wantDir := filepath.Join(baseDir, "20260329T152405Z-hover-surface-workbench-selection-driven-workspace")
	if runDir != wantDir {
		t.Fatalf("runDir = %q, want %q", runDir, wantDir)
	}
	if len(written) != 2 {
		t.Fatalf("written len = %d, want 2", len(written))
	}
	for _, name := range []string{"before.png", "after.png"} {
		path := filepath.Join(runDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(data) != map[string]string{"before.png": "before", "after.png": "after"}[name] {
			t.Fatalf("%s content = %q", name, string(data))
		}
	}
	if _, err := os.Stat(filepath.Join(runDir, "diff.png")); !os.IsNotExist(err) {
		t.Fatalf("diff.png exists = %v, want not exists", err)
	}
}

func TestWritePNGArtifact(t *testing.T) {
	baseDir := t.TempDir()
	path := filepath.Join(baseDir, "captures", "window.png")
	if err := writePNGArtifact(path, []byte("png")); err != nil {
		t.Fatalf("writePNGArtifact: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "png" {
		t.Fatalf("content = %q, want %q", string(data), "png")
	}
}
