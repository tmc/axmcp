//go:build darwin

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

func actionScreenshotArtifactRunName(appName, windowTitle, action string, now time.Time) string {
	parts := []string{
		now.UTC().Format("20060102T150405Z"),
		sanitizeArtifactComponent(action),
		sanitizeArtifactComponent(appName),
		sanitizeArtifactComponent(windowTitle),
	}
	out := strings.Join(filterEmpty(parts), "-")
	return strings.Trim(out, "-")
}

func sanitizeArtifactComponent(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}

	var b strings.Builder
	lastDash := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}

	out := strings.Trim(b.String(), "-")
	if out == "" {
		return ""
	}
	if len(out) > 48 {
		out = strings.Trim(out[:48], "-")
	}
	return out
}

func filterEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func artifactPaths(baseDir, appName, windowTitle, action string, now time.Time) (string, string, string, string) {
	runDir := filepath.Join(baseDir, actionScreenshotArtifactRunName(appName, windowTitle, action, now))
	return runDir,
		filepath.Join(runDir, "before.png"),
		filepath.Join(runDir, "after.png"),
		filepath.Join(runDir, "diff.png")
}

func fileBaseNames(paths []string) []string {
	out := make([]string, len(paths))
	for i, path := range paths {
		out[i] = filepath.Base(path)
	}
	return out
}

func writePNGArtifact(path string, png []byte) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("artifact_path is empty")
	}
	if len(png) == 0 {
		return fmt.Errorf("screenshot is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create artifact directory: %w", err)
	}
	if err := os.WriteFile(path, png, 0o644); err != nil {
		return fmt.Errorf("write artifact %s: %w", path, err)
	}
	return nil
}
