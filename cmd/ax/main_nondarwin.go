//go:build !darwin

package main

import (
	"fmt"
	"os"
	"runtime"
)

func main() {
	fmt.Fprintf(os.Stderr, "ax: macOS Accessibility inspection is not supported on %s\n", runtime.GOOS)
	os.Exit(1)
}
