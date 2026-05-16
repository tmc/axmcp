# ScreenCaptureKit and TCC

This note records the screenshot capture paths used by axmcp and how they
interact with Screen Recording authorization.

## Capture Options

### ScreenCaptureKit

`captureWindow` now tries ScreenCaptureKit first for app-backed CLI and MCP
window screenshots. The implementation uses:

- `SCShareableContent.getShareableContent`
- `SCContentFilter(desktopIndependentWindow:)`
- `SCScreenshotManager.captureImage`

This is the preferred path because `CGWindowListCreateImage` is deprecated
and because desktop-independent window filters are the right API for windows
that are not simply a rectangle on the active display.

TCC behavior:

- Requires Screen Recording permission for the axmcp app identity.
- Runs through ScreenCaptureKit and the AppKit main run loop.
- May trigger AppKit termination callbacks during the permission flow, so
  axmcp keeps the AppKit run loop alive and installs a delegate that cancels
  automatic termination.
- Requires the app-backed process shape; the pre-AppKit direct fast path does
  not use SCK.

### CGWindowListCreateImage

The legacy CoreGraphics window capture remains as a fallback. It runs
synchronously on the calling goroutine and is still used by the pre-AppKit
direct screenshot fast path.

TCC behavior:

- Requires Screen Recording permission before returning useful pixels.
- Can be attempted before the AppKit run loop is started.
- Does not need the ScreenCaptureKit main-thread dispatch/termination guard.
- Is deprecated on modern macOS and should not be the primary path for new
  app-backed captures.

### screencapture -R

The `screencapture` command remains a rectangle fallback for padded element
captures and older helper paths.

TCC behavior:

- Requires Screen Recording permission.
- Prompts/authorization are attributed to the calling app identity and helper
  process behavior can vary by launch context.
- Captures a display rectangle, not a desktop-independent window, so it is a
  weaker fit for off-Space/minimized/window-specific capture semantics.

## Current Policy

- App-backed axmcp full-window captures: try ScreenCaptureKit first, then
  fall back to `CGWindowListCreateImage`.
- Direct pre-AppKit screenshot fast path: use `CGWindowListCreateImage`.
- Element/padded-rect captures: keep AX or `screencapture -R` fallback until a
  rectangle-oriented SCK helper is added.

## Validation

Relevant local gates:

- `go test ./cmd/axmcp`
- `go test ./...`

Manual smoke recipes:

- `axmcp screenshot <app> -o /tmp/axmcp-sck.png`
- `axmcp screenshot <app> --contains <text> -o /tmp/axmcp-element.png`

When Screen Recording is not granted, compare the visible flow for:

- app-backed SCK capture: axmcp permission window plus macOS Screen Recording
  prompt/settings guidance
- legacy direct fast path: no AppKit permission window; capture fails or
  falls through to the app-backed flow
- `screencapture -R`: helper-style prompt behavior tied to the current launch
  context
