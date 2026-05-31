//go:build !windows

package winstate

import (
	"context"

	"github.com/tmc/axmcp/internal/computeruse"
)

func enumerateWindows(context.Context) ([]Window, error) {
	return nil, computeruse.PlatformUnsupported("enumerate windows")
}

func captureWindowPNG(context.Context, Window) ([]byte, error) {
	return nil, computeruse.PlatformUnsupported("capture window screenshot")
}
