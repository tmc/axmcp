# CUA and OpenAI Computer Use parity

This note records the current compatibility boundary against:

- CUA Driver:
  <https://cua.ai/docs/cua-driver/guide/getting-started/introduction>
- CUA Driver comparison:
  <https://cua.ai/docs/cua-driver/guide/getting-started/comparison>
- OpenAI Computer Use:
  <https://developers.openai.com/api/docs/guides/tools-computer-use>

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
axmcp currently returns the captured screenshot size for
`get_app_state`; pixel coordinates are interpreted in that returned image
space. Clients that downscale screenshots before model input must remap
coordinates before calling pixel actions.

## Known gaps

- No cross-platform CUA parity. This repo targets macOS; non-darwin
  builds are for installability, not functional desktop automation.
- No claim that every action preserves the user's foreground app.
- No built-in executor for arbitrary OpenAI `computer_call.actions`
  batches.
- No built-in 1568 px screenshot long-side cap for Computer Use snapshots.
- `SLEventPostToPid` support is present in `internal/skylightinput`,
  but app coverage still depends on target behavior and fallback paths.
- `internal/axpump` exists to keep Chromium-family AX trees populated,
  but it is an implementation helper, not a public event stream or
  daemon contract.
