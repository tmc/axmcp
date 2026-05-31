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
	os.Exit(1)
}
