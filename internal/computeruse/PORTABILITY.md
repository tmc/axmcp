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

Non-Darwin builds now get unsupported stubs for the Darwin-only helper
packages, so the package set can compile without pulling in the Apple
dependency chain. `cmd/computer-use-mcp` starts as an MCP server on Windows and
Linux with state tools wired to platform window metadata. Windows uses
`internal/computeruse/winstate` to enumerate visible Win32 windows and build a
minimal window-metadata snapshot with a PrintWindow/GDI screenshot; UI
Automation trees are still missing. Linux uses `internal/computeruse/linuxstate`
for X11 window metadata through `wmctrl -lpG`; AT-SPI trees, screenshots,
input, and Wayland support are still missing. Action tools remain registered
but return explicit unsupported errors until input backends land. Non-Darwin
servers expose `mcp://platform/status` so clients can inspect the compiled
backend and prerequisite probes.

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
that are present or missing. Windows and Linux expose that report through
`mcp://platform/status`. Linux reports both `DISPLAY` and `wmctrl` availability
because the current X11 state backend shells out to `wmctrl -lpG`. State
backends still surface missing prerequisites through `list_apps` and
`get_app_state` errors instead of silently returning empty state.

`computeruse.Backend` is the package-level contract for native
implementations. It separates app/window state, input, screenshots, and
intervention monitoring while keeping native element handles inside
state-bound snapshots. `cmd/computer-use-mcp` now routes Darwin app-state
capture, replay state binding, and action execution through that backend, and
exposes a Darwin input adapter over the existing Accessibility, CoreGraphics,
and SkyLight paths. Windows and Linux backends should implement the same
interface instead of exposing UIA, MSAA, AT-SPI, X11, WGC, or portal handles
through tool responses. `internal/computeruse/winstate` is the first Windows
state slice: it uses Win32 top-level windows for app resolution and a root
window node plus a PNG screenshot captured with PrintWindow and a GDI BitBlt
fallback, leaving UIA/MSAA element trees for the next state slice. The Windows
command now serves that state through the normal MCP `list_apps` and
`get_app_state` tools. `internal/computeruse/linuxstate` mirrors that boundary
for X11: it resolves apps from `wmctrl -lpG` output and returns a root window
node while AT-SPI and Wayland-specific state remain future work. The Linux
command serves that state through the same MCP tools.

## Upstream-Backed Backlog

The next code milestones should keep the current `cmd/computer-use-mcp`
contract and replace the unsupported stubs behind it.

1. Expand the Windows state backend. Keep HWNDs inside retained snapshots, add
   UI Automation for the tree, and add an MSAA fallback for apps whose UIA
   providers hang or lose role fidelity. Preserve axmcp's `state_id` refresh
   contract instead of exposing reusable raw element handles to callers.
2. Expand Windows screenshot and coordinate handling. Add WGC for
   DirectComposition/XAML hosts, DWM frame cropping, and explicit occlusion
   warnings when only BitBlt is available. Preserve the existing 1568 px
   long-side cap and returned-dimension coordinate contract.
3. Add Windows input dispatch. Prefer element-index UIA patterns
   (`Invoke`, `Value`, `Toggle`, `SelectionItem`, `ExpandCollapse`) for
   background actions. For pixel actions, hit-test UIA first, then PostMessage
   to the target HWND. Return a structured `background_unavailable` result when
   background dispatch is known to drop, and require an explicit foreground
   option before using SendInput.
4. Expand the Linux backend in narrower phases. Replace or supplement the
   `wmctrl` dependency when a direct X11 path lands, capture with XGetImage or a
   checked external helper, walk AT-SPI when the bus is available, perform
   element actions through AT-SPI, and use XSendEvent for window-targeted
   pixels/keys. Treat Wayland input as passive or portal-gated until compositor
   support is detected.
5. Expand `PlatformStatus` into a real doctor surface. Windows should report
   interactive-session status, UIA reachability, WGC availability, integrity
   level risks, and screen-capture readiness. Linux should report X11 versus
   Wayland, AT-SPI bus reachability, XTest availability, portal availability,
   and screenshot helper availability.
6. Add tests before broad implementation: contract tests against fake backends,
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
