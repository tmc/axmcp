# Computer Use Portability

This package has a Darwin implementation plus non-Darwin unsupported stubs. The
exported model types and helper packages are portable; live app state, native
input, and physical-intervention monitoring still require platform backends.

## Public Evidence

Refreshed on 2026-05-31:

The public OpenAI CUA sample app
<https://github.com/openai/openai-cua-sample-app> at main
`3751c8baa6376c0bbf6cceea2cdc0c0b42996e03` is browser-oriented. Its native
mode exposes the Responses API `computer` tool and maps model actions to
Playwright mouse, keyboard, wait, and screenshot operations in
`packages/runner-core/src/responses-loop.ts`. Screenshots come from
`page.screenshot` through `packages/browser-runtime/src/index.ts`, and the
state/replay schema is browser-session state. The sample documents Linux setup
through Playwright's OS dependency installer, which is useful evidence for a
cross-platform browser backend, not for this repository's native desktop path.
No public OpenAI repository found in this audit exposes a native Windows or
Linux desktop backend to clone or port.

The public Cua driver repository <https://github.com/trycua/cua> documents a
cross-platform native direction. The inspected main commit was
`ef0a745b94a8578239561100cacf22dc41ef9431`. Relevant paths:

- `libs/cua-driver/rust/crates/platform-windows`: UI Automation for trees,
  MSAA fallback for SAL/VCL apps, Win32 enumeration, UIA/PostMessage/SendInput
  input dispatch, and WGC/PrintWindow/GDI screenshot capture.
- `libs/cua-driver/rust/crates/platform-linux`: AT-SPI tree walking, X11 window
  enumeration, XSendEvent input, and X11 screenshot capture through
  ImageMagick or XGetImage.
- `libs/cua-driver/rust/Skills/cua-driver/WINDOWS.md` and `LINUX.md`: the
  platform contracts and caveats.

TryCUA's Windows path is the strongest native parity target: default background
dispatch first, explicit foreground dispatch only when needed, and structured
errors when background delivery is known to drop. Its Linux path is still beta:
X11 can target windows with XSendEvent, but some apps reject synthetic events;
XTest-style fallbacks route through the focused window; Wayland depends on
remote-desktop portals; and AT-SPI availability varies by desktop and distro.

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

`computeruse.PlatformStatus` reports the compiled backend and the capabilities
that are present or missing. The non-Darwin command prints that report before
exiting so missing Windows or Linux prerequisites can become explicit backend
probes instead of silent no-op behavior.

`computeruse.Backend` is the package-level contract for the next native
implementations. It separates app/window state, input, screenshots, and
intervention monitoring while keeping native element handles inside
state-bound snapshots. Windows and Linux backends should implement that
interface instead of exposing UIA, MSAA, AT-SPI, X11, WGC, or portal handles
through tool responses.

## Upstream-Backed Backlog

The next code milestones should keep the current `cmd/computer-use-mcp`
contract and replace the unsupported stubs behind it.

1. Wire the existing Darwin implementation through `computeruse.Backend` while
   keeping the current MCP schema and state_id behavior unchanged.
2. Add a Windows state backend. Use Win32 process/window enumeration, HWND as
   the native window id, UI Automation for the tree, and a retained per-state
   element cache. Add an MSAA fallback for apps whose UIA providers hang or lose
   role fidelity. Preserve axmcp's `state_id` refresh contract instead of
   exposing reusable raw element handles to callers.
3. Add Windows screenshot and coordinate handling. Mirror TryCUA's target:
   WGC for DirectComposition/XAML hosts, PrintWindow for normal windows,
   screen-region BitBlt only as a fallback with an occlusion warning, and DWM
   frame cropping so returned image pixels map to the same origin used by pixel
   actions. Preserve the existing 1568 px long-side cap and returned-dimension
   coordinate contract.
4. Add Windows input dispatch. Prefer element-index UIA patterns
   (`Invoke`, `Value`, `Toggle`, `SelectionItem`, `ExpandCollapse`) for
   background actions. For pixel actions, hit-test UIA first, then PostMessage
   to the target HWND. Return a structured `background_unavailable` result when
   background dispatch is known to drop, and require an explicit foreground
   option before using SendInput.
5. Add a Linux backend in narrower phases. Start with X11: list windows, capture
   with XGetImage or a checked external helper, walk AT-SPI when the bus is
   available, perform element actions through AT-SPI, and use XSendEvent for
   window-targeted pixels/keys. Treat Wayland input as passive or portal-gated
   until compositor support is detected.
6. Expand `PlatformStatus` into a real doctor surface. Windows should report
   interactive-session status, UIA reachability, WGC availability, integrity
   level risks, and screen-capture readiness. Linux should report X11 versus
   Wayland, AT-SPI bus reachability, XTest availability, portal availability,
   and screenshot helper availability.
7. Add tests before broad implementation: contract tests against fake backends,
   cross-`GOOS` compile tests for stubs, and host-gated integration tests that
   prove one calculator/notepad-class workflow on Windows and one GTK/Qt
   workflow on Linux.

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
