package xcodebuild

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

type BuildOptions struct {
	Project       string // Path to .xcodeproj
	Workspace     string // Path to .xcworkspace (mutually exclusive with Project)
	Scheme        string
	Configuration string // Debug, Release
	Destination   string // e.g., "platform=iOS Simulator,name=iPhone 15"
	DerivedData   string // Custom derived data path
}

type BuildResult struct {
	Success  bool
	Duration time.Duration
	Output   string
	Errors   []string
	Warnings []string
}

// Build runs xcodebuild build.
func Build(ctx context.Context, opts BuildOptions) (*BuildResult, error) {
	return runXcodebuild(ctx, "build", opts)
}

// Test runs xcodebuild test.
func Test(ctx context.Context, opts BuildOptions) (*BuildResult, error) {
	return runXcodebuild(ctx, "test", opts)
}

// Clean runs xcodebuild clean.
func Clean(ctx context.Context, opts BuildOptions) error {
	_, err := runXcodebuild(ctx, "clean", opts)
	return err
}

func runXcodebuild(ctx context.Context, action string, opts BuildOptions) (*BuildResult, error) {
	start := time.Now()
	args := []string{action}

	if opts.Workspace != "" {
		args = append(args, "-workspace", opts.Workspace)
	} else if opts.Project != "" {
		args = append(args, "-project", opts.Project)
	}

	if opts.Scheme != "" {
		args = append(args, "-scheme", opts.Scheme)
	}
	if opts.Configuration != "" {
		args = append(args, "-configuration", opts.Configuration)
	}
	if opts.Destination != "" {
		args = append(args, "-destination", opts.Destination)
	}
	if opts.DerivedData != "" {
		args = append(args, "-derivedDataPath", opts.DerivedData)
	}

	cmd := exec.CommandContext(ctx, "xcodebuild", args...)
	// Capture combined output for parsing
	out, err := cmd.CombinedOutput()
	outputStr := string(out)

	result := &BuildResult{
		Duration: time.Since(start),
		Output:   outputStr,
		Success:  err == nil,
		Errors:   []string{},
		Warnings: []string{},
	}

	// Basic parsing of errors and warnings from output
	// This is fragile and simplistic; purely regex-based or line prefix checking
	lines := strings.Split(outputStr, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(strings.ToLower(trimmed), "error:") {
			result.Errors = append(result.Errors, trimmed)
		} else if strings.Contains(strings.ToLower(trimmed), "warning:") {
			result.Warnings = append(result.Warnings, trimmed)
		} else if strings.Contains(trimmed, "** BUILD FAILED **") || strings.Contains(trimmed, "** TEST FAILED **") {
			result.Success = false
		}
	}

	if err != nil && result.Success {
		// If exec returns error but we didn't catch it in parsing (unlikely but possible)
		result.Success = false
	}

	if !result.Success && len(result.Errors) == 0 {
		// Fallback if we failed but didn't parse specific errors
		result.Errors = append(result.Errors, "Build failed with unknown error (check output)")
	}

	return result, nil // Return result even on failure, as it contains the error details
}
