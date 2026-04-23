package main

import (
	"time"

	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/ghostcursor"
)

func beginTypingCursor(el *axuiautomation.Element) func() {
	if el == nil {
		return func() {}
	}
	frame := el.Frame()
	pos := ghostcursor.TypingPositionForFrame(
		frame.Origin.X,
		frame.Origin.Y,
		frame.Size.Width,
		frame.Size.Height,
	)
	_ = ghostcursor.Default().Show(pos, ghostcursor.ActivityTyping, 0)
	noteCLIVisualFeedback()
	return func() {
		_ = ghostcursor.Default().Show(pos, ghostcursor.ActivityIdle, 220*time.Millisecond)
	}
}
