package main

import (
	"fmt"
	"strings"

	"github.com/tmc/axmcp/internal/computeruse"
)

type captureMode string

const (
	captureModeSOM    captureMode = "som"
	captureModeAX     captureMode = "ax"
	captureModeVision captureMode = "vision"
)

func parseCaptureMode(raw string) (captureMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(captureModeSOM):
		return captureModeSOM, nil
	case string(captureModeAX):
		return captureModeAX, nil
	case string(captureModeVision):
		return captureModeVision, nil
	default:
		return "", fmt.Errorf("invalid capture_mode %q; use som, ax, or vision", raw)
	}
}

func appStateResponse(state computeruse.AppState, mode captureMode, omitScreenshot bool) computeruse.AppState {
	switch mode {
	case "", captureModeSOM:
	case captureModeAX:
		omitScreenshot = true
	case captureModeVision:
		state.Tree = nil
	}
	if omitScreenshot {
		state.ScreenshotPNGBase64 = ""
	}
	return state
}
