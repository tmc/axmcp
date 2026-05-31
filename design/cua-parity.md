# CUA and OpenAI Computer Use parity

This note records the current compatibility boundary against:

- CUA Driver:
  <https://cua.ai/docs/cua-driver/guide/getting-started/introduction>
- CUA Driver comparison:
  <https://cua.ai/docs/cua-driver/guide/getting-started/comparison>
- OpenAI Computer Use:
  <https://developers.openai.com/api/docs/guides/tools-computer-use>

## Upstream snapshot

Refreshed on 2026-05-31:

- OpenAI `openai/openai-cua-sample-app` main
  `3751c8baa6376c0bbf6cceea2cdc0c0b42996e03` is browser-focused. Its
  `native` mode gives the Responses API a `computer` tool, executes returned
  actions with Playwright in `packages/runner-core/src/responses-loop.ts`, and
  returns browser screenshots from `page.screenshot`. It is useful for
  action-batch and screenshot-loop semantics, but it is not a native
  Windows/Linux desktop backend.
- TryCUA `trycua/cua` main
  `ef0a745b94a8578239561100cacf22dc41ef9431` has the native parity target.
  Windows code under `libs/cua-driver/rust/crates/platform-windows` combines
  UIA, MSAA fallback, Win32 window/process discovery, UIA/PostMessage/SendInput
  input dispatch, and WGC/PrintWindow/GDI screenshot capture. Linux code under
  `libs/cua-driver/rust/crates/platform-linux` combines AT-SPI, X11 window
  discovery, XSendEvent input, and X11 screenshot capture; its public docs still
  mark Linux as beta and compositor-dependent.

## Current match

- `cmd/computer-use-mcp` is the Codex-compatible MCP surface in this
  repo. It exposes the core `list_apps`, `get_app_state`, `click`,
  `perform_secondary_action`, `set_value`, `scroll`, `drag`,
  `press_key`, and `type_text` tools over stdio, plus
  `set_recording`, `replay_trajectory`, `evaluate_javascript`, and
  `evaluate_cdp_javascript` extensions.
- The action loop is stateful: call `get_app_state` each turn, pass the
  returned `state_id` to actions, and refresh state when an action
  reports stale or missing state.
- `get_app_state` returns an accessibility tree plus a screenshot by
  default. `capture_mode` accepts `som`, `ax`, or `vision`;
  `omit_screenshot=true` suppresses the PNG when a caller only needs
  metadata and element IDs.
- Element-index actions use Accessibility when available. Pixel actions
  can target screenshot coordinates, with `foreground_hid=true` as an
  explicit fallback for opaque canvas, WebGL, Metal, and game-like views
  that reject background events.
- OpenAI's first-party Computer Use tool can return multiple low-level
  actions in one `computer_call.actions` batch. This MCP surface exposes
  single action tools and uses `replay_trajectory` for recorded multi-step
  replay rather than accepting arbitrary model-emitted action batches.

## CUA Driver comparison

CUA Driver documents three capture modes: `vision` for pixels, `ax` for
the accessibility tree, and `som` for both. axmcp exposes these as the
optional `get_app_state.capture_mode` switch:

- `get_app_state` or `capture_mode="som"`: returns both the AX tree and
  a screenshot.
- `capture_mode="ax"` or `omit_screenshot=true`: keeps element metadata
  and IDs without the PNG payload.
- `capture_mode="vision"`: returns app/window/image state without the
  AX tree in the response. The stored state keeps the AX snapshot so
  actions using the returned `state_id` remain valid.
- `ax_screenshot`, `ax_ocr`, and pixel-coordinate click paths:
  vision-oriented helper paths, not a separate Computer Use mode.

CUA Driver also documents a default no-foreground contract. axmcp's
default direction is background operation through Accessibility and
pid-routed input where possible, but it is not a full no-foreground
equivalent. `click.foreground_hid` intentionally activates the target
application and may steal focus; it exists for targets that reject
background delivery.

CUA Driver caps returned PNGs at a 1568 px long side by default so the
image and pixel-click coordinate space match without client scaling.
`cmd/computer-use-mcp` applies the same long-side cap to
`get_app_state` screenshots and reports the capped `screenshot_width`
and `screenshot_height`; pixel coordinates are interpreted in that
returned image space. Clients that downscale screenshots further before
model input must remap coordinates before calling pixel actions.

## Windows and Linux backlog

The backlog below is ordered so each milestone creates a usable, testable
artifact rather than a broad rewrite.

- Backend boundary: keep `cmd/computer-use-mcp` stable and introduce a small
  `internal/computeruse` backend interface for app discovery, window state,
  screenshots, input, and intervention monitoring. Darwin remains the first
  backend; non-Darwin stubs remain the fallback.
- Windows state: enumerate apps/windows with Win32, use HWND as `window_id`,
  walk UIA with batched property/pattern reads, cache element handles only
  behind axmcp `state_id`, and add an MSAA fallback for SAL/VCL-style
  providers.
- Windows pixels: return one screenshot coordinate frame per window. Match
  TryCUA's WGC/PrintWindow/screen-region fallback stack, preserve occlusion
  warnings when screen-region capture is used, and keep the existing 1568 px
  cap plus returned-dimension coordinate mapping.
- Windows input: element-index actions should prefer UIA patterns; pixel
  actions should try UIA hit-test, then PostMessage. SendInput must require an
  explicit foreground option and should report when background dispatch is
  unavailable instead of silently stealing focus.
- Linux state: start with X11 plus AT-SPI. Treat AT-SPI trees as partial and
  permission-dependent; return clear platform status when the bus is missing.
- Linux pixels/input: capture X11 windows with XGetImage or a checked helper,
  use XSendEvent for target-window pixels and keys, and report when an app or
  Wayland compositor forces focus-sensitive or passive-only behavior.
- Doctor and tests: make `PlatformStatus` report native prerequisites, then add
  fake-backend contract tests, cross-`GOOS` stub compile tests, and host-gated
  Windows/Linux smoke workflows before expanding action coverage.

## Known gaps

- No cross-platform CUA parity yet. Non-darwin builds compile through
  unsupported stubs for installability; functional Windows and Linux
  desktop automation backends are not implemented.
- OpenAI's public sample does not currently provide a native Windows/Linux
  desktop implementation to port; TryCUA is the stronger public native
  reference.
- No claim that every action preserves the user's foreground app.
- No built-in executor for arbitrary OpenAI `computer_call.actions`
  batches.
- `SLEventPostToPid` support is present in `internal/skylightinput`,
  but app coverage still depends on target behavior and fallback paths.
- `internal/axpump` exists to keep Chromium-family AX trees populated,
  but it is an implementation helper, not a public event stream or
  daemon contract.
