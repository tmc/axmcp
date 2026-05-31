# Computer Use Portability

This package is currently a Darwin implementation. The exported model types are
portable, but the command and the runtime packages that build state, send input,
and watch for interventions call macOS APIs directly.

## Public Evidence

The public OpenAI CUA sample app
<https://github.com/openai/openai-cua-sample-app> is browser-oriented. Its
native mode exposes the Responses API `computer` tool and maps model actions to
Playwright mouse, keyboard, wait, and screenshot operations. The sample
documents Linux setup through Playwright's OS dependency installer, which is
useful evidence for a cross-platform browser backend, not for this repository's
native desktop path. No public OpenAI repository found in this audit exposes a
native Windows desktop backend to clone or port.

The public Cua driver repository <https://github.com/trycua/cua> documents a
cross-platform native direction: macOS through Accessibility APIs, Windows
through UI Automation, and Linux through AT-SPI with X11 or Wayland-specific
input fallbacks. Its Linux support is described as beta or pre-release in public
docs. The same docs call out platform risks that matter here: Windows screenshot
capture can miss GPU-composited content, Windows services have session
isolation, and Linux input depends on the display server and permissions.

## Current Boundary

These repo packages are not build-tagged as Darwin-only, but they import Darwin
or Apple-specific APIs:

- `internal/computeruse/appstate`: Accessibility, CoreFoundation, axpump,
  ghostcursor, and macosapp.
- `internal/computeruse/input`: Accessibility, CoreFoundation, CoreGraphics,
  ghostcursor, and Skylight input.
- `internal/computeruse/intervention`: CoreFoundation, CoreGraphics, and kernel
  event APIs.

As a result, `GOOS=linux go test ./internal/computeruse/... ./cmd/computer-use-mcp`
and the equivalent Windows command fail before package tests run because the
Apple dependency chain reaches `github.com/ebitengine/purego/objc`, whose files
are excluded on those targets.

## Small Implementation Slice

The next small code change should split the platform operations behind narrow
interfaces and keep the Darwin implementation as the first backend:

- state building: list apps, resolve an app, read accessibility state, and take
  screenshots;
- input: click, drag, scroll, type text, and press keys by element or
  coordinate;
- intervention monitoring: start, stop, and report user-intervention state.

After that split, Darwin files should carry `//go:build darwin` and Linux and
Windows should get stub implementations that return one shared unsupported
platform error. That would let cross-platform builds compile while preserving a
clear runtime failure until UIA and AT-SPI backends are implemented.

## Verification Targets

The compatibility gate for the current implementation is:

```sh
GOOS=darwin go test ./internal/computeruse/... ./cmd/computer-use-mcp
```

After the stub layer exists, these should also compile and run package tests:

```sh
GOOS=linux go test ./internal/computeruse/... ./cmd/computer-use-mcp
GOOS=windows go test ./internal/computeruse/... ./cmd/computer-use-mcp
```
