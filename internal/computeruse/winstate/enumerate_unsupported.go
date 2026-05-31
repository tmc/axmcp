//go:build !windows

package winstate

import (
	"context"

	"github.com/tmc/axmcp/internal/computeruse"
)

func enumerateWindows(context.Context) ([]Window, error) {
	return nil, computeruse.PlatformUnsupported("enumerate windows")
}
