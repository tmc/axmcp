//go:build !windows

package winstate

import (
	"context"
	"fmt"

	"github.com/tmc/axmcp/internal/computeruse"
)

func readAutomationTree(ctx context.Context, win Window) (AutomationNode, error) {
	if err := ctx.Err(); err != nil {
		return AutomationNode{}, err
	}
	if win.Handle == 0 {
		return AutomationNode{}, fmt.Errorf("missing window handle")
	}
	return AutomationNode{}, computeruse.PlatformUnsupported("read UI Automation tree")
}
