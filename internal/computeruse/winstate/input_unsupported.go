//go:build !windows

package winstate

import (
	"context"

	"github.com/tmc/axmcp/internal/computeruse"
)

func sendWindowInput(context.Context, inputAction) error {
	return computeruse.PlatformUnsupported("windows input")
}
