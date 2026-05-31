package main

import (
	"os"
	"testing"
)

func TestResourceContextFromCWD(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	ctx := resourceContextFromCWD()
	if ctx.ProjectRoot != wd {
		t.Fatalf("ProjectRoot = %q, want %q", ctx.ProjectRoot, wd)
	}
}
