# Computer Use Portability

This package has a Darwin implementation plus non-Darwin unsupported stubs. The
exported model types and helper packages are portable; live app state, native
input, and physical-intervention monitoring still require platform backends.

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

These repo packages are Darwin-only implementations because they import Darwin
or Apple-specific APIs:

- `internal/computeruse/appstate`: Accessibility, CoreFoundation, axpump,
  ghostcursor, and macosapp.
- `internal/computeruse/input`: Accessibility, CoreFoundation, CoreGraphics,
  ghostcursor, and Skylight input.
- `internal/computeruse/intervention`: CoreFoundation, CoreGraphics, and kernel
  event APIs.

Non-Darwin builds now get unsupported stubs for these packages and for
`cmd/computer-use-mcp`, so the package set can compile without pulling in the
Apple dependency chain. Runtime native automation remains unavailable until
Windows and Linux backends replace those stubs.

## Implementation Slice

Platform operations are split behind Darwin implementations and non-Darwin
unsupported stubs:

- state building: list apps, resolve an app, read accessibility state, and take
  screenshots;
- input: click, drag, scroll, type text, and press keys by element or
  coordinate;
- intervention monitoring: start, stop, and report user-intervention state.

Darwin files carry `//go:build darwin`, and non-Darwin stubs return one shared
unsupported platform error. Windows and Linux native automation backends are not
implemented. The next slice is to replace those stubs with real backends one
subsystem at a time.

## Verification Targets

The compatibility gate for the current implementation is:

```sh
GOOS=darwin go test ./internal/computeruse/... ./cmd/computer-use-mcp
```

The stub layer should keep these compile-oriented checks passing:

```sh
GOOS=linux go test -exec=true ./internal/computeruse/... ./cmd/computer-use-mcp
GOOS=windows go test -exec=true ./internal/computeruse/... ./cmd/computer-use-mcp
```
