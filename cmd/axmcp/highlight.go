package main

import (
	"fmt"
	"time"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/axmcp/internal/ghostcursor"
)

const (
	highlightDuration     = 2 * time.Second
	highlightFadeDuration = 220 * time.Millisecond
	highlightFadeSteps    = 8
	highlightBoxPadding   = 4
	highlightMaxMatches   = 8
	highlightGlowPadding  = 8
	highlightInnerInset   = 3
)

func highlightCollectionBehavior() appkit.NSWindowCollectionBehavior {
	return appkit.NSWindowCollectionBehaviorCanJoinAllSpaces |
		appkit.NSWindowCollectionBehaviorTransient |
		appkit.NSWindowCollectionBehaviorIgnoresCycle |
		appkit.NSWindowCollectionBehaviorFullScreenAuxiliary
}

func runOnMain(work func()) {
	if foundation.GetThreadClass().CurrentThread().IsMainThread() {
		work()
		return
	}
	done := make(chan struct{})
	dispatch.MainQueue().Async(func() {
		defer close(done)
		work()
	})
	<-done
}

func uniqueOCRMatches(matches []ocrResult, limit int) []ocrResult {
	if limit <= 0 {
		limit = len(matches)
	}
	out := make([]ocrResult, 0, min(len(matches), limit))
	seen := make(map[[4]int]bool, len(matches))
	for _, match := range matches {
		if match.W <= 0 || match.H <= 0 {
			continue
		}
		key := [4]int{match.X, match.Y, match.W, match.H}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, match)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func highlightRectForMatch(match ocrResult, imgW, imgH int) corefoundation.CGRect {
	x0 := max(0, match.X-highlightBoxPadding)
	y0 := max(0, match.Y-highlightBoxPadding)
	x1 := min(imgW, match.X+match.W+highlightBoxPadding)
	y1 := min(imgH, match.Y+match.H+highlightBoxPadding)
	if x1 < x0 {
		x1 = x0
	}
	if y1 < y0 {
		y1 = y0
	}
	return corefoundation.CGRect{
		Origin: corefoundation.CGPoint{
			X: float64(x0),
			Y: float64(imgH - y1),
		},
		Size: corefoundation.CGSize{
			Width:  float64(x1 - x0),
			Height: float64(y1 - y0),
		},
	}
}

func expandCGRect(rect corefoundation.CGRect, padding float64) corefoundation.CGRect {
	if padding <= 0 {
		return rect
	}
	return corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: rect.Origin.X - padding, Y: rect.Origin.Y - padding},
		Size: corefoundation.CGSize{
			Width:  rect.Size.Width + 2*padding,
			Height: rect.Size.Height + 2*padding,
		},
	}
}

func insetCGRect(rect corefoundation.CGRect, padding float64) corefoundation.CGRect {
	if padding <= 0 {
		return rect
	}
	width := rect.Size.Width - 2*padding
	if width < 0 {
		width = 0
	}
	height := rect.Size.Height - 2*padding
	if height < 0 {
		height = 0
	}
	return corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: rect.Origin.X + padding, Y: rect.Origin.Y + padding},
		Size:   corefoundation.CGSize{Width: width, Height: height},
	}
}

func animateOverlayWindow(win appkit.NSWindow, duration time.Duration) {
	if duration <= 0 {
		duration = highlightDuration
	}
	fade := highlightFadeDuration
	if duration < 2*fade {
		fade = duration / 2
	}
	if fade <= 0 {
		runOnMain(func() {
			win.SetAlphaValue(1)
		})
		return
	}
	stepSleep := fade / highlightFadeSteps
	if stepSleep <= 0 {
		stepSleep = time.Millisecond
	}
	for i := 1; i <= highlightFadeSteps; i++ {
		alpha := float64(i) / float64(highlightFadeSteps)
		runOnMain(func() {
			win.SetAlphaValue(alpha)
		})
		time.Sleep(stepSleep)
	}
	hold := duration - (2 * fade)
	if hold > 0 {
		time.Sleep(hold)
	}
	for i := highlightFadeSteps - 1; i >= 0; i-- {
		alpha := float64(i) / float64(highlightFadeSteps)
		runOnMain(func() {
			win.SetAlphaValue(alpha)
		})
		time.Sleep(stepSleep)
	}
}

