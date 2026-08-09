package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tmc/axmcp/internal/focusless"
)

func cliFocusState() *cobra.Command {
	return &cobra.Command{
		Use:   "focus-state [app]",
		Short: "Report window server focus and Space state",
		Long: `Report window server focus and Space state.

Without an app, prints the frontmost process and the active Space. With one,
also lists that app's windows with the Space verdict used to decide whether
input will reach them: a window marked off-Space will not receive synthetic
clicks or keystrokes, because those are delivered by screen point against
whatever is drawn on the active Space.

Reads only. Nothing is focused, raised or activated.`,
		Example: `  axmcp focus-state
  axmcp focus-state com.apple.finder`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := focusless.Capture()
			if err != nil {
				return fmt.Errorf("focus state: %w", err)
			}
			fmt.Printf("active space %d\nfront pid    %d (window server)\nfront pid    %d (NSWorkspace)\n",
				state.ActiveSpace, state.FrontPID, state.WorkspacePID)
			if len(args) == 0 {
				return nil
			}

			windows, err := listAppWindows(args[0])
			if err != nil {
				return fmt.Errorf("focus state: %w", err)
			}
			focused := focusless.FocusedWindow(int(windows[0].OwnerPID))
			fmt.Printf("focused wid  %d\n", focused)
			for _, w := range windows {
				mark := "on-space"
				if off, err := focusless.IsOffSpace(w.WindowID); err != nil {
					mark = "unknown"
				} else if off {
					mark = "OFF-SPACE"
				}
				star := " "
				if w.WindowID == focused {
					star = "*"
				}
				fmt.Printf("%s wid=%-8d %-9s %s\n", star, w.WindowID, mark, w.Title)
			}
			return nil
		},
	}
}
