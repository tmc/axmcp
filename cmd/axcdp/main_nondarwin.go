//go:build !darwin

package main

import (
	"fmt"
	"os"
	"runtime"
)

func main() {
	fmt.Fprintf(os.Stderr, "axcdp: native macOS Accessibility CDP automation is not supported on %s\n", runtime.GOOS)
	os.Exit(1)
}
