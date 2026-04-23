package main

import (
	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
)

// primaryDisplayHeight returns the height of the primary display — the one
// whose bottom-left is the origin of AppKit's global screen coord space.
// NSScreen.mainScreen() returns the screen with keyboard focus, NOT the
// primary display; using it on multi-monitor setups (or when another app is
// key) produces wrong Y flips. [NSScreen screens][0] is the primary.
func primaryDisplayHeight() float64 {
	screens := appkit.GetNSScreenClass().Screens()
	if len(screens) == 0 {
		return appkit.GetNSScreenClass().MainScreen().Frame().Size.Height
	}
	return screens[0].Frame().Size.Height
}

// axToAppKitRect converts a rect in AX / CoreGraphics global display
// coordinates (top-left origin, Y grows down, origin at the top-left of the
// primary display) to AppKit screen coordinates (bottom-left origin, Y grows
// up, origin at the bottom-left of the primary display), suitable for
// NSWindow.setFrame: and NSWindow content rects.
//
// Sizes are unchanged; only the origin Y is flipped. X is identical in both
// systems because the primary-display origin is shared on the X axis.
func axToAppKitRect(axRect corefoundation.CGRect) corefoundation.CGRect {
	primaryHeight := primaryDisplayHeight()
	return corefoundation.CGRect{
		Origin: corefoundation.CGPoint{
			X: axRect.Origin.X,
			Y: primaryHeight - (axRect.Origin.Y + axRect.Size.Height),
		},
		Size: axRect.Size,
	}
}

// axPointToAppKit converts a single point from AX/CG top-left coords to
// AppKit bottom-left coords. Equivalent to [NSEvent mouseLocation] space.
func axPointToAppKit(p corefoundation.CGPoint) corefoundation.CGPoint {
	primaryHeight := primaryDisplayHeight()
	return corefoundation.CGPoint{X: p.X, Y: primaryHeight - p.Y}
}