func highlightOCRMatches(capture *ocrCapture, matches []ocrResult, duration time.Duration) (int, error) {
	if capture == nil || capture.target == nil {
		return 0, fmt.Errorf("highlight target disappeared")
	}
	if capture.imgW <= 0 || capture.imgH <= 0 {
		return 0, fmt.Errorf("highlight target has invalid size")
	}
	matches = uniqueOCRMatches(matches, highlightMaxMatches)
	if len(matches) == 0 {
		return 0, fmt.Errorf("no visible OCR boxes to highlight")
	}

	targetFrame := capture.target.Frame()
	axRect := corefoundation.CGRect{
		Origin: corefoundation.CGPoint{
			X: targetFrame.Origin.X,
			Y: targetFrame.Origin.Y,
		},
		Size: corefoundation.CGSize{
			Width:  float64(capture.imgW),
			Height: float64(capture.imgH),
		},
	}
	// capture.target.Frame() is in AX / CoreGraphics global coords
	// (top-left origin), but NSWindow content rects are in AppKit
	// screen coords (bottom-left origin). Flip Y against the primary
	// display height so the overlay lands on the element, not mirrored
	// across the screen.
	frame := axToAppKitRect(axRect)

	var win appkit.NSWindow
	runOnMain(func() {
		win = appkit.NewWindowWithContentRectStyleMaskBackingDefer(
			frame,
			appkit.NSWindowStyleMaskBorderless,
			appkit.NSBackingStoreBuffered,
			false,
		)
		win.SetOpaque(false)
		win.SetBackgroundColor(appkit.NewColorWithSRGBRedGreenBlueAlpha(0, 0, 0, 0))
		win.SetHasShadow(false)
		win.SetIgnoresMouseEvents(true)
		win.SetReleasedWhenClosed(false)
		win.SetLevel(appkit.StatusWindowLevel)
		win.SetCollectionBehavior(highlightCollectionBehavior())
		win.SetSharingType(ghostcursor.OverlaySharingType())

		content := appkit.NSViewFromID(win.ContentView().GetID())
		glowFill := appkit.NewColorWithSRGBRedGreenBlueAlpha(1.0, 0.68, 0.18, 0.16)
		border := appkit.NewColorWithSRGBRedGreenBlueAlpha(1.0, 0.56, 0.10, 0.99)
		fill := appkit.NewColorWithSRGBRedGreenBlueAlpha(1.0, 0.50, 0.08, 0.18)
		innerBorder := appkit.NewColorWithSRGBRedGreenBlueAlpha(1.0, 0.98, 0.92, 0.86)
		for _, match := range matches {
			mainRect := highlightRectForMatch(match, capture.imgW, capture.imgH)

			glow := appkit.NewBoxWithFrame(expandCGRect(mainRect, highlightGlowPadding))
			glow.SetBoxType(appkit.NSBoxCustom)
			glow.SetTitlePosition(appkit.NSNoTitle)
			glow.SetBorderWidth(0)
			glow.SetCornerRadius(14)
			glow.SetFillColor(glowFill)
			content.AddSubview(glow)

			box := appkit.NewBoxWithFrame(mainRect)
			box.SetBoxType(appkit.NSBoxCustom)
			box.SetTitlePosition(appkit.NSNoTitle)
			box.SetBorderColor(border)
			box.SetBorderWidth(4)
			box.SetCornerRadius(10)
			box.SetFillColor(fill)
			content.AddSubview(box)

			inner := appkit.NewBoxWithFrame(insetCGRect(mainRect, highlightInnerInset))
			inner.SetBoxType(appkit.NSBoxCustom)
			inner.SetTitlePosition(appkit.NSNoTitle)
			inner.SetBorderColor(innerBorder)
			inner.SetBorderWidth(1.5)
			inner.SetCornerRadius(7)
			inner.SetFillColor(appkit.NewColorWithSRGBRedGreenBlueAlpha(1, 1, 1, 0))
			content.AddSubview(inner)
		}

		win.SetAlphaValue(0)
		win.OrderFrontRegardless()
	})

	animateOverlayWindow(win, duration)
	runOnMain(func() {
		win.OrderOut(nil)
		win.Close()
	})
	return len(matches), nil
}
