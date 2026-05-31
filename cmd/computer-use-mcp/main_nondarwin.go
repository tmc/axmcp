//go:build !darwin

package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/tmc/axmcp/internal/computeruse"
)

func main() {
	fmt.Fprintf(os.Stderr, "computer-use-mcp: %v\n", computeruse.PlatformUnsupported("native desktop automation on "+runtime.GOOS))
	report := computeruse.PlatformStatus()
	fmt.Fprintf(os.Stderr, "platform: %s\nbackend: %s\n", report.OS, report.Backend)
	if report.Message != "" {
		fmt.Fprintf(os.Stderr, "status: %s\n", report.Message)
	}
	for _, cap := range report.Capabilities {
		status := "unavailable"
		if cap.Available {
			status = "available"
		}
		if cap.Message == "" {
			fmt.Fprintf(os.Stderr, "- %s: %s\n", cap.Name, status)
			continue
		}
		fmt.Fprintf(os.Stderr, "- %s: %s: %s\n", cap.Name, status, cap.Message)
	}
	os.Exit(1)
}
