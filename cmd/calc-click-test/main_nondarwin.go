//go:build !darwin

package main

import (
	"fmt"
	"os"
	"runtime"
)

func main() {
	fmt.Fprintf(os.Stderr, "calc-click-test: macOS Calculator click test is not supported on %s\n", runtime.GOOS)
	os.Exit(1)
}
