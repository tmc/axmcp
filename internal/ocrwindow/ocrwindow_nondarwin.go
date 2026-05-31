//go:build !darwin

// Package ocrwindow captures a macOS application window and runs Apple Vision
// OCR on Darwin.
package ocrwindow

import "fmt"

// Window describes a single on-screen window owned by an application.
type Window struct {
	ID        uint32
	Title     string
	OwnerName string
	OwnerPID  int64
	X, Y      float64
	W, H      float64
}

// Hit is one piece of recognized text with its bounding box in pixel
// coordinates relative to the captured PNG (origin top-left).
type Hit struct {
	Text       string
	Confidence float32
	X, Y, W, H int
}

func unsupported() error {
	return fmt.Errorf("ocr window capture is not available on this platform")
}

// FindWindow reports that macOS window capture is unavailable.
func FindWindow(string) (Window, error) {
	return Window{}, unsupported()
}

// ListWindows reports that macOS window capture is unavailable.
func ListWindows(string) ([]Window, error) {
	return nil, unsupported()
}

// FindWindowID reports that macOS window capture is unavailable.
func FindWindowID(string, uint32) (Window, error) {
	return Window{}, unsupported()
}

// Capture reports that macOS window capture is unavailable.
func Capture(Window) ([]byte, int, int, error) {
	return nil, 0, 0, unsupported()
}

// Recognize reports that Apple Vision OCR is unavailable.
func Recognize([]byte, int, int) ([]Hit, error) {
	return nil, unsupported()
}
